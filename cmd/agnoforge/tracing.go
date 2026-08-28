package main

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// This file is the whole of the tracing wiring, and it is the only place in
// the program that may name the OpenTelemetry SDK: everything under internal/
// sees the vendor-neutral API and nothing else (ADR 0003).

// httpapiScope names the server span and the layer it belongs to: the HTTP
// adapter, the outermost of the four layers a trace crosses.
const httpapiScope = "httpapi"

// traceIDHeader is how a response tells its caller which trace answered it.
// The CLI reads it back on a failure, and the playground looks a trace up by
// it.
const traceIDHeader = "X-Trace-ID"

// layerKey marks which layer a span belongs to: httpapi, app, provider or
// store.
const layerKey = attribute.Key("agnoforge.layer")

// newTracerProvider builds the tracer provider the whole process records on.
// The sampler is always-on: this is a local-first developer tool, and a trace
// that was dropped is a trace the playground cannot show.
func newTracerProvider() *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
}

// installTracing makes tp the process-wide default and speaks W3C trace
// context, so a caller's traceparent is continued and the API packages can
// reach a tracer through otel.Tracer without being handed one.
func installTracing(tp *sdktrace.TracerProvider) {
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

// instrument wraps the API in its server span and its trace id header. The
// span comes from otelhttp — its name, its http.* attributes and its
// continuation of an inbound traceparent are that package's own defaults, not
// something this program spells again.
func instrument(handler http.Handler, tp trace.TracerProvider) http.Handler {
	return otelhttp.NewHandler(traceIDResponseHeader(handler), httpapiScope,
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithPropagators(propagation.TraceContext{}))
}

// traceIDResponseHeader writes X-Trace-ID on every response and marks the
// server span with its layer. Both happen before the handler runs, because a
// header set after the first byte is written is a header nobody receives.
func traceIDResponseHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.SpanFromContext(r.Context()).SetAttributes(layerKey.String(httpapiScope))
		if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
			w.Header().Set(traceIDHeader, sc.TraceID().String())
		}
		next.ServeHTTP(w, r)
	})
}
