package app

import (
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// This file is everything this package knows about tracing: the vendor-neutral
// OpenTelemetry API, the names of the attributes a span carries, and two
// helpers. There is no decorator and no wrapper — a use case starts its own
// span, in the one place that knows what the operation is about (ADR 0003).

// scope names the instrumentation this package records under; layer is what
// every span it starts says it belongs to.
const (
	scope = "app"
	layer = "app"
)

// The attributes an agnoforge span carries. They are domain words — Provider,
// Symbol, Timeframe, a range, a Backfill id, counts — and never a request, a
// response, a header or a query string.
const (
	layerKey      = attribute.Key("agnoforge.layer")
	providerKey   = attribute.Key("agnoforge.provider")
	symbolKey     = attribute.Key("agnoforge.symbol")
	timeframeKey  = attribute.Key("agnoforge.timeframe")
	rangeStartKey = attribute.Key("agnoforge.range.start")
	rangeEndKey   = attribute.Key("agnoforge.range.end")
	backfillIDKey = attribute.Key("agnoforge.backfill.id")
	gapCountKey   = attribute.Key("agnoforge.gap.count")
)

// tracer is resolved per span rather than cached in a package variable, so a
// tracer provider installed after this package was linked — which is every
// provider, since the SDK is wired in cmd — is the one that records.
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

// rangeAttrs spells a half-open range as two RFC3339 instants in UTC.
func rangeAttrs(r domain.Range) []attribute.KeyValue {
	return []attribute.KeyValue{
		rangeStartKey.String(instant(r.Start)),
		rangeEndKey.String(instant(r.End)),
	}
}

// instant is how every bound reaches a span: RFC3339, in UTC.
func instant(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// fail records err on the span and marks the span the operation's failure,
// then hands err back so a caller can return it in one line. A nil error
// leaves the span alone.
func fail(span trace.Span, err error) error {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
