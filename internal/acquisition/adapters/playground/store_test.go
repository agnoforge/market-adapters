package playground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// harness is a store, the tracer provider that feeds it, and the mux that
// serves it — the whole of what these tests need.
type harness struct {
	t     *testing.T
	store *Store
	tp    *sdktrace.TracerProvider
	mux   *http.ServeMux
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := NewStore()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(store))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	mux := http.NewServeMux()
	store.Register(mux)
	return &harness{t: t, store: store, tp: tp, mux: mux}
}

// get runs one request against the playground mux and returns the status and
// the decoded body.
func (h *harness) get(path string) (int, []byte) {
	h.t.Helper()
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	res := rec.Result()
	defer res.Body.Close()
	if got := res.Header.Get("Content-Type"); got != "application/json" {
		h.t.Errorf("GET %s: Content-Type = %q, want application/json", path, got)
	}
	return res.StatusCode, rec.Body.Bytes()
}

// trace decodes GET /playground/traces/{id}, failing the test on any status
// but 200.
func (h *harness) trace(id string) map[string]any {
	h.t.Helper()
	status, body := h.get("/playground/traces/" + id)
	if status != http.StatusOK {
		h.t.Fatalf("GET /playground/traces/%s = %d, want 200: %s", id, status, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		h.t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

// spansOf pulls the span objects out of a decoded trace.
func spansOf(t *testing.T, trace map[string]any) []map[string]any {
	t.Helper()
	raw, ok := trace["spans"].([]any)
	if !ok {
		t.Fatalf("trace has no spans array: %v", trace)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, s := range raw {
		span, ok := s.(map[string]any)
		if !ok {
			t.Fatalf("span is not an object: %v", s)
		}
		out = append(out, span)
	}
	return out
}

// A trace that was recorded comes back whole: every field of the wire shape,
// the parent link between the two spans, native attribute types, the event and
// the link.
func TestTraceIsServedInTheDocumentedShape(t *testing.T) {
	h := newHarness(t)
	tracer := h.tp.Tracer("test")

	linked := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x01},
		SpanID:  trace.SpanID{0x02},
	})
	ctx, root := tracer.Start(context.Background(), "app.StartBackfill", trace.WithAttributes(
		attribute.String("agnoforge.layer", "app"),
		attribute.String("agnoforge.symbol", "BTCUSDT"),
		attribute.Int("agnoforge.page.bar_count", 500),
	))
	_, child := tracer.Start(ctx, "binance.get", trace.WithAttributes(
		attribute.String("agnoforge.layer", "provider"),
	), trace.WithLinks(trace.Link{SpanContext: linked}))
	child.AddEvent("retry", trace.WithAttributes(attribute.Int("agnoforge.retry.attempt", 2)))
	child.SetStatus(codes.Error, "boom")
	child.End()
	root.End()

	traceID := root.SpanContext().TraceID().String()
	decoded := h.trace(traceID)
	if got := decoded["trace_id"]; got != traceID {
		t.Errorf("trace_id = %v, want %q", got, traceID)
	}
	spans := spansOf(t, decoded)
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans, want 2", len(spans))
	}
	// Ordered by start time: the parent started first.
	if spans[0]["name"] != "app.StartBackfill" || spans[1]["name"] != "binance.get" {
		t.Fatalf("span order = %v, %v", spans[0]["name"], spans[1]["name"])
	}

	wantKeys := []string{"span_id", "parent_id", "name", "layer", "start", "end",
		"status", "status_message", "attributes", "events", "links"}
	for i, span := range spans {
		keys := make([]string, 0, len(span))
		for k := range span {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		want := slices.Clone(wantKeys)
		slices.Sort(want)
		if !slices.Equal(keys, want) {
			t.Errorf("span %d keys = %v, want %v", i, keys, want)
		}
	}

	// The root has no parent; the child names the root.
	if spans[0]["parent_id"] != nil {
		t.Errorf("root parent_id = %v, want null", spans[0]["parent_id"])
	}
	if got, want := spans[1]["parent_id"], root.SpanContext().SpanID().String(); got != want {
		t.Errorf("child parent_id = %v, want %q", got, want)
	}
	if got, want := spans[1]["span_id"], child.SpanContext().SpanID().String(); got != want {
		t.Errorf("child span_id = %v, want %q", got, want)
	}

	// Layer, status and times.
	if spans[0]["layer"] != "app" || spans[1]["layer"] != "provider" {
		t.Errorf("layers = %v, %v", spans[0]["layer"], spans[1]["layer"])
	}
	if spans[0]["status"] != "unset" || spans[0]["status_message"] != "" {
		t.Errorf("root status = %v/%v, want unset and no message", spans[0]["status"], spans[0]["status_message"])
	}
	if spans[1]["status"] != "error" || spans[1]["status_message"] != "boom" {
		t.Errorf("child status = %v/%v, want error/boom", spans[1]["status"], spans[1]["status_message"])
	}
	for i, span := range spans {
		for _, field := range []string{"start", "end"} {
			value, ok := span[field].(string)
			if !ok {
				t.Fatalf("span %d %s = %v, want a time", i, field, span[field])
			}
			when, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				t.Errorf("span %d %s = %q: %v", i, field, value, err)
			}
			if !strings.HasSuffix(value, "Z") {
				t.Errorf("span %d %s = %q, want UTC", i, field, value)
			}
			_ = when
		}
	}

	// Attribute values keep their own JSON types.
	attrs, ok := spans[0]["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("attributes = %v, want an object", spans[0]["attributes"])
	}
	if attrs["agnoforge.symbol"] != "BTCUSDT" {
		t.Errorf("agnoforge.symbol = %v", attrs["agnoforge.symbol"])
	}
	if count, ok := attrs["agnoforge.page.bar_count"].(float64); !ok || count != 500 {
		t.Errorf("agnoforge.page.bar_count = %#v, want the number 500", attrs["agnoforge.page.bar_count"])
	}

	// Events and links come through.
	events, ok := spans[1]["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("child events = %v, want one", spans[1]["events"])
	}
	event := events[0].(map[string]any)
	if event["name"] != "retry" {
		t.Errorf("event name = %v, want retry", event["name"])
	}
	if _, err := time.Parse(time.RFC3339Nano, event["time"].(string)); err != nil {
		t.Errorf("event time = %v: %v", event["time"], err)
	}
	if attempt := event["attributes"].(map[string]any)["agnoforge.retry.attempt"]; attempt != float64(2) {
		t.Errorf("event attribute = %#v, want 2", attempt)
	}
	links, ok := spans[1]["links"].([]any)
	if !ok || len(links) != 1 {
		t.Fatalf("child links = %v, want one", spans[1]["links"])
	}
	link := links[0].(map[string]any)
	if link["trace_id"] != linked.TraceID().String() || link["span_id"] != linked.SpanID().String() {
		t.Errorf("link = %v, want %s/%s", link, linked.TraceID(), linked.SpanID())
	}
	// The root's own events and links are empty arrays, never null.
	if got := spans[0]["events"]; !slices.Equal(got.([]any), []any{}) {
		t.Errorf("root events = %#v, want []", got)
	}
	if got := spans[0]["links"]; !slices.Equal(got.([]any), []any{}) {
		t.Errorf("root links = %#v, want []", got)
	}
}

// An id nobody recorded is a 404 in the same error shape the domain routes
// use.
func TestUnknownTraceIsNotFound(t *testing.T) {
	h := newHarness(t)
	status, body := h.get("/playground/traces/0af7651916cd43dd8448eb211c80319c")
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
	if got := strings.TrimSpace(string(body)); got != `{"error":"not found"}` {
		t.Errorf("body = %s, want the not-found error", got)
	}
}

// A span that has not ended is in the store with a null end, and gains its end
// when it finishes. This is why the store is a SpanProcessor with an OnStart
// and not an exporter.
func TestARunningSpanHasANullEnd(t *testing.T) {
	h := newHarness(t)
	_, span := h.tp.Tracer("test").Start(context.Background(), "app.HistoricalBackfill")
	id := span.SpanContext().TraceID().String()

	spans := spansOf(t, h.trace(id))
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	if spans[0]["end"] != nil {
		t.Errorf("end = %v, want null while the span runs", spans[0]["end"])
	}
	if spans[0]["start"] == nil {
		t.Error("start = null, want the start time of a running span")
	}

	span.End()
	spans = spansOf(t, h.trace(id))
	if _, ok := spans[0]["end"].(string); !ok {
		t.Errorf("end = %v, want a time once the span finished", spans[0]["end"])
	}
}

// A span that gains an attribute after it started — which is how
// app.StartBackfill learns its Backfill id, and how the request span learns
// its layer — is indexed on that attribute all the same.
func TestAttributesSetAfterStartAreIndexed(t *testing.T) {
	h := newHarness(t)
	_, span := h.tp.Tracer("test").Start(context.Background(), "app.StartBackfill")
	span.SetAttributes(attribute.String("agnoforge.backfill.id", "bf-late"))

	if got := h.backfillTraces("bf-late"); len(got) != 0 {
		t.Errorf("found %d traces before the span ended, want none", len(got))
	}
	span.End()
	traces := h.backfillTraces("bf-late")
	if len(traces) != 1 {
		t.Fatalf("found %d traces, want 1", len(traces))
	}
	if traces[0]["trace_id"] != span.SpanContext().TraceID().String() {
		t.Errorf("trace_id = %v, want %s", traces[0]["trace_id"], span.SpanContext().TraceID())
	}
}

// backfillTraces decodes GET /playground/traces?backfill_id=…
func (h *harness) backfillTraces(id string) []map[string]any {
	h.t.Helper()
	status, body := h.get("/playground/traces?backfill_id=" + id)
	if status != http.StatusOK {
		h.t.Fatalf("?backfill_id=%s = %d, want 200: %s", id, status, body)
	}
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		h.t.Fatalf("decode %s: %v", body, err)
	}
	return raw
}

// A Backfill nobody ran is an empty array, not null and not a 404.
func TestUnknownBackfillIsAnEmptyArray(t *testing.T) {
	h := newHarness(t)
	status, body := h.get("/playground/traces?backfill_id=nosuch")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got := strings.TrimSpace(string(body)); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}
}

// The query is the whole of the question: without it there is nothing to
// answer.
func TestListWithoutABackfillIDIsARequestError(t *testing.T) {
	h := newHarness(t)
	status, body := h.get("/playground/traces")
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if got := strings.TrimSpace(string(body)); got != `{"error":"backfill_id is required"}` {
		t.Errorf("body = %s", got)
	}
}

// Retention is the last 256 traces: the 257th evicts the first, and is itself
// retrievable.
func TestTheRingKeepsTheLastTwoHundredAndFiftySixTraces(t *testing.T) {
	h := newHarness(t)
	tracer := h.tp.Tracer("test")

	ids := make([]string, 0, maxTraces+1)
	for range maxTraces + 1 {
		_, span := tracer.Start(context.Background(), "app.Query")
		ids = append(ids, span.SpanContext().TraceID().String())
		span.End()
	}

	if got := h.store.size(); got != maxTraces {
		t.Errorf("store holds %d traces, want %d", got, maxTraces)
	}
	if status, _ := h.get("/playground/traces/" + ids[0]); status != http.StatusNotFound {
		t.Errorf("the first trace = %d, want 404: it should have been evicted", status)
	}
	if status, _ := h.get("/playground/traces/" + ids[1]); status != http.StatusOK {
		t.Errorf("the second trace = %d, want 200: it should still be there", status)
	}
	if status, _ := h.get("/playground/traces/" + ids[maxTraces]); status != http.StatusOK {
		t.Errorf("the 257th trace = %d, want 200", status)
	}
}

// A trace that gains a later span keeps its place in the eviction order: the
// ring is first-seen, not last-touched.
func TestEvictionIsByFirstSighting(t *testing.T) {
	h := newHarness(t)
	tracer := h.tp.Tracer("test")

	ctx, first := tracer.Start(context.Background(), "app.Query")
	firstID := first.SpanContext().TraceID().String()
	first.End()

	for range maxTraces - 1 {
		_, span := tracer.Start(context.Background(), "app.Query")
		span.End()
	}
	// A second span on the oldest trace does not renew it.
	_, late := tracer.Start(ctx, "duckdb.Bars")
	late.End()

	_, span := tracer.Start(context.Background(), "app.Query")
	span.End()
	if status, _ := h.get("/playground/traces/" + firstID); status != http.StatusNotFound {
		t.Errorf("the oldest trace = %d, want 404", status)
	}
}

// The store is written by the SDK from whichever goroutine a span runs on and
// read by the HTTP handlers at the same time. Run under -race.
func TestConcurrentSpansAndReads(t *testing.T) {
	h := newHarness(t)
	tracer := h.tp.Tracer("test")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				ctx, root := tracer.Start(context.Background(), "app.HistoricalBackfill",
					trace.WithAttributes(attribute.String("agnoforge.backfill.id", "bf-race")))
				_, child := tracer.Start(ctx, "duckdb.UpsertBars",
					trace.WithAttributes(attribute.Int("agnoforge.page.bar_count", w*i)))
				child.End()
				root.End()
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				h.get("/playground/traces?backfill_id=bf-race")
				h.get("/playground/traces/0af7651916cd43dd8448eb211c80319c")
			}
		}()
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(stop)
	}()
	wg.Wait()

	if got := h.store.size(); got > maxTraces {
		t.Errorf("store holds %d traces, want at most %d", got, maxTraces)
	}
}

// The per-trace span cap is the deliberate omission, and the code says so
// where it would go.
func TestThePonytailCommentNamesTheUpgradePath(t *testing.T) {
	source, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("read store.go: %v", err)
	}
	const want = "// ponytail: per-trace span cap if a multi-year backfill ever hurts"
	if !strings.Contains(string(source), want) {
		t.Errorf("store.go does not carry %q", want)
	}
}

// The store implements the processor contract, shutdown and flush included.
func TestShutdownAndForceFlushAreNoOps(t *testing.T) {
	store := NewStore()
	if err := store.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	if err := store.ForceFlush(context.Background()); err != nil {
		t.Errorf("ForceFlush: %v", err)
	}
	var _ sdktrace.SpanProcessor = store
}

func TestEvictionSparesATraceThatIsStillRunning(t *testing.T) {
	store := NewStore()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(store))
	tr := tp.Tracer("t")

	// The first trace stays open while ordinary traffic fills the ring.
	_, running := tr.Start(context.Background(), "app.HistoricalBackfill")
	runningID := running.SpanContext().TraceID().String()
	var second string
	for i := 0; i < maxTraces; i++ {
		_, sp := tr.Start(context.Background(), "GET /backfills/{id}")
		if i == 0 {
			second = sp.SpanContext().TraceID().String()
		}
		sp.End()
	}
	if _, ok := store.lookup(runningID); !ok {
		t.Fatal("the running trace was evicted")
	}
	if _, ok := store.lookup(second); ok {
		t.Fatal("the oldest ended trace should have been evicted instead")
	}
	running.End()
}
