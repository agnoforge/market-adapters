// Package playground serves the traces this process recorded, so a developer
// can see the path a request took without running a tracing backend beside it
// (ADR 0003).
//
// It is a SpanProcessor and two HTTP handlers, and that is the whole of it: it
// holds no Service, no Store port and no Provider, and it never learns what a
// Backfill or a Dataset is. Spans and HTTP requests are all it sees.
//
// This is the one package under internal/ that may name the OpenTelemetry SDK,
// because being a sdktrace.SpanProcessor is what it is for.
package playground

import (
	"context"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// maxTraces is the retention: the last 256 traces, oldest evicted first.
const maxTraces = 256

// The two attributes the store reads. Everything else a span carries is
// passed through untouched; these two are the ones it indexes and colours by.
const (
	layerKey      = attribute.Key("agnoforge.layer")
	backfillIDKey = attribute.Key("agnoforge.backfill.id")
)

// Store is the bounded in-process sink: a sdktrace.SpanProcessor on one side
// and the /playground/traces endpoints on the other. The zero value is not
// usable; call NewStore.
type Store struct {
	mu     sync.Mutex
	traces map[string]*traceRecord
	// order is the trace ids in the order they were first seen, which is the
	// order they are evicted in. A trace that gains a later span keeps its
	// place: it is first-seen, not last-touched.
	order []string
}

// traceRecord is one trace as the store holds it — its spans by span id, and
// the Backfill id one of those spans named, which is the secondary index.
//
// ponytail: per-trace span cap if a multi-year backfill ever hurts
type traceRecord struct {
	backfillID string
	spans      map[string]spanJSON
}

// NewStore builds an empty trace store. Hand it to the tracer provider with
// sdktrace.WithSpanProcessor and to the mux with Register.
func NewStore() *Store {
	return &Store{traces: make(map[string]*traceRecord)}
}

var _ sdktrace.SpanProcessor = (*Store)(nil)

// OnStart records the span while it is still running, so the playground can
// show a Backfill in flight rather than only after it finished. Such a span
// has no end yet, and says so.
func (s *Store) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	s.record(span, span.Attributes(), false)
}

// OnEnd replaces the running record with the finished one. The attributes are
// read again here because a span may gain them after it started — the request
// span learns its layer inside the middleware, and app.StartBackfill learns
// its Backfill id only once the Backfill exists.
func (s *Store) OnEnd(span sdktrace.ReadOnlySpan) {
	s.record(span, span.Attributes(), true)
}

// Shutdown does nothing: the store is memory that dies with the process.
func (s *Store) Shutdown(context.Context) error { return nil }

// ForceFlush does nothing: a span is in the store the moment it starts.
func (s *Store) ForceFlush(context.Context) error { return nil }

// record files one span, creating its trace and evicting the oldest when the
// trace is new.
func (s *Store) record(span sdktrace.ReadOnlySpan, attrs []attribute.KeyValue, ended bool) {
	sc := span.SpanContext()
	if !sc.IsValid() {
		return
	}
	rec := spanOf(span, attrs, ended)
	traceID := sc.TraceID().String()

	s.mu.Lock()
	defer s.mu.Unlock()
	tr, ok := s.traces[traceID]
	if !ok {
		for len(s.order) >= maxTraces {
			delete(s.traces, s.order[0])
			s.order = s.order[1:]
		}
		tr = &traceRecord{spans: make(map[string]spanJSON)}
		s.traces[traceID] = tr
		s.order = append(s.order, traceID)
	}
	if tr.backfillID == "" {
		tr.backfillID = stringAttr(attrs, backfillIDKey)
	}
	tr.spans[rec.SpanID] = rec
}

// lookup returns one trace by id.
func (s *Store) lookup(traceID string) (traceJSON, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr, ok := s.traces[traceID]
	if !ok {
		return traceJSON{}, false
	}
	return tr.snapshot(traceID), true
}

// byBackfill returns every trace carrying agnoforge.backfill.id = id, in
// first-seen order. There are normally two: the request that asked for the
// Backfill, and the Backfill's own execution trace.
func (s *Store) byBackfill(id string) []traceJSON {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]traceJSON, 0)
	for _, traceID := range s.order {
		if tr := s.traces[traceID]; tr != nil && tr.backfillID == id {
			out = append(out, tr.snapshot(traceID))
		}
	}
	return out
}

// size reports how many traces are retained.
func (s *Store) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.traces)
}

// snapshot copies the trace out from under the lock, spans ordered by start
// time. The copy is what the handler marshals, so no caller ever holds a
// reference the processor can write through.
func (tr *traceRecord) snapshot(traceID string) traceJSON {
	out := traceJSON{TraceID: traceID, Spans: make([]spanJSON, 0, len(tr.spans))}
	for _, span := range tr.spans {
		out.Spans = append(out.Spans, span)
	}
	slices.SortStableFunc(out.Spans, func(a, b spanJSON) int {
		if c := a.start.Compare(b.start); c != 0 {
			return c
		}
		return compareStrings(a.SpanID, b.SpanID)
	})
	return out
}

// compareStrings orders two span ids, which is the tie-break when two spans
// started in the same instant.
func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// spanOf converts one SDK span into the wire shape. ended says whether the
// end time is real: a running span reports a null end.
func spanOf(span sdktrace.ReadOnlySpan, attrs []attribute.KeyValue, ended bool) spanJSON {
	status := span.Status()
	out := spanJSON{
		SpanID:        span.SpanContext().SpanID().String(),
		Name:          span.Name(),
		Layer:         stringAttr(attrs, layerKey),
		Start:         instant(span.StartTime()),
		Status:        statusName(status.Code),
		StatusMessage: status.Description,
		Attributes:    attributesJSON(attrs),
		Events:        eventsJSON(span.Events()),
		Links:         linksJSON(span.Links()),
		start:         span.StartTime().UTC(),
	}
	if parent := span.Parent(); parent.IsValid() {
		id := parent.SpanID().String()
		out.ParentID = &id
	}
	if ended {
		end := instant(span.EndTime())
		out.End = &end
	}
	return out
}

// instant is how every time reaches the wire: RFC3339 with nanoseconds, in
// UTC.
func instant(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// statusName spells a span status the way the wire shape does.
func statusName(code codes.Code) string {
	switch code {
	case codes.Ok:
		return "ok"
	case codes.Error:
		return "error"
	default:
		return "unset"
	}
}

// stringAttr reads one string attribute, reporting "" when it is absent.
func stringAttr(attrs []attribute.KeyValue, key attribute.Key) string {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value.Emit()
		}
	}
	return ""
}

// attributesJSON turns a span's attributes into a JSON object whose values
// keep their own types: a count stays a number, a flag stays a boolean.
func attributesJSON(attrs []attribute.KeyValue) map[string]any {
	out := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value.AsInterface()
	}
	return out
}

// eventsJSON is the span's events — a retry, a rate-limit wait, a recorded
// error — in the order they happened.
func eventsJSON(events []sdktrace.Event) []eventJSON {
	out := make([]eventJSON, 0, len(events))
	for _, e := range events {
		out = append(out, eventJSON{
			Name:       e.Name,
			Time:       instant(e.Time),
			Attributes: attributesJSON(e.Attributes),
		})
	}
	return out
}

// linksJSON is what the span points at: for a Backfill's execution trace, the
// request span that asked for it.
func linksJSON(links []sdktrace.Link) []linkJSON {
	out := make([]linkJSON, 0, len(links))
	for _, l := range links {
		out = append(out, linkJSON{
			TraceID: l.SpanContext.TraceID().String(),
			SpanID:  l.SpanContext.SpanID().String(),
		})
	}
	return out
}
