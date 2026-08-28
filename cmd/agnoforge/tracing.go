package main

import (
	"net/http"
	"strings"

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

// playgroundPrefix is the path the developer playground answers under. It is
// deliberately not traced: a request that reads the trace store would
// otherwise write a trace into the store it is reading.
const playgroundPrefix = "/playground/"

// newTracerProvider builds the tracer provider the whole process records on,
// sinking every span into the given processors — in practice the playground's
// in-process trace store, which is the only sink there is (ADR 0003).
// The sampler is always-on: this is a local-first developer tool, and a trace
// that was dropped is a trace the playground cannot show.
func newTracerProvider(processors ...sdktrace.SpanProcessor) *sdktrace.TracerProvider {
	opts := []sdktrace.TracerProviderOption{sdktrace.WithSampler(sdktrace.AlwaysSample())}
	for _, p := range processors {
		opts = append(opts, sdktrace.WithSpanProcessor(p))
	}
	return sdktrace.NewTracerProvider(opts...)
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
		otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return !strings.HasPrefix(r.URL.Path, playgroundPrefix)
		}))
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
