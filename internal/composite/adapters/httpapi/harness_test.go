package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	compositeduckdb "github.com/agnos/agnoforge/internal/composite/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/composite/adapters/httpapi"
	compositeapp "github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// The declaration every test starts from, spelled the way the wire spells one.
const (
	instrument = "BTC/USD"
	baseSymbol = "BTCUSDT"
)

// clockStart is the instant the harness's clock reads, so created_at and
// updated_at are facts a test can assert on.
var clockStart = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

// harness is one running composites API over a real composite app service and
// a real DuckDB file of its own. Nothing here reaches a network beyond its own
// httptest listener, and no database outside t.TempDir() is opened.
type harness struct {
	t   *testing.T
	url string
	// store is the same real Store the service runs on. A test uses it only to
	// stand in for what a Build will later do to a dataset's state, which no
	// route can do yet.
	store *compositeduckdb.Store
	tick  func()
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store, err := compositeduckdb.Open(filepath.Join(t.TempDir(), "agnoforge.duckdb"))
	if err != nil {
		t.Fatalf("opening the composite store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	now := clockStart
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := compositeapp.New(store,
		compositeapp.WithLogger(logger),
		compositeapp.WithClock(func() time.Time { return now }))
	server := httptest.NewServer(httpapi.New(svc, logger))
	t.Cleanup(server.Close)

	return &harness{t: t, url: server.URL, store: store, tick: func() { now = now.Add(time.Hour) }}
}

// build stands in for the Build this ticket does not have yet: it moves a
// declared dataset into a built state through the same Store the service uses,
// so the edit rule can be exercised over HTTP against a dataset that has
// really been built.
func (h *harness) build(name string, state domain.State) {
	h.t.Helper()
	ctx := context.Background()
	d, err := h.store.Dataset(ctx, domain.Name(name))
	if err != nil {
		h.t.Fatalf("reading %q back: %v", name, err)
	}
	d.State = state
	if err := h.store.UpdateDataset(ctx, d); err != nil {
		h.t.Fatalf("moving %q to %q: %v", name, state, err)
	}
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

// decode reads a JSON response into v, insisting on the status and the JSON
// content type first.
func (h *harness) decode(res *http.Response, want int, v any) {
	h.t.Helper()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("reading the response body: %v", err)
	}
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
// API answers every failure with, and hands back the message.
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

// compositeJSON mirrors the wire shape of one Composite Dataset, which is
// what every test reads back.
type compositeJSON struct {
	Name       string     `json:"name"`
	Instrument string     `json:"instrument"`
	Base       sourceJSON `json:"base"`
	CatchUp    struct {
		Kind       string `json:"kind"`
		Instrument string `json:"instrument"`
		Provider   string `json:"provider"`
		Symbol     string `json:"symbol"`
		Timeframe  string `json:"timeframe"`
	} `json:"catch_up"`
	RequestedStart string   `json:"requested_start"`
	RequestedEnd   string   `json:"requested_end"`
	Timeframes     []string `json:"timeframes"`
	Mode           string   `json:"mode"`
	State          string   `json:"state"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

type sourceJSON struct {
	Instrument string `json:"instrument"`
	Provider   string `json:"provider"`
	Symbol     string `json:"symbol"`
	Timeframe  string `json:"timeframe"`
}

// declaration is the smallest well-formed body: one 1-minute base source over
// a fixed range, with two materialized timeframes.
func declaration() map[string]any {
	return map[string]any{
		"instrument": instrument,
		"base": map[string]any{
			"instrument": instrument,
			"provider":   "binance",
			"symbol":     baseSymbol,
			"timeframe":  "1m",
		},
		"requested_start": "2024-01-01T00:00:00Z",
		"requested_end":   "2024-02-01T00:00:00Z",
		"timeframes":      []string{"1h", "5m"},
		"mode":            "strict",
	}
}

// named is declaration() with a name, ready to POST.
func named(name string) map[string]any {
	body := declaration()
	body["name"] = name
	return body
}

// assertSame insists two responses describe the same Composite Dataset, field
// for field.
func assertSame(t *testing.T, got, want compositeJSON, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %+v, want %s %+v", got, what, want)
	}
}

// create declares one Composite Dataset and insists it worked.
func (h *harness) create(body map[string]any) compositeJSON {
	h.t.Helper()
	var created compositeJSON
	h.decode(h.do("POST", "/composites", body), http.StatusCreated, &created)
	return created
}
