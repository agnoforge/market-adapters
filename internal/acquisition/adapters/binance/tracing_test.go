package binance_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/agnos/agnoforge/internal/acquisition/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/acquisition/adapters/httpapi"
	"github.com/agnos/agnoforge/internal/acquisition/app"
	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// This file is the one place the four layers are assembled for real — the HTTP
// adapter over the use cases over this Provider and a DuckDB Store — because
// only there can a test check that a span's parent is the span the layer above
// started. The SDK is imported by the test alone; the packages under test see
// the vendor-neutral API and nothing more.

// headerMarker is set on every fake response and must never reach an
// attribute: it stands in for a header a real Provider might send.
const headerMarker = "s3cr3t-header-value"

// recordingTracer installs an always-on TracerProvider that keeps every
// finished span in memory for the duration of one test, and takes it away
// again afterwards.
func recordingTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		_ = tp.Shutdown(context.Background())
	})
	return exporter
}

// stack is the whole service over this Provider: an in-memory Store, the use
// cases, and the HTTP adapter inside an otelhttp server span.
type stack struct {
	t        *testing.T
	svc      *app.Service
	url      string
	exporter *tracetest.InMemoryExporter
}

// newStack wires the four layers over the fake Binance s, on the fake clock c.
func newStack(t *testing.T, s *stub, c *clock) *stack {
	t.Helper()
	exporter := recordingTracer(t)

	store, err := duckdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	log := slog.New(slog.DiscardHandler)
	svc := app.New(store, []app.Provider{s.provider(c)}, app.WithLogger(log), app.WithClock(c.Now))
	srv := httptest.NewServer(otelhttp.NewHandler(httpapi.New(svc, log), "httpapi"))
	t.Cleanup(srv.Close)

	return &stack{t: t, svc: svc, url: srv.URL, exporter: exporter}
}

// post sends one JSON request and returns the status and the decoded body.
func (st *stack) post(path string, body map[string]string) (int, map[string]any) {
	st.t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		st.t.Fatalf("encode request: %v", err)
	}
	res, err := http.Post(st.url+path, "application/json", strings.NewReader(string(encoded)))
	if err != nil {
		st.t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		st.t.Fatalf("read response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		st.t.Fatalf("decode %s: %v", raw, err)
	}
	return res.StatusCode, decoded
}

// startBackfill asks for [origin, origin+minutes) and waits for the Backfill
// and its terminal work to finish, so every span it produced is exported.
func (st *stack) startBackfill(sym domain.Symbol, minutes int) string {
	st.t.Helper()
	status, body := st.post("/backfills", map[string]string{
		"provider":  "binance",
		"symbol":    string(sym),
		"timeframe": string(domain.TF1m),
		"start":     origin.Format(time.RFC3339),
		"end":       origin.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339),
	})
	if status != http.StatusAccepted {
		st.t.Fatalf("POST /backfills: status %d, want 202 (body %v)", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		st.t.Fatalf("POST /backfills: no id in %v", body)
	}
	if _, ok := st.svc.Wait(app.BackfillID(id)); !ok {
		st.t.Fatalf("backfill %q is not registered", id)
	}
	return id
}

// get sends one GET and insists on the status.
func (st *stack) get(path string, want int) {
	st.t.Helper()
	res, err := http.Get(st.url + path)
	if err != nil {
		st.t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		st.t.Fatalf("GET %s: status %d, want %d (body %s)", path, res.StatusCode, want, raw)
	}
}

// spans returns every span recorded so far.
func (st *stack) spans() tracetest.SpanStubs { return st.exporter.GetSpans() }

// only returns the single span named name, failing when there is not exactly
// one.
func only(t *testing.T, spans tracetest.SpanStubs, name string) tracetest.SpanStub {
	t.Helper()
	var found []tracetest.SpanStub
	for _, s := range spans {
		if s.Name == name {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("recorded %d spans named %q, want exactly 1 (recorded %v)", len(found), name, names(spans))
	}
	return found[0]
}

// inTrace keeps the spans belonging to one trace.
func inTrace(spans tracetest.SpanStubs, id trace.TraceID) tracetest.SpanStubs {
	var out tracetest.SpanStubs
	for _, s := range spans {
		if s.SpanContext.TraceID() == id {
			out = append(out, s)
		}
	}
	return out
}

// names lists the span names, for a failure message.
func names(spans tracetest.SpanStubs) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name)
	}
	return out
}

// serverSpan returns the one span otelhttp recorded for the request.
func serverSpan(t *testing.T, spans tracetest.SpanStubs) tracetest.SpanStub {
	t.Helper()
	var found []tracetest.SpanStub
	for _, s := range spans {
		if s.SpanKind == trace.SpanKindServer {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("recorded %d server spans, want exactly 1 (recorded %v)", len(found), names(spans))
	}
	return found[0]
}

// attr reads one attribute off a span.
func attr(s tracetest.SpanStub, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// wantAttr insists a span carries key with the given string value.
func wantAttr(t *testing.T, s tracetest.SpanStub, key, want string) {
	t.Helper()
	v, ok := attr(s, key)
	if !ok {
		t.Errorf("span %q has no %s attribute", s.Name, key)
		return
	}
	if got := v.AsString(); got != want {
		t.Errorf("span %q: %s = %q, want %q", s.Name, key, got, want)
	}
}

// events returns every event of a span with the given name.
func events(s tracetest.SpanStub, name string) []sdktrace.Event {
	var out []sdktrace.Event
	for _, e := range s.Events {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

// A request that starts a Backfill is one trace across three layers, each span
// the child of the one above it.
func TestRequestTraceCrossesTheLayers(t *testing.T) {
	s := serving(t, bars(origin, domain.TF1m, 10))
	st := newStack(t, s, newClock(origin.Add(time.Hour)))
	st.startBackfill(symbol, 10)

	server := serverSpan(t, st.spans())
	request := inTrace(st.spans(), server.SpanContext.TraceID())

	start := only(t, request, "app.StartBackfill")
	if start.Parent.SpanID() != server.SpanContext.SpanID() {
		t.Errorf("app.StartBackfill parent = %s, want the server span %s",
			start.Parent.SpanID(), server.SpanContext.SpanID())
	}
	wantAttr(t, start, "agnoforge.layer", "app")
	wantAttr(t, start, "agnoforge.provider", "binance")
	wantAttr(t, start, "agnoforge.symbol", string(symbol))
	wantAttr(t, start, "agnoforge.timeframe", "1m")

	earliest := only(t, request, "binance.EarliestAvailable")
	if earliest.Parent.SpanID() != start.SpanContext.SpanID() {
		t.Errorf("binance.EarliestAvailable parent = %s, want app.StartBackfill %s",
			earliest.Parent.SpanID(), start.SpanContext.SpanID())
	}
	wantAttr(t, earliest, "agnoforge.layer", "provider")

	get := only(t, request, "binance.get")
	if get.Parent.SpanID() != earliest.SpanContext.SpanID() {
		t.Errorf("binance.get parent = %s, want binance.EarliestAvailable %s",
			get.Parent.SpanID(), earliest.SpanContext.SpanID())
	}
}

// The worker runs after the response, so it runs in a trace of its own: a new
// root, linked back to the request span and named by the Backfill it is.
func TestWorkerRunsInItsOwnLinkedTrace(t *testing.T) {
	s := serving(t, bars(origin, domain.TF1m, 10))
	st := newStack(t, s, newClock(origin.Add(time.Hour)))
	id := st.startBackfill(symbol, 10)

	spans := st.spans()
	server := serverSpan(t, spans)
	start := only(t, inTrace(spans, server.SpanContext.TraceID()), "app.StartBackfill")

	root := only(t, spans, "app.HistoricalBackfill")
	if root.SpanContext.TraceID() == server.SpanContext.TraceID() {
		t.Errorf("app.HistoricalBackfill trace = %s, want a trace of its own", root.SpanContext.TraceID())
	}
	if root.Parent.IsValid() {
		t.Errorf("app.HistoricalBackfill parent = %s, want a root span", root.Parent.SpanID())
	}
	wantAttr(t, root, "agnoforge.layer", "app")
	wantAttr(t, root, "agnoforge.backfill.id", id)

	if len(root.Links) != 1 {
		t.Fatalf("app.HistoricalBackfill has %d links, want the request span", len(root.Links))
	}
	if got := root.Links[0].SpanContext.SpanID(); got != start.SpanContext.SpanID() {
		t.Errorf("link = %s, want app.StartBackfill %s", got, start.SpanContext.SpanID())
	}

	worker := inTrace(spans, root.SpanContext.TraceID())
	for _, name := range []string{"binance.Bars", "binance.get", "duckdb.UpsertBars", "duckdb.ExtendCoverage", "app.DetectGaps"} {
		found := false
		for _, sp := range worker {
			if sp.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("the worker trace has no %q span (recorded %v)", name, names(worker))
		}
	}
	gaps := only(t, worker, "app.DetectGaps")
	if _, ok := attr(gaps, "agnoforge.gap.count"); !ok {
		t.Errorf("app.DetectGaps has no agnoforge.gap.count attribute")
	}
	upsert := only(t, worker, "duckdb.UpsertBars")
	if v, ok := attr(upsert, "agnoforge.page.bar_count"); !ok || v.AsInt64() != 10 {
		t.Errorf("duckdb.UpsertBars agnoforge.page.bar_count = %v (present %t), want 10", v.AsInt64(), ok)
	}
}

// Every operation the spec names records a span under that exact name, across
// the whole surface: a Backfill, both bars queries, and a Repair.
func TestEveryNamedOperationRecordsItsSpan(t *testing.T) {
	// Minute 4 is missing, so the Backfill leaves one open Gap to repair.
	fixture := bars(origin, domain.TF1m, 10)
	fixture = append(fixture[:4:4], fixture[5:]...)
	s := serving(t, fixture)
	st := newStack(t, s, newClock(origin.Add(time.Hour)))
	st.startBackfill(symbol, 10)

	const dataset = "/datasets/binance/BTCUSDT/1m"
	window := "start=" + origin.Format(time.RFC3339) + "&end=" + origin.Add(10*time.Minute).Format(time.RFC3339)
	st.get(dataset+"/bars?format=json&"+window, http.StatusOK)
	st.get(dataset+"/bars?"+window, http.StatusOK)
	st.get(dataset+"/coverage", http.StatusOK)

	var gaps []struct {
		ID int64 `json:"id"`
	}
	res, err := http.Get(st.url + dataset + "/gaps")
	if err != nil {
		t.Fatalf("GET gaps: %v", err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if err := json.Unmarshal(raw, &gaps); err != nil || len(gaps) != 1 {
		t.Fatalf("gaps = %s (%v), want the one seeded Gap", raw, err)
	}

	status, body := st.post("/gaps/"+strconv.FormatInt(gaps[0].ID, 10)+"/repair", nil)
	if status != http.StatusAccepted {
		t.Fatalf("POST repair: status %d, want 202 (body %v)", status, body)
	}
	repairID, _ := body["backfill_id"].(string)
	if _, ok := st.svc.Wait(app.BackfillID(repairID)); !ok {
		t.Fatalf("repair backfill %q is not registered", repairID)
	}

	recorded := names(st.spans())
	for _, name := range []string{
		"app.StartBackfill", "app.HistoricalBackfill", "app.DetectGaps",
		"app.IsComplete", "app.Repair", "app.Query",
		"binance.EarliestAvailable", "binance.Bars", "binance.get",
		"duckdb.UpsertBars", "duckdb.ExtendCoverage", "duckdb.OpenTimes",
		"duckdb.ReplaceOpenGaps", "duckdb.ExportParquet", "duckdb.Bars",
		"duckdb.Coverage", "duckdb.Gaps",
	} {
		if !slices.Contains(recorded, name) {
			t.Errorf("no span named %q was recorded", name)
		}
	}
	for _, span := range st.spans() {
		if !strings.HasPrefix(span.Name, "app.") &&
			!strings.HasPrefix(span.Name, "binance.") &&
			!strings.HasPrefix(span.Name, "duckdb.") {
			continue
		}
		v, ok := attr(span, "agnoforge.layer")
		if !ok {
			t.Errorf("span %q has no agnoforge.layer attribute", span.Name)
			continue
		}
		if got := v.AsString(); got != "app" && got != "provider" && got != "store" {
			t.Errorf("span %q: agnoforge.layer = %q", span.Name, got)
		}
	}
}

// An unknown Symbol is the Provider's verdict: the Provider span carries it as
// an error, and the request is a plain 400 the server span does not call a
// failure.
func TestUnknownSymbolFailsTheProviderSpanOnly(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, q url.Values, _ int) {
		w.Header().Set("X-Secret-Header", headerMarker)
		writeJSON(w, http.StatusBadRequest, []byte(`{"code":-1121,"msg":"Invalid symbol."}`))
	})
	st := newStack(t, s, newClock(origin.Add(time.Hour)))

	status, body := st.post("/backfills", map[string]string{
		"provider":  "binance",
		"symbol":    "NOSUCHPAIR",
		"timeframe": string(domain.TF1m),
		"start":     origin.Format(time.RFC3339),
		"end":       origin.Add(10 * time.Minute).Format(time.RFC3339),
	})
	if status != http.StatusBadRequest {
		t.Fatalf("POST /backfills: status %d, want 400 (body %v)", status, body)
	}

	spans := st.spans()
	earliest := only(t, spans, "binance.EarliestAvailable")
	if earliest.Status.Code != codes.Error {
		t.Errorf("binance.EarliestAvailable status = %v, want %v", earliest.Status.Code, codes.Error)
	}
	if len(events(earliest, "exception")) != 1 {
		t.Errorf("binance.EarliestAvailable events = %v, want one exception", earliest.Events)
	}
	if server := serverSpan(t, spans); server.Status.Code == codes.Error {
		t.Errorf("server span status = %v, want %v for a 400", server.Status.Code, codes.Unset)
	}
}

// Every retry and every rate-limit wait is an event on the one span the page
// fetch is, never a span of its own.
func TestGetRecordsOneEventPerRetry(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, q url.Values, call int) {
		w.Header().Set("X-Secret-Header", headerMarker)
		if call <= 2 {
			writeJSON(w, http.StatusBadGateway, []byte(`bad gateway`))
			return
		}
		writeJSON(w, http.StatusOK, encode(slice(t, bars(origin, domain.TF1m, 1), q)))
	})
	exporter := recordingTracer(t)
	c := newClock(origin.Add(time.Hour))
	if _, err := s.provider(c).EarliestAvailable(context.Background(), symbol); err != nil {
		t.Fatalf("EarliestAvailable: %v", err)
	}

	get := only(t, exporter.GetSpans(), "binance.get")
	retries := events(get, "retry")
	if len(retries) != 2 {
		t.Fatalf("binance.get has %d retry events, want 2 (one per retried attempt)", len(retries))
	}
	for i, want := range []int64{500, 1000} {
		if got := eventAttr(t, retries[i], "agnoforge.retry.delay_ms").AsInt64(); got != want {
			t.Errorf("retry %d delay = %dms, want %dms", i+1, got, want)
		}
		if got := eventAttr(t, retries[i], "agnoforge.retry.attempt").AsInt64(); got != int64(i+2) {
			t.Errorf("retry %d attempt = %d, want %d", i+1, got, i+2)
		}
	}
	if len(events(get, "rate_limit_wait")) != 0 {
		t.Errorf("binance.get waited on the rate limit inside the budget: %v", get.Events)
	}
}

// Exhausting the budget is an event on the page fetch that had to wait.
func TestGetRecordsOneEventPerRateLimitWait(t *testing.T) {
	s := serving(t, bars(origin, domain.TF1m, 1))
	c := newClock(origin.Add(time.Hour))
	p := s.provider(c)
	ctx := context.Background()

	// Spend the whole 6000-weight budget before anything is recorded, so the
	// exporter holds only the request that had to wait for the refill.
	for range 3000 {
		if _, err := p.EarliestAvailable(ctx, symbol); err != nil {
			t.Fatalf("EarliestAvailable: %v", err)
		}
	}
	exporter := recordingTracer(t)
	if _, err := p.EarliestAvailable(ctx, symbol); err != nil {
		t.Fatalf("EarliestAvailable past the budget: %v", err)
	}

	get := only(t, exporter.GetSpans(), "binance.get")
	waits := events(get, "rate_limit_wait")
	if len(waits) != 1 {
		t.Fatalf("binance.get has %d rate_limit_wait events, want 1 (%v)", len(waits), get.Events)
	}
	if got := eventAttr(t, waits[0], "agnoforge.rate_limit.wait_ms").AsInt64(); got != 20 {
		t.Errorf("rate_limit_wait = %dms, want 20ms for 2 weight at 6000/min", got)
	}
}

// eventAttr reads one attribute off a span event.
func eventAttr(t *testing.T, e sdktrace.Event, key string) attribute.Value {
	t.Helper()
	for _, kv := range e.Attributes {
		if string(kv.Key) == key {
			return kv.Value
		}
	}
	t.Fatalf("event %q has no %s attribute (%v)", e.Name, key, e.Attributes)
	return attribute.Value{}
}

// Nothing this repository puts on a span is a request or a response: not a
// body, not a header, not a query string. Only Symbols, Timeframes, ranges and
// counts.
func TestNoSpanCarriesABodyOrAHeader(t *testing.T) {
	fixture := bars(origin, domain.TF1m, 10)
	body := string(encode(fixture))
	s := newStub(t, func(w http.ResponseWriter, q url.Values, _ int) {
		w.Header().Set("X-Secret-Header", headerMarker)
		writeJSON(w, http.StatusOK, encode(slice(t, fixture, q)))
	})
	st := newStack(t, s, newClock(origin.Add(time.Hour)))
	st.startBackfill(symbol, 10)

	forbidden := []string{"http.request", "body", "header"}
	for _, span := range st.spans() {
		ours := strings.HasPrefix(span.Name, "app.") ||
			strings.HasPrefix(span.Name, "binance.") ||
			strings.HasPrefix(span.Name, "duckdb.")
		check := func(where string, attrs []attribute.KeyValue) {
			for _, kv := range attrs {
				key := strings.ToLower(string(kv.Key))
				if ours {
					for _, bad := range forbidden {
						if strings.Contains(key, bad) {
							t.Errorf("%s %s carries %q", span.Name, where, kv.Key)
						}
					}
				}
				v := kv.Value.Emit()
				if strings.Contains(v, headerMarker) {
					t.Errorf("%s %s: %s carries a response header value", span.Name, where, kv.Key)
				}
				if strings.Contains(v, body[:32]) {
					t.Errorf("%s %s: %s carries a response body", span.Name, where, kv.Key)
				}
			}
		}
		check("attribute", span.Attributes)
		for _, e := range span.Events {
			check("event "+e.Name, e.Attributes)
		}
	}
}
