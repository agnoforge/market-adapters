package binance

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// This file is everything this adapter knows about tracing: the
// vendor-neutral OpenTelemetry API and the attributes a span carries. A page
// fetch is one span whatever it costs — a retry and a rate-limit wait are
// events on it, never spans of their own, because they are things that
// happened to the fetch, not separate work (ADR 0003).

// scope names the instrumentation this package records under; layer is what
// every span it starts says it belongs to.
const (
	scope = "binance"
	layer = "provider"
)

// The attributes a span of this adapter carries. Nothing here is a request, a
// response, a header or a query string: a Symbol, a Timeframe, a range and a
// count of Bars is all a page fetch is allowed to say about itself.
const (
	layerKey        = attribute.Key("agnoforge.layer")
	providerKey     = attribute.Key("agnoforge.provider")
	symbolKey       = attribute.Key("agnoforge.symbol")
	timeframeKey    = attribute.Key("agnoforge.timeframe")
	rangeStartKey   = attribute.Key("agnoforge.range.start")
	rangeEndKey     = attribute.Key("agnoforge.range.end")
	pageBarCountKey = attribute.Key("agnoforge.page.bar_count")

	retryAttemptKey  = attribute.Key("agnoforge.retry.attempt")
	retryDelayKey    = attribute.Key("agnoforge.retry.delay_ms")
	rateLimitWaitKey = attribute.Key("agnoforge.rate_limit.wait_ms")
)

// tracer is resolved per span rather than cached in a package variable, so the
// tracer provider the command installs is the one that records.
func tracer() trace.Tracer { return otel.Tracer(scope) }

// providerAttrs is what every span of this adapter says about itself before it
// says anything else.
func providerAttrs() []attribute.KeyValue {
	return []attribute.KeyValue{layerKey.String(layer), providerKey.String(name)}
}

// fail records err on the span and marks the span the operation's failure,
// then hands err back so a caller can return it in one line.
func fail(span trace.Span, err error) error {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
