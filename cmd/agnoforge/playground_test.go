package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// playgroundTrace is as much of the playground's wire shape as these tests
// need: one trace and the spans it holds.
type playgroundTrace struct {
	TraceID string           `json:"trace_id"`
	Spans   []playgroundSpan `json:"spans"`
}

// playgroundSpan is one span as the playground serves it: what it is, where it
// sits in the tree, whether it has finished, and what it recorded.
type playgroundSpan struct {
	SpanID        string         `json:"span_id"`
	ParentID      *string        `json:"parent_id"`
	Name          string         `json:"name"`
	Layer         string         `json:"layer"`
	End           *string        `json:"end"`
	Status        string         `json:"status"`
	StatusMessage string         `json:"status_message"`
	Attributes    map[string]any `json:"attributes"`
}

// The real service serves the playground on its own port: a request's
// X-Trace-ID is a trace the store can be asked for, and the trace holds the
// server span otelhttp named, marked as the httpapi layer.
func TestPlaygroundServesTheTraceOfARealRequest(t *testing.T) {
	fake := newFakeBinance(t, 1)
	svc := startService(t, map[string]string{
		"AGNOFORGE_DB_PATH": filepath.Join(t.TempDir(), "agnoforge.duckdb"),
		"BINANCE_BASE_URL":  fake.url(),
	})

	res, err := http.Get(svc.baseURL + "/providers")
	if err != nil {
		t.Fatalf("GET /providers: %v", err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	traceID := res.Header.Get("X-Trace-ID")
	if !traceIDPattern.MatchString(traceID) {
		t.Fatalf("X-Trace-ID = %q, want 32 hex digits", traceID)
	}

	// The server span is ended after the response is written, so the layer it
	// gained inside the middleware lands a moment later. The trace itself is
	// there from the instant it started.
	var trace playgroundTrace
	deadline := time.Now().Add(5 * time.Second)
	for {
		trace = getPlaygroundTrace(t, svc.baseURL, traceID)
		if hasHTTPAPISpan(trace) || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if trace.TraceID != traceID {
		t.Errorf("trace_id = %q, want %q", trace.TraceID, traceID)
	}
	if !hasHTTPAPISpan(trace) {
		t.Errorf("trace %s holds no httpapi span: %+v", traceID, trace.Spans)
	}

	// The playground is not itself traced, so reading a trace does not write
	// one — and a playground response carries no X-Trace-ID.
	res, err = http.Get(svc.baseURL + "/playground/traces/" + traceID)
	if err != nil {
		t.Fatalf("GET /playground/traces: %v", err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := res.Header.Get("X-Trace-ID"); got != "" {
		t.Errorf("playground X-Trace-ID = %q, want none: /playground/ is not traced", got)
	}

	// An id nobody recorded is a 404 from the playground, not the API's.
	res, err = http.Get(svc.baseURL + "/playground/traces/0af7651916cd43dd8448eb211c80319c")
	if err != nil {
		t.Fatalf("GET /playground/traces: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown trace = %d, want 404", res.StatusCode)
	}
	if string(body) != `{"error":"not found"}` {
		t.Errorf("body = %s, want the not-found error", body)
	}

	// The domain routes are untouched by the outer mux.
	res, err = http.Get(svc.baseURL + "/providers")
	if err != nil {
		t.Fatalf("GET /providers: %v", err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("GET /providers = %d, want 200", res.StatusCode)
	}
}

// getPlaygroundTrace asks the running service for one trace.
func getPlaygroundTrace(t *testing.T, baseURL, traceID string) playgroundTrace {
	t.Helper()
	res, err := http.Get(baseURL + "/playground/traces/" + traceID)
	if err != nil {
		t.Fatalf("GET /playground/traces/%s: %v", traceID, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /playground/traces/%s = %d: %s", traceID, res.StatusCode, body)
	}
	var out playgroundTrace
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

// hasHTTPAPISpan reports whether the trace holds the server span otelhttp
// recorded, which is the one marked as the httpapi layer.
func hasHTTPAPISpan(trace playgroundTrace) bool {
	for _, span := range trace.Spans {
		if span.Layer == "httpapi" && span.Name != "" {
			return true
		}
	}
	return false
}
