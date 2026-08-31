package app

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// This file is everything this package knows about tracing: the
// vendor-neutral OpenTelemetry API, its own scope and layer, and the two
// helpers a use case needs. A use case starts its own span, in the one place
// that knows what the operation is about (ADR 0003).

// scope names the instrumentation this package records under; layer is what
// every span it starts says it belongs to. Both are this context's own, so a
// composite span is never mistaken for an acquisition one.
const (
	scope = "composite/app"
	layer = "composite-app"
)

// The attributes a span of this context carries: composite domain words, and
// never a request, a response or a query string.
const (
	layerKey       = attribute.Key("agnoforge.layer")
	datasetNameKey = attribute.Key("agnoforge.composite.dataset")
	stateKey       = attribute.Key("agnoforge.composite.state")
	resolvedEndKey = attribute.Key("agnoforge.composite.resolved_end")
	gapCountKey    = attribute.Key("agnoforge.composite.open_gaps")
	rangeStartKey  = attribute.Key("agnoforge.composite.range_start")
	rangeEndKey    = attribute.Key("agnoforge.composite.range_end")
	// What the catch-up phase found and did: the parts of the requested range
	// the base source did not have, how many of them a backfill landed, and how
	// many Gaps a repair closed.
	missingCountKey  = attribute.Key("agnoforge.composite.missing_ranges")
	filledCountKey   = attribute.Key("agnoforge.composite.backfills")
	repairedCountKey = attribute.Key("agnoforge.composite.repairs")
	// What the assembly produced: how many Segments the timeline is made of,
	// and how many provider boundaries it crosses.
	segmentCountKey    = attribute.Key("agnoforge.composite.segments")
	transitionCountKey = attribute.Key("agnoforge.composite.transitions")
	// What materialization derived: how many higher-timeframe bars were written,
	// and how many of their windows the source data does not fully back.
	materializedBarsKey  = attribute.Key("agnoforge.composite.materialized_bars")
	incompleteWindowsKey = attribute.Key("agnoforge.composite.incomplete_windows")
	// What a bars query asked for and answered with: the frame, and how many
	// bars came back. The range it covers is the two range attributes above.
	timeframeKey = attribute.Key("agnoforge.composite.timeframe")
	barCountKey  = attribute.Key("agnoforge.composite.bars")
)

// tracer is resolved per span rather than cached, so the tracer provider the
// command installs is the one that records.
func tracer() trace.Tracer { return otel.Tracer(scope) }

// datasetAttrs is the layer and the Composite Dataset an operation is about.
func datasetAttrs(name domain.Name) []attribute.KeyValue {
	return []attribute.KeyValue{
		layerKey.String(layer),
		datasetNameKey.String(name.String()),
	}
}

// rangeAttrs spells a half-open range as two RFC3339 instants in UTC.
func rangeAttrs(r acq.Range) []attribute.KeyValue {
	return []attribute.KeyValue{
		rangeStartKey.String(instant(r.Start)),
		rangeEndKey.String(instant(r.End)),
	}
}

// phaseAttrs is what every phase span of a Build carries: the Composite
// Dataset being built and the range the phase is about. Both are on every
// phase, so a waterfall reads as one dataset over one range even where the
// spans of two builds interleave.
func phaseAttrs(name domain.Name, r acq.Range) []attribute.KeyValue {
	return append(datasetAttrs(name), rangeAttrs(r)...)
}

// phase starts one child span of a Build, named for the phase it is.
func phase(ctx context.Context, name string, attrs []attribute.KeyValue) (context.Context, trace.Span) {
	return tracer().Start(ctx, name, trace.WithAttributes(attrs...))
}

// queryAttrs is what a span of one resolved bars query carries: the dataset,
// the frame and the range — never a URL, a format or a byte count.
func queryAttrs(q BarQuery) []attribute.KeyValue {
	return append(datasetAttrs(q.Name()),
		timeframeKey.String(q.Timeframe.String()),
		rangeStartKey.String(instant(q.Range.Start)),
		rangeEndKey.String(instant(q.Range.End)))
}

// instant is how an instant reaches a span or a log line: RFC3339, in UTC.
func instant(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// fail records err on the span and marks it the operation's failure, then
// hands err back so a caller can return it in one line.
func fail(span trace.Span, err error) error {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
