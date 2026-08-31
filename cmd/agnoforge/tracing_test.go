package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// traceIDPattern is the wire spelling of a trace id: 32 lowercase hex digits.
var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// recordingProvider builds an always-on TracerProvider that keeps every
// finished span in memory, so a test can compare a response header against the
// span the server actually recorded.
func recordingProvider(t *testing.T) (*sdktrace.TracerProvider, *tracetest.InMemoryExporter) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(exporter),
	)
	t.Cleanup(func() { tp.Shutdown(t.Context()) })
	return tp, exporter
}

// Every response the instrumented handler writes — whatever its status —
// carries X-Trace-ID, and it is the trace id of the server span.
func TestEveryResponseCarriesTheServerSpanTraceID(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError} {
		tp, exporter := recordingProvider(t)
		handler := instrument(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}), tp)
		srv := httptest.NewServer(handler)
		defer srv.Close()

		res, err := http.Get(srv.URL + "/providers")
		if err != nil {
			t.Fatalf("GET /providers: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("status = %d, want %d", res.StatusCode, status)
		}

		header := res.Header.Get("X-Trace-ID")
		if !traceIDPattern.MatchString(header) {
			t.Errorf("X-Trace-ID = %q, want 32 hex digits", header)
		}
		spans := exporter.GetSpans()
		if len(spans) != 1 {
			t.Fatalf("recorded %d spans, want exactly the server span", len(spans))
		}
		if got := spans[0].SpanContext.TraceID().String(); got != header {
			t.Errorf("X-Trace-ID = %q, server span trace id = %q", header, got)
		}
		// The name and the http.* attributes are otelhttp's own semconv
		// spelling; this program only asserts that the span is the server
		// side of the request.
		if spans[0].SpanKind != trace.SpanKindServer {
			t.Errorf("server span kind = %v, want %v", spans[0].SpanKind, trace.SpanKindServer)
		}
	}
}

// The server span says which layer it belongs to, and never carries a header
// or a body.
func TestServerSpanCarriesTheLayerAttribute(t *testing.T) {
	tp, exporter := recordingProvider(t)
	srv := httptest.NewServer(instrument(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}"))
	}), tp))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/providers")
	if err != nil {
		t.Fatalf("GET /providers: %v", err)
	}
	res.Body.Close()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want exactly the server span", len(spans))
	}
	found := ""
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "agnoforge.layer" {
			found = attr.Value.AsString()
		}
	}
	if found != "httpapi" {
		t.Errorf("agnoforge.layer = %q, want %q", found, "httpapi")
	}
}

// A caller that already has a trace continues it: the response names the
// inbound trace id, not a new one.
func TestInboundTraceparentIsContinued(t *testing.T) {
	const (
		inboundTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
		inboundSpan  = "00f067aa0ba902b7"
	)
	tp, exporter := recordingProvider(t)
	srv := httptest.NewServer(instrument(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}"))
	}), tp))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/providers", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("traceparent", "00-"+inboundTrace+"-"+inboundSpan+"-01")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /providers: %v", err)
	}
	res.Body.Close()

	if got := res.Header.Get("X-Trace-ID"); got != inboundTrace {
		t.Errorf("X-Trace-ID = %q, want the inbound trace id %q", got, inboundTrace)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want exactly the server span", len(spans))
	}
	if got := spans[0].Parent.SpanID().String(); got != inboundSpan {
		t.Errorf("server span parent = %q, want the inbound span %q", got, inboundSpan)
	}
}

// The running service answers with X-Trace-ID too: the wiring, not just the
// middleware, is instrumented.
func TestServeAnswersWithATraceID(t *testing.T) {
	fake := newFakeBinance(t, 1)
	svc := startService(t, map[string]string{
		"AGNOFORGE_DB_PATH": filepath.Join(t.TempDir(), "agnoforge.duckdb"),
		"BINANCE_BASE_URL":  fake.url(),
	})

	for _, path := range []string{"/providers", "/nosuchroute"} {
		res, err := http.Get(svc.baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if got := res.Header.Get("X-Trace-ID"); !traceIDPattern.MatchString(got) {
			t.Errorf("GET %s: X-Trace-ID = %q, want 32 hex digits", path, got)
		}
	}
}

// A non-2xx names the trace it happened in, after the error line, so the
// failure can be looked up.
func TestNonSuccessPrintsTheTraceID(t *testing.T) {
	const traceID = "0af7651916cd43dd8448eb211c80319c"
	f := newFake(t)
	f.answer(http.StatusBadRequest, `{"error":"boom"}`, map[string]string{"X-Trace-ID": traceID})

	code, _, stderr := f.run("data", "providers")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if want := "error: boom\ntrace: " + traceID + "\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// query reports the trace of a failure the same way.
func TestQueryNonSuccessPrintsTheTraceID(t *testing.T) {
	const traceID = "0af7651916cd43dd8448eb211c80319c"
	f := newFake(t)
	f.answer(http.StatusNotFound, `{"error":"boom"}`, map[string]string{"X-Trace-ID": traceID})

	out := filepath.Join(t.TempDir(), "bars.parquet")
	code, _, stderr := f.run("data", "query", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01", "-o", out)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if want := "error: boom\ntrace: " + traceID + "\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// A service that does not trace is still usable: no header, no extra line.
func TestNonSuccessWithoutATraceIDPrintsNothingExtra(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusBadRequest, `{"error":"boom"}`, nil)

	code, _, stderr := f.run("data", "providers")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if stderr != "error: boom\n" {
		t.Errorf("stderr = %q, want just the error line", stderr)
	}
}

// A failure that is not a response — no service there at all — has no trace to
// name.
func TestTransportFailurePrintsNoTraceID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"data", "providers"}, &stdout, &stderr,
		envFunc(map[string]string{"AGNOFORGE_URL": url})); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), "trace:") {
		t.Errorf("stderr = %q, want no trace line", stderr.String())
	}
}

// The tracing SDK is wiring, so it lives only in the command: no package under
// internal/ may reach it, and the domain reaches no OpenTelemetry package at
// all.
//
// internal/adapters/playground is the one exemption, and it is the exemption
// ADR 0003 buys: the in-process trace store *is* a sdktrace.SpanProcessor, so
// being the SDK's own seam is the whole of what that package is. It still may
// not reach contrib/, which is otelhttp and belongs here.
func TestOnlyTheCommandDependsOnTheTracingSDK(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Deps " "}}`,
		"github.com/agnos/agnoforge/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	const (
		internal   = "github.com/agnos/agnoforge/internal/"
		domain     = "github.com/agnos/agnoforge/internal/acquisition/domain"
		playground = "github.com/agnos/agnoforge/internal/acquisition/adapters/playground"
	)
	seen, exempted := 0, false
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], internal) {
			continue
		}
		seen++
		if fields[0] == playground {
			exempted = true
			for _, dep := range fields[1:] {
				if strings.HasPrefix(dep, "go.opentelemetry.io/contrib/") {
					t.Errorf("%s depends on %s, which belongs to cmd", fields[0], dep)
				}
			}
			continue
		}
		for _, dep := range fields[1:] {
			if strings.HasPrefix(dep, "go.opentelemetry.io/otel/sdk") ||
				strings.HasPrefix(dep, "go.opentelemetry.io/contrib/") {
				t.Errorf("%s depends on %s, which belongs to cmd", fields[0], dep)
			}
			if fields[0] == domain && strings.HasPrefix(dep, "go.opentelemetry.io/") {
				t.Errorf("%s depends on %s: the domain is stdlib only", fields[0], dep)
			}
		}
	}
	if seen == 0 {
		t.Fatal("go list reported no internal packages")
	}
	if !exempted {
		t.Fatalf("go list never reported %s: the exemption is guarding nothing", playground)
	}
}
