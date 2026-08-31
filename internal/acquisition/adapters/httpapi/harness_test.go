package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/acquisition/adapters/httpapi"
	"github.com/agnos/agnoforge/internal/acquisition/app"
	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// The Dataset every test acquires: a fake Provider, so nothing here reaches a
// network, and a Timeframe short enough to write ten Bars of by hand.
const (
	providerName  = "fake"
	symbol        = domain.Symbol("BTCUSDT")
	tf            = domain.TF1m
	unknownSymbol = domain.Symbol("NOSUCHPAIR")
)

// origin is the open_time of the first Bar of every fixture.
var origin = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// at returns the instant minutes after origin, spelled the way the API spells
// one.
func at(minutes int) string {
	return origin.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339)
}

// harness is one running API over a real in-memory Store and a fake Provider.
type harness struct {
	t        *testing.T
	provider *fakeProvider
	svc      *app.Service
	url      string
}

// newHarness starts the API over a Provider whose only page holds nine of the
// ten Bars of [origin, origin+10m): minute 4 is missing, so a Backfill of that
// range leaves exactly one open Gap.
func newHarness(t *testing.T) *harness {
	t.Helper()
	provider := &fakeProvider{
		name:       providerName,
		timeframes: []domain.Timeframe{domain.TF1m, domain.TF1h},
		earliest:   origin,
		unknown:    unknownSymbol,
		pages:      [][]domain.Bar{testBarsAt(origin, tf, 0, 1, 2, 3, 5, 6, 7, 8, 9)},
	}
	return newHarnessWith(t, provider)
}

func newHarnessWith(t *testing.T, provider *fakeProvider) *harness {
	t.Helper()
	store, err := duckdb.Open(":memory:")
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := app.New(store, []app.Provider{provider}, app.WithLogger(logger))
	server := httptest.NewServer(httpapi.New(svc, logger))
	t.Cleanup(server.Close)

	return &harness{t: t, provider: provider, svc: svc, url: server.URL}
}

// do sends one request. A nil body sends none; anything else is sent as JSON,
// except a string, which is sent verbatim so a test can send invalid JSON.
func (h *harness) do(method, path string, body any) *http.Response {
	h.t.Helper()
	var reader io.Reader
	switch v := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(v)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			h.t.Fatalf("encoding the request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, h.url+path, reader)
	if err != nil {
		h.t.Fatalf("building the request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	h.t.Cleanup(func() { res.Body.Close() })
	return res
}

// body reads the whole response body.
func (h *harness) body(res *http.Response) []byte {
	h.t.Helper()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("reading the response body: %v", err)
	}
	return raw
}

// decode reads a JSON response into v, insisting on the status and the JSON
// content type first.
func (h *harness) decode(res *http.Response, want int, v any) {
	h.t.Helper()
	raw := h.body(res)
	if res.StatusCode != want {
		h.t.Fatalf("%s: status %d, want %d (body %s)", res.Request.URL.Path, res.StatusCode, want, raw)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		h.t.Fatalf("%s: content type %q, want application/json", res.Request.URL.Path, ct)
	}
	if v == nil {
		return
	}
	if err := json.Unmarshal(raw, v); err != nil {
		h.t.Fatalf("%s: decoding %s: %v", res.Request.URL.Path, raw, err)
	}
}

// expectError insists on a failed request reported as the JSON {error} this
// API answers every failure with.
func (h *harness) expectError(res *http.Response, want int) string {
	h.t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	h.decode(res, want, &body)
	if body.Error == "" {
		h.t.Fatalf("%s: %d carried an empty error message", res.Request.URL.Path, want)
	}
	return body.Error
}

// backfill starts a Backfill over [origin+from, origin+to) and waits for it to
// settle, so the Dataset is in its final shape before the test asks anything.
func (h *harness) backfill(from, to int) string {
	h.t.Helper()
	res := h.do("POST", "/backfills", map[string]string{
		"provider": providerName, "symbol": string(symbol), "timeframe": tf.String(),
		"start": at(from), "end": at(to),
	})
	var started struct {
		ID             string `json:"id"`
		EffectiveRange struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"effective_range"`
	}
	h.decode(res, http.StatusAccepted, &started)
	h.wait(started.ID)
	return started.ID
}

// wait blocks until the Backfill and its gap detection are finished.
func (h *harness) wait(id string) {
	h.t.Helper()
	if _, ok := h.svc.Wait(app.BackfillID(id)); !ok {
		h.t.Fatalf("backfill %q is not registered", id)
	}
}

// gapJSON mirrors the wire shape of one Gap, which several tests read back.
type gapJSON struct {
	ID      int64 `json:"id"`
	Dataset struct {
		Provider  string `json:"provider"`
		Symbol    string `json:"symbol"`
		Timeframe string `json:"timeframe"`
	} `json:"dataset"`
	Range struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"range"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// seededGap is the one open Gap a full Backfill of [origin, origin+10m)
// leaves: minute 4, which the Provider's page does not hold.
func (h *harness) seededGap() gapJSON {
	h.t.Helper()
	var gaps []gapJSON
	h.decode(h.do("GET", "/datasets/fake/BTCUSDT/1m/gaps", nil), http.StatusOK, &gaps)
	if len(gaps) != 1 {
		h.t.Fatalf("gaps = %d, want the single seeded one: %+v", len(gaps), gaps)
	}
	return gaps[0]
}

// itoa spells a Gap id the way a URL carries one.
func itoa(id int64) string { return strconv.FormatInt(id, 10) }

// backfillID is the id a Backfill was started under, back in its own type.
func backfillID(id string) app.BackfillID { return app.BackfillID(id) }
