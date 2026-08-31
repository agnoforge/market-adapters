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
	"sync"
	"testing"
	"time"

	acquisitionduckdb "github.com/agnos/agnoforge/internal/adapters/duckdb"
	compositeduckdb "github.com/agnos/agnoforge/internal/composite/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/composite/adapters/httpapi"
	compositeapp "github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// The declaration every test starts from, spelled the way the wire spells one.
const (
	instrument = "BTC/USD"
	baseSymbol = "BTCUSDT"
	// catchUpSymbol is the same market at the second provider — a different
	// symbol for the same Instrument, which is what a cross-provider dataset
	// asserts.
	catchUpSymbol = "BTC-USD"
)

// The two source Datasets the tests script: the base one every test uses, and
// the catch-up one only a configured cross-provider dataset ever reaches.
var (
	baseSource = domain.Source{
		Instrument: instrument, Provider: "binance",
		Symbol: acq.Symbol(baseSymbol), Timeframe: domain.TF1m,
	}
	catchUpSource = domain.Source{
		Instrument: instrument, Provider: "coinbase",
		Symbol: acq.Symbol(catchUpSymbol), Timeframe: domain.TF1m,
	}
)

// defaultBar is what a seeded bar is worth when a test does not care: a flat
// price with a close a hair above its open.
var defaultBar = acq.Bar{
	Open: "100.00000000", High: "101.00000000",
	Low: "99.00000000", Close: "100.50000000", Volume: "1.00000000",
}

// clockStart is the instant the harness's clock reads, so created_at and
// updated_at are facts a test can assert on.
var clockStart = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

// harness is one running composites API over a real composite app service and
// a real DuckDB file of its own. Nothing here reaches a network beyond its own
// httptest listener, and no database outside t.TempDir() is opened.
type harness struct {
	t   *testing.T
	url string
	// path is the database file both stores are open on. A test reads the rows
	// a Build wrote through a connection of its own.
	path string
	// store is the same real Store the service runs on. A test uses it only to
	// put a dataset in a state no route reaches directly.
	store *compositeduckdb.Store
	// source is acquisition's own store on the same database file. A test
	// seeds real source bars through it — the composite store reads them in
	// SQL, which is the data plane this context actually uses (ADR-0005).
	source *acquisitionduckdb.Store
	// port is the fake AcquisitionPort: the control plane a test scripts.
	port *fakePort
	// prices is what one source's bars are worth, for the tests that care —
	// a transition's delta is the difference between two of them.
	pricesMu sync.Mutex
	prices   map[domain.Source]string
	tick     func()
	// at reads the clock the service builds against.
	at func() time.Time
	// setNow moves that clock, which is what makes an end of `now` resolvable.
	setNow func(time.Time)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agnoforge.duckdb")
	store, err := compositeduckdb.Open(path)
	if err != nil {
		t.Fatalf("opening the composite store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	source, err := acquisitionduckdb.Open(path)
	if err != nil {
		t.Fatalf("opening the acquisition store: %v", err)
	}
	t.Cleanup(func() { source.Close() })

	now := clockStart
	var clockMu sync.Mutex
	read := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	write := func(t time.Time) {
		clockMu.Lock()
		defer clockMu.Unlock()
		now = t
	}
	port := newFakePort()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := compositeapp.New(store, port,
		compositeapp.WithLogger(logger),
		compositeapp.WithClock(read),
		// Waiting out a concurrent backfill is a real wait in production; here
		// it must not cost the test suite wall-clock time.
		compositeapp.WithBackfillRetry(time.Millisecond, 100))
	server := httptest.NewServer(httpapi.New(svc, logger))
	t.Cleanup(server.Close)

	h := &harness{
		t: t, url: server.URL, path: path, store: store, source: source, port: port,
		prices: map[domain.Source]string{},
		tick:   func() { write(read().Add(time.Hour)) },
		at:     read,
		setNow: write,
	}
	// Whatever a backfill lands really lands: the bars go into acquisition's
	// own table, for whichever source the backfill was of. No port carries a
	// bar (ADR-0005).
	port.onFill(func(src domain.Source, landed acq.Range) {
		if err := h.writeBars(src, landed); err != nil {
			t.Errorf("a backfill of %s landed %s but its bars could not be written: %v", src, landed, err)
		}
	})
	return h
}

// priceBarsOf makes every bar of one source flat at price: open, high, low and
// close alike. It is how a test gives two providers different prices, so the
// delta across the transition between them is a number it can name.
func (h *harness) priceBarsOf(src domain.Source, price string) {
	h.pricesMu.Lock()
	defer h.pricesMu.Unlock()
	h.prices[src] = price
}

// barOf is what one bar of a source is worth.
func (h *harness) barOf(src domain.Source) acq.Bar {
	h.pricesMu.Lock()
	defer h.pricesMu.Unlock()
	price, ok := h.prices[src]
	if !ok {
		return defaultBar
	}
	return acq.Bar{Open: price, High: price, Low: price, Close: price, Volume: "1.00000000"}
}

// setState moves a declared dataset into a state through the same Store the
// service uses, so a rule can be exercised over HTTP against a dataset that
// really is in it.
func (h *harness) setState(name string, state domain.State) {
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

// seedBars writes one real 1-minute bar per minute of r into acquisition's own
// bars table, in the same database file the composite store reads. This is the
// data plane: no port carries a bar (ADR-0005), so a Build's counts can only
// be right if these rows really are there.
func (h *harness) seedBars(provider, symbol string, r acq.Range) {
	h.t.Helper()
	h.seedBarsOf(domain.Source{
		Instrument: instrument, Provider: provider,
		Symbol: acq.Symbol(symbol), Timeframe: domain.TF1m,
	}, r)
}

// seedBarsOf is seedBars aimed at a source Dataset by name.
func (h *harness) seedBarsOf(src domain.Source, r acq.Range) {
	h.t.Helper()
	if err := h.writeBars(src, r); err != nil {
		h.t.Fatalf("seeding the bars of %s over %s: %v", src, r, err)
	}
}

// writeBars is seedBars without the fatal: it is also called from inside a
// Build, on the server's goroutine, where only the test goroutine may stop the
// test.
func (h *harness) writeBars(src domain.Source, r acq.Range) error {
	id := acq.DatasetID{Provider: src.Provider, Symbol: src.Symbol, Timeframe: acq.TF1m}
	template := h.barOf(src)
	var bars []acq.Bar
	for t := r.Start.UTC(); t.Before(r.End); t = t.Add(time.Minute) {
		bar := template
		bar.OpenTime = t
		bars = append(bars, bar)
	}
	if len(bars) == 0 {
		return nil
	}
	return h.source.UpsertBars(context.Background(), id, bars)
}

// providerServes says what the base provider can still serve. A backfill that
// lands really lands: the fake port extends the source's coverage, and the bars
// themselves are written into acquisition's own table — the data plane no port
// carries (ADR-0005).
func (h *harness) providerServes(r acq.Range) {
	h.t.Helper()
	h.port.serves(r)
}

// catchUpProviderServes is the same for the second provider, which only a
// dataset that explicitly configured one ever reaches.
func (h *harness) catchUpProviderServes(r acq.Range) {
	h.t.Helper()
	h.port.servesOf(catchUpSource, r)
}
