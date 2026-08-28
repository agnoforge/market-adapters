package duckdb

import (
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/domain"
)

// This file is everything this adapter knows about tracing: the
// vendor-neutral OpenTelemetry API and the attributes a span carries. Each
// store operation opens its own span inline, so a trace shows which statement
// a request spent its time in (ADR 0003).

// scope names the instrumentation this package records under; layer is what
// every span it starts says it belongs to.
const (
	scope = "duckdb"
	layer = "store"
)

// The attributes a span of this adapter carries: the Dataset, the range, and
// counts. No statement, no row and no value ever reaches a span.
const (
	layerKey        = attribute.Key("agnoforge.layer")
	providerKey     = attribute.Key("agnoforge.provider")
	symbolKey       = attribute.Key("agnoforge.symbol")
	timeframeKey    = attribute.Key("agnoforge.timeframe")
	rangeStartKey   = attribute.Key("agnoforge.range.start")
	rangeEndKey     = attribute.Key("agnoforge.range.end")
	pageBarCountKey = attribute.Key("agnoforge.page.bar_count")
	gapCountKey     = attribute.Key("agnoforge.gap.count")
)

// tracer is resolved per span rather than cached in a package variable, so the
// tracer provider the command installs is the one that records.
func tracer() trace.Tracer { return otel.Tracer(scope) }

// datasetAttrs is the layer and the three components of a DatasetID.
func datasetAttrs(id domain.DatasetID) []attribute.KeyValue {
	return []attribute.KeyValue{
		layerKey.String(layer),
		providerKey.String(id.Provider),
		symbolKey.String(string(id.Symbol)),
		timeframeKey.String(string(id.Timeframe)),
	}
}

// datasetRangeAttrs is a Dataset and the half-open range an operation touches.
func datasetRangeAttrs(id domain.DatasetID, r domain.Range) []attribute.KeyValue {
	return append(datasetAttrs(id),
		rangeStartKey.String(instant(r.Start)),
		rangeEndKey.String(instant(r.End)))
}

// instant is how a range bound reaches a span: RFC3339, in UTC.
func instant(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// fail records err on the span and marks the span the operation's failure,
// then hands err back so a caller can return it in one line.
func fail(span trace.Span, err error) error {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
