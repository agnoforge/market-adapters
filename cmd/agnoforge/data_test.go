package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recorded is one request the fake service received, reduced to the three
// things a thin client can get wrong: the method, the route and the query.
type recorded struct {
	method string
	path   string
	query  string
	body   string
}

// fake is a stand-in for a running agnoforge service. It records every
// request and answers each one from the canned response the test set.
type fake struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	requests []recorded
	status   int
	body     string
	headers  map[string]string
}

// newFake starts a fake service answering 200 {} to everything.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{t: t, status: http.StatusOK, body: "{}", headers: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{
			method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(body),
		})
		status, canned, headers := f.status, f.body, f.headers
		f.mu.Unlock()

		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, canned)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// answer replaces the canned response.
func (f *fake) answer(status int, body string, headers map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body = status, body
	f.headers = map[string]string{}
	for k, v := range headers {
		f.headers[k] = v
	}
}

// env is the environment the CLI reads, pointing it at this fake.
func (f *fake) env() func(string) string {
	return envFunc(map[string]string{"AGNOFORGE_URL": f.srv.URL})
}

// run invokes the CLI in-process and returns its exit code and streams.
func (f *fake) run(args ...string) (int, string, string) {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, f.env())
	return code, stdout.String(), stderr.String()
}

// only returns the single request the fake received.
func (f *fake) only() recorded {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 1 {
		f.t.Fatalf("got %d requests, want exactly 1: %+v", len(f.requests), f.requests)
	}
	return f.requests[0]
}

// envFunc turns a map into the env lookup the CLI takes.
func envFunc(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// Every data subcommand maps to exactly one route: same method, same path,
// same query, nothing else.
func TestDataSubcommandsHitTheirRoute(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		answer string
		method string
		path   string
		query  string
		body   string
	}{
		{
			name:   "providers",
			args:   []string{"data", "providers"},
			answer: `[{"name":"binance","timeframes":["1m"]}]`,
			method: "GET",
			path:   "/providers",
		},
		{
			name:   "backfill",
			args:   []string{"data", "backfill", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01"},
			answer: `{"id":"abc","effective_range":{"start":"2024-01-01T00:00:00Z","end":"2024-02-01T00:00:00Z"}}`,
			method: "POST",
			path:   "/backfills",
			body:   `{"provider":"binance","symbol":"BTCUSDT","timeframe":"1m","start":"2024-01-01","end":"2024-02-01"}`,
		},
		{
			name:   "status",
			args:   []string{"data", "status", "abc"},
			answer: `{"id":"abc","state":"running"}`,
			method: "GET",
			path:   "/backfills/abc",
		},
		{
			name:   "cancel",
			args:   []string{"data", "cancel", "abc"},
			answer: `{"id":"abc","state":"cancelled"}`,
			method: "DELETE",
			path:   "/backfills/abc",
		},
		{
			name:   "gaps",
			args:   []string{"data", "gaps", "binance", "BTCUSDT", "1m"},
			answer: `[]`,
			method: "GET",
			path:   "/datasets/binance/BTCUSDT/1m/gaps",
		},
		{
			name:   "gaps with status",
			args:   []string{"data", "gaps", "binance", "BTCUSDT", "1m", "-status", "open"},
			answer: `[]`,
			method: "GET",
			path:   "/datasets/binance/BTCUSDT/1m/gaps",
			query:  "status=open",
		},
		{
			name:   "repair",
			args:   []string{"data", "repair", "7"},
			answer: `{"backfill_id":"abc"}`,
			method: "POST",
			path:   "/gaps/7/repair",
		},
		{
			name:   "complete",
			args:   []string{"data", "complete", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01"},
			answer: `{"complete":true,"gaps":[]}`,
			method: "GET",
			path:   "/datasets/binance/BTCUSDT/1m/complete",
			query:  "end=2024-02-01&start=2024-01-01",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.answer(http.StatusOK, tc.answer, nil)
			code, stdout, stderr := f.run(tc.args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
			}
			got := f.only()
			if got.method != tc.method || got.path != tc.path || got.query != tc.query {
				t.Errorf("hit %s %s?%s, want %s %s?%s", got.method, got.path, got.query, tc.method, tc.path, tc.query)
			}
			if tc.body != "" && strings.TrimSpace(got.body) != tc.body {
				t.Errorf("request body = %s, want %s", got.body, tc.body)
			}
			if stdout == "" {
				t.Errorf("printed nothing to stdout")
			}
		})
	}
}

// backfill reports the id and the effective range the service answered with.
func TestBackfillPrintsIDAndRange(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusAccepted, `{"id":"deadbeef","effective_range":{"start":"2024-01-01T00:00:00Z","end":"2024-02-01T00:00:00Z"}}`, nil)
	code, stdout, stderr := f.run("data", "backfill", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{"id: deadbeef", "effective range: 2024-01-01T00:00:00Z .. 2024-02-01T00:00:00Z"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

// complete answers a question, so it exits 0 either way and says so on the
// first line.
func TestCompletePrintsVerdictAndExitsZero(t *testing.T) {
	for _, verdict := range []bool{true, false} {
		f := newFake(t)
		body := `{"complete":false,"gaps":[]}`
		want := "complete: false\n"
		if verdict {
			body, want = `{"complete":true,"gaps":[]}`, "complete: true\n"
		}
		f.answer(http.StatusOK, body, nil)
		code, stdout, stderr := f.run("data", "complete", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01")
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
		}
		if !strings.HasPrefix(stdout, want) {
			t.Errorf("stdout = %q, want it to start with %q", stdout, want)
		}
	}
}

// query writes the body it was given to -o, byte for byte, and prints the
// X-Complete verdict.
func TestQueryWritesBodyAndPrintsVerdict(t *testing.T) {
	f := newFake(t)
	const payload = "PAR1\x00\x01parquet bytes"
	f.answer(http.StatusOK, payload, map[string]string{"X-Complete": "true", "X-Gaps": "[]"})

	out := filepath.Join(t.TempDir(), "bars.parquet")
	code, stdout, stderr := f.run("data", "query", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01", "-o", out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	got := f.only()
	if got.method != "GET" || got.path != "/datasets/binance/BTCUSDT/1m/bars" || got.query != "end=2024-02-01&start=2024-01-01" {
		t.Errorf("hit %s %s?%s", got.method, got.path, got.query)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}
	if string(written) != payload {
		t.Errorf("wrote %q, want %q", written, payload)
	}
	if !strings.HasPrefix(stdout, "complete: true\n") {
		t.Errorf("stdout = %q, want it to start with %q", stdout, "complete: true\n")
	}
}

// An incomplete range is flagged, not refused: the verdict is false, the gaps
// are printed, and the file is still written.
func TestQueryFlagsIncompleteRange(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusOK, "body", map[string]string{
		"X-Complete": "false",
		"X-Gaps":     `[{"id":1,"status":"open"}]`,
	})
	out := filepath.Join(t.TempDir(), "bars.parquet")
	code, stdout, _ := f.run("data", "query", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01", "-o", out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout, "complete: false\n") {
		t.Errorf("stdout = %q, want it to start with %q", stdout, "complete: false\n")
	}
	if !strings.Contains(stdout, `gaps: [{"id":1,"status":"open"}]`) {
		t.Errorf("stdout = %q, want it to name the gaps", stdout)
	}
}

// -o - streams the body to stdout instead of a file.
func TestQueryWritesToStdout(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusOK, "raw-bytes", map[string]string{"X-Complete": "true", "X-Gaps": "[]"})
	code, stdout, _ := f.run("data", "query", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01", "-o", "-")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if stdout != "complete: true\nraw-bytes" {
		t.Errorf("stdout = %q", stdout)
	}
}

// -format json is passed through as the query parameter the route takes.
func TestQueryPassesFormat(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusOK, "[]", map[string]string{"X-Complete": "true"})
	out := filepath.Join(t.TempDir(), "bars.json")
	if code, _, stderr := f.run("data", "query", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01", "-o", out, "-format", "json"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if q := f.only().query; q != "end=2024-02-01&format=json&start=2024-01-01" {
		t.Errorf("query = %q", q)
	}
}

// Any non-2xx is the service's message on stderr and a non-zero exit.
func TestNonSuccessStatusIsAnError(t *testing.T) {
	for _, sub := range [][]string{
		{"data", "providers"},
		{"data", "backfill", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01"},
		{"data", "status", "abc"},
		{"data", "cancel", "abc"},
		{"data", "gaps", "binance", "BTCUSDT", "1m"},
		{"data", "repair", "7"},
		{"data", "complete", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01"},
	} {
		f := newFake(t)
		f.answer(http.StatusBadRequest, `{"error":"boom"}`, nil)
		code, _, stderr := f.run(sub...)
		if code != 1 {
			t.Errorf("%v: exit = %d, want 1", sub, code)
		}
		if !strings.HasPrefix(stderr, "error: boom") {
			t.Errorf("%v: stderr = %q, want it to start with %q", sub, stderr, "error: boom")
		}
	}
}

// query fails the same way, and writes no file when it does.
func TestQueryNonSuccessStatusIsAnError(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusNotFound, `{"error":"boom"}`, nil)
	out := filepath.Join(t.TempDir(), "bars.parquet")
	code, _, stderr := f.run("data", "query", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01", "-o", out)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.HasPrefix(stderr, "error: boom") {
		t.Errorf("stderr = %q, want it to start with %q", stderr, "error: boom")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a failed query wrote %s", out)
	}
}

// A response that is not the {error} document still reports its status.
func TestNonSuccessWithoutErrorDocument(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusInternalServerError, "not json at all", nil)
	code, _, stderr := f.run("data", "providers")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.HasPrefix(stderr, "error: ") || !strings.Contains(stderr, "500") {
		t.Errorf("stderr = %q, want it to name the status", stderr)
	}
}

// A service that is not there is an error, not a panic.
func TestTransportFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"data", "providers"}, &stdout, &stderr, envFunc(map[string]string{"AGNOFORGE_URL": url}))
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.HasPrefix(stderr.String(), "error: ") {
		t.Errorf("stderr = %q, want it to start with %q", stderr.String(), "error: ")
	}
}

// Bad arguments are a usage error, which is exit 2 and never a request.
func TestBadArgumentsExitTwo(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"nonsense"},
		{"data"},
		{"data", "nonsense"},
		{"data", "backfill", "binance"},
		{"data", "backfill", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01", "extra"},
		{"data", "status"},
		{"data", "status", "a", "b"},
		{"data", "cancel"},
		{"data", "gaps", "binance"},
		{"data", "repair"},
		{"data", "complete", "binance", "BTCUSDT", "1m"},
		{"data", "query", "binance", "BTCUSDT", "1m", "2024-01-01", "2024-02-01"}, // -o is required
		{"data", "providers", "-nosuchflag"},
	} {
		f := newFake(t)
		code, _, stderr := f.run(args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if stderr == "" {
			t.Errorf("%v: printed no usage to stderr", args)
		}
		f.mu.Lock()
		n := len(f.requests)
		f.mu.Unlock()
		if n != 0 {
			t.Errorf("%v: made %d requests, want none", args, n)
		}
	}
}

// AGNOFORGE_URL defaults to the local service.
func TestClientDefaultURL(t *testing.T) {
	c := newClient(envFunc(nil))
	if c.base != "http://localhost:8080" {
		t.Errorf("base = %q, want %q", c.base, "http://localhost:8080")
	}
}
