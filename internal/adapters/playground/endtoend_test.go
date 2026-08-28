package playground

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/agnos/agnoforge/internal/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/adapters/httpapi"
	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/domain"
)

// This file stands where the UI stands: it knows the service only through the
// same HTTP endpoints the page uses, and it mirrors the command's wiring —
// one mux, the playground registered on it, the API under the catch-all, the
// whole thing wrapped in otelhttp with /playground/ filtered out. Everything
// it asserts is a thing the page does on Execute.

// instantProvider yields the requested Bars in one page and returns, so a
// Backfill started through HTTP finishes without anything having to be
// released.
type instantProvider struct{}

func (instantProvider) Name() string { return "binance" }

func (instantProvider) SupportedTimeframes() []domain.Timeframe {
	return []domain.Timeframe{domain.TF1m}
}

func (instantProvider) EarliestAvailable(context.Context, domain.Symbol) (time.Time, error) {
	return origin, nil
}

func (instantProvider) Calendar(domain.Symbol) domain.TradingCalendar { return domain.Continuous{} }

func (instantProvider) Bars(_ context.Context, _ domain.Symbol, _ domain.Timeframe, _ domain.Range) iter.Seq2[[]domain.Bar, error] {
	return func(yield func([]domain.Bar, error) bool) {
		yield(bars(origin, 0, 2), nil)
	}
}

// traceIDResponseHeader is the command's middleware, spelled again here
// because it lives in package main: every response names the trace that
// answered it, which is the header the page reads.
func traceIDResponseHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
			w.Header().Set("X-Trace-ID", sc.TraceID().String())
		}
		next.ServeHTTP(w, r)
	})
}

// A backfill started the way the page starts one — POST /backfills through
// the same mux — comes back with a trace id the page can fetch, and the
// Backfill's own execution trace is there under ?backfill_id= once it is done.
func TestExecutingABackfillProducesATraceTheUICanFetch(t *testing.T) {
	store := NewStore()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(store))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		_ = tp.Shutdown(context.Background())
	})

	db, err := duckdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	logger := slog.New(slog.DiscardHandler)
	now := origin.Add(time.Hour)
	svc := app.New(db, []app.Provider{instantProvider{}},
		app.WithLogger(logger),
		app.WithClock(func() time.Time { return now }))

	mux := http.NewServeMux()
	store.Register(mux)
	mux.Handle("/", httpapi.New(svc, logger))
	handler := otelhttp.NewHandler(traceIDResponseHeader(mux), "httpapi",
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return !strings.HasPrefix(r.URL.Path, "/playground/")
		}))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	body := `{"provider":"binance","symbol":"BTCUSDT","timeframe":"1m",` +
		`"start":"2024-01-01T00:00:00Z","end":"2024-01-01T00:02:00Z"}`
	res, err := http.Post(server.URL+"/backfills", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /backfills: %v", err)
	}
	started, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /backfills = %d, want 202: %s", res.StatusCode, started)
	}
	var accepted struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(started, &accepted); err != nil {
		t.Fatalf("decode %s: %v", started, err)
	}
	if accepted.ID == "" {
		t.Fatalf("202 body names no Backfill id: %s", started)
	}
	traceID := res.Header.Get("X-Trace-Id")
	if traceID == "" {
		t.Fatal("the response carries no X-Trace-Id: the page would have nothing to fetch")
	}

	// The page's first move: GET /playground/traces/{id}.
	requestTrace := fetchJSON[map[string]any](t, server.URL+"/playground/traces/"+traceID)
	if got := requestTrace["trace_id"]; got != traceID {
		t.Errorf("trace_id = %v, want %q", got, traceID)
	}
	if !hasSpan(t, requestTrace, "app.StartBackfill") {
		t.Errorf("the request trace holds no app.StartBackfill span: %v", spanNames(t, requestTrace))
	}

	if _, ok := svc.Wait(app.BackfillID(accepted.ID)); !ok {
		t.Fatal("Wait reported no such Backfill")
	}

	// The page's "Execution trace" button: poll ?backfill_id= until the trace
	// rooted at app.HistoricalBackfill has no running span left.
	traces := fetchJSON[[]map[string]any](t, server.URL+"/playground/traces?backfill_id="+accepted.ID)
	var execution map[string]any
	for _, tr := range traces {
		for _, span := range spansOf(t, tr) {
			if span["parent_id"] == nil && span["name"] == "app.HistoricalBackfill" {
				execution = tr
			}
		}
	}
	if execution == nil {
		t.Fatalf("?backfill_id=%s has no trace rooted at app.HistoricalBackfill: %d trace(s)",
			accepted.ID, len(traces))
	}
	ended := 0
	for _, span := range spansOf(t, execution) {
		if span["end"] == nil {
			t.Errorf("span %v is still running after Wait: the poll would never stop", span["name"])
			continue
		}
		ended++
	}
	if ended == 0 {
		t.Fatal("the execution trace has no spans")
	}
}

// fetchJSON gets one playground document off the running server.
func fetchJSON[T any](t *testing.T, url string) T {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", url, res.StatusCode, body)
	}
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

// spanNames lists what a trace holds, for a failure message.
func spanNames(t *testing.T, trace map[string]any) []string {
	t.Helper()
	var out []string
	for _, span := range spansOf(t, trace) {
		out = append(out, span["name"].(string))
	}
	return out
}

// hasSpan reports whether the trace holds a span of that name.
func hasSpan(t *testing.T, trace map[string]any, name string) bool {
	t.Helper()
	for _, got := range spanNames(t, trace) {
		if got == name {
			return true
		}
	}
	return false
}
