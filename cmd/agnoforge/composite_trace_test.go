package main

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file is the composite context's trace test, and it is here rather than
// under internal/composite for the reason ADR 0003 gives: the tracing SDK is
// wiring, so the process that installs it — this command — is where a test can
// watch what the layers below recorded. It reads the spans back through the
// playground, the same way an operator does, so what it asserts is exactly what
// the trace UI draws.

// The six phases a Build is made of, in the order they happen. Each is a child
// span of composite.Build, so a slow or failed phase is the one the waterfall
// points at.
var buildPhases = []string{
	"composite.resolve",
	"composite.ensure",
	"composite.assemble",
	"composite.validate",
	"composite.quality",
	"composite.materialize",
}

// A Build over the real wiring records one span per phase, each a child of
// composite.Build, which is itself a child of the server span — and every one
// of them says which dataset and which range it is about.
func TestCompositeBuildTraceShowsItsPhases(t *testing.T) {
	const bars = 60
	binance := newFakeBinance(t, bars)
	svc := startService(t, map[string]string{
		"AGNOFORGE_DB_PATH": filepath.Join(t.TempDir(), "agnoforge.duckdb"),
		"BINANCE_BASE_URL":  binance.url(),
	})
	env := envFunc(map[string]string{"AGNOFORGE_URL": svc.baseURL})
	cli := func(args ...string) string {
		t.Helper()
		var stdout, stderr strings.Builder
		if code := run(args, &stdout, &stderr, env); code != 0 {
			t.Fatalf("%v: exit %d (stderr: %s)", args, code, stderr.String())
		}
		return stdout.String()
	}

	start := fixtureOrigin.Format(time.RFC3339)
	end := fixtureOrigin.Add(bars * time.Minute).Format(time.RFC3339)
	cli("data", "backfill", "binance", "BTCUSDT", "1m", start, end, "-wait", "-interval", "20ms")
	cli("composite", "create", "btc-usd",
		"-instrument", "BTC/USD", "-base", "binance:BTCUSDT",
		"-start", start, "-end", end, "-timeframes", "1h")

	// Every composite route carries the trace id header the rest of the service
	// does, and the id names a trace the playground really holds — even for the
	// one operation that is about no single dataset.
	res, err := http.Get(svc.baseURL + "/composites")
	if err != nil {
		t.Fatalf("GET the composites: %v", err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	listID := res.Header.Get("X-Trace-ID")
	if !traceIDPattern.MatchString(listID) {
		t.Fatalf("GET /composites X-Trace-ID = %q, want 32 hex digits", listID)
	}
	if got := spanNamed(t, traceOf(t, svc.baseURL, listID, "composite.Datasets"),
		"composite.Datasets").Layer; got != "composite-app" {
		t.Errorf("composite.Datasets layer = %q, want %q", got, "composite-app")
	}

	traceID := buildOverHTTP(t, svc.baseURL, "btc-usd", http.StatusOK)
	trace := traceOf(t, svc.baseURL, traceID, "composite.Build")

	// The trace crosses the layers the same way an acquisition trace does: the
	// server span otelhttp recorded, then the composite use case under it.
	server := spanWithLayer(t, trace, "httpapi")
	build := spanNamed(t, trace, "composite.Build")
	if build.ParentID == nil || *build.ParentID != server.SpanID {
		t.Fatalf("composite.Build parent = %v, want the server span %s", build.ParentID, server.SpanID)
	}
	if build.Layer != "composite-app" {
		t.Errorf("composite.Build layer = %q, want %q", build.Layer, "composite-app")
	}
	wantAttribute(t, build, "agnoforge.composite.dataset", "btc-usd")

	for _, phase := range buildPhases {
		span := spanNamed(t, trace, phase)
		if span.ParentID == nil || *span.ParentID != build.SpanID {
			t.Errorf("%s parent = %v, want composite.Build %s", phase, span.ParentID, build.SpanID)
		}
		if span.Layer != "composite-app" {
			t.Errorf("%s layer = %q, want %q", phase, span.Layer, "composite-app")
		}
		wantAttribute(t, span, "agnoforge.composite.dataset", "btc-usd")
		wantAttribute(t, span, "agnoforge.composite.range_start", start)
		wantAttribute(t, span, "agnoforge.composite.range_end", end)
		if span.Status == "error" {
			t.Errorf("%s is marked failed: %s", phase, span.StatusMessage)
		}
	}

	// And nothing this context records is unlabelled: every composite span says
	// it is a composite one, which is what keeps it its own colour in the trace
	// UI and never mistaken for an acquisition span.
	for _, span := range trace.Spans {
		if strings.HasPrefix(span.Name, "composite.") && span.Layer != "composite-app" {
			t.Errorf("span %q: layer = %q, want %q", span.Name, span.Layer, "composite-app")
		}
	}
}

// A Build that cannot run fails the phase it broke in, not just the Build: the
// base provider does not exist, so the catch-up phase that asks acquisition for
// it is the span carrying the error, and no phase after it ran at all.
func TestCompositeBuildFailureNamesTheFailingPhase(t *testing.T) {
	binance := newFakeBinance(t, 1)
	svc := startService(t, map[string]string{
		"AGNOFORGE_DB_PATH": filepath.Join(t.TempDir(), "agnoforge.duckdb"),
		"BINANCE_BASE_URL":  binance.url(),
	})
	env := envFunc(map[string]string{"AGNOFORGE_URL": svc.baseURL})

	var stdout, stderr strings.Builder
	if code := run([]string{"composite", "create", "no-such-provider",
		"-instrument", "BTC/USD", "-base", "kraken:BTCUSD",
		"-start", fixtureOrigin.Format(time.RFC3339),
		"-end", fixtureOrigin.Add(time.Hour).Format(time.RFC3339)},
		&stdout, &stderr, env); code != 0 {
		t.Fatalf("create: exit %d (stderr: %s)", code, stderr.String())
	}

	traceID := buildOverHTTP(t, svc.baseURL, "no-such-provider", http.StatusInternalServerError)
	trace := traceOf(t, svc.baseURL, traceID, "composite.Build")

	ensure := spanNamed(t, trace, "composite.ensure")
	if ensure.Status != "error" {
		t.Errorf("composite.ensure status = %q, want %q", ensure.Status, "error")
	}
	if !strings.Contains(ensure.StatusMessage, "unknown provider") {
		t.Errorf("composite.ensure message = %q, want the acquisition failure", ensure.StatusMessage)
	}
	if build := spanNamed(t, trace, "composite.Build"); build.Status != "error" {
		t.Errorf("composite.Build status = %q, want %q", build.Status, "error")
	}
	for _, phase := range []string{"composite.assemble", "composite.validate",
		"composite.quality", "composite.materialize"} {
		if findSpan(trace, phase) != nil {
			t.Errorf("%s ran, but the build failed before it", phase)
		}
	}
}

// buildOverHTTP asks the running service to build a dataset and hands back the
// trace id the response carries — the header every domain route answers with.
func buildOverHTTP(t *testing.T, baseURL, name string, want int) string {
	t.Helper()
	res, err := http.Post(baseURL+"/composites/"+name+"/build", contentTypeJSON, nil)
	if err != nil {
		t.Fatalf("POST build: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("POST build = %d, want %d (%s)", res.StatusCode, want, body)
	}
	traceID := res.Header.Get("X-Trace-ID")
	if !traceIDPattern.MatchString(traceID) {
		t.Fatalf("X-Trace-ID = %q, want 32 hex digits", traceID)
	}
	return traceID
}

// contentTypeJSON is what the build request is sent as.
const contentTypeJSON = "application/json"

// traceOf reads one trace back from the playground, waiting until the named
// span has finished — a span is recorded when it starts, so a trace can be
// served while it is still being written.
func traceOf(t *testing.T, baseURL, traceID, until string) playgroundTrace {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		trace := getPlaygroundTrace(t, baseURL, traceID)
		if span := findSpan(trace, until); span != nil && span.End != nil {
			return trace
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace %s never finished a %q span: %+v", traceID, until, trace.Spans)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// findSpan returns the span with that name, or nil when the trace holds none.
func findSpan(trace playgroundTrace, name string) *playgroundSpan {
	for i, span := range trace.Spans {
		if span.Name == name {
			return &trace.Spans[i]
		}
	}
	return nil
}

// spanNamed insists the trace holds exactly one span under that name.
func spanNamed(t *testing.T, trace playgroundTrace, name string) playgroundSpan {
	t.Helper()
	var found []playgroundSpan
	for _, span := range trace.Spans {
		if span.Name == name {
			found = append(found, span)
		}
	}
	if len(found) != 1 {
		t.Fatalf("trace holds %d spans named %q, want 1 (holds %v)", len(found), name, spanNames(trace))
	}
	return found[0]
}

// spanWithLayer insists the trace holds exactly one span of that layer.
func spanWithLayer(t *testing.T, trace playgroundTrace, layer string) playgroundSpan {
	t.Helper()
	var found []playgroundSpan
	for _, span := range trace.Spans {
		if span.Layer == layer {
			found = append(found, span)
		}
	}
	if len(found) != 1 {
		t.Fatalf("trace holds %d spans of layer %q, want 1 (holds %v)", len(found), layer, spanNames(trace))
	}
	return found[0]
}

// wantAttribute insists a span carries key with that string value.
func wantAttribute(t *testing.T, span playgroundSpan, key, want string) {
	t.Helper()
	got, ok := span.Attributes[key]
	if !ok {
		t.Errorf("span %q has no %s attribute (has %v)", span.Name, key, span.Attributes)
		return
	}
	if got != want {
		t.Errorf("span %q: %s = %v, want %q", span.Name, key, got, want)
	}
}

// spanNames lists what a trace holds, for a failure message.
func spanNames(trace playgroundTrace) []string {
	out := make([]string, 0, len(trace.Spans))
	for _, span := range trace.Spans {
		out = append(out, span.Name)
	}
	return out
}
