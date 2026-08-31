package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every composite subcommand maps to exactly one route: same method, same
// path, and for the two that carry a declaration, exactly the body the flags
// spell.
func TestCompositeSubcommandsHitTheirRoute(t *testing.T) {
	const answer = `{"name":"btc-usd","state":"draft"}`
	tests := []struct {
		name   string
		args   []string
		method string
		path   string
		body   string
	}{
		{
			name: "create",
			args: []string{"composite", "create", "btc-usd",
				"-instrument", "BTC/USD", "-base", "binance:BTCUSDT",
				"-start", "2024-01-01", "-end", "now",
				"-timeframes", "5m,1h", "-mode", "research"},
			method: "POST",
			path:   "/composites",
			body: `{"name":"btc-usd","instrument":"BTC/USD",` +
				`"base":{"provider":"binance","symbol":"BTCUSDT","timeframe":"1m"},` +
				`"requested_start":"2024-01-01","requested_end":"now",` +
				`"timeframes":["5m","1h"],"mode":"research"}`,
		},
		{
			name: "create with a catch-up provider",
			args: []string{"composite", "create", "btc-usd",
				"-instrument", "BTC/USD", "-base", "binance:BTCUSDT",
				"-catch-up", "coinbase:BTC-USD",
				"-start", "2024-01-01", "-end", "2024-02-01"},
			method: "POST",
			path:   "/composites",
			body: `{"name":"btc-usd","instrument":"BTC/USD",` +
				`"base":{"provider":"binance","symbol":"BTCUSDT","timeframe":"1m"},` +
				`"catch_up":{"kind":"source","provider":"coinbase","symbol":"BTC-USD","timeframe":"1m"},` +
				`"requested_start":"2024-01-01","requested_end":"2024-02-01","timeframes":[]}`,
		},
		{
			name: "create with catch-up none",
			args: []string{"composite", "create", "btc-usd",
				"-instrument", "BTC/USD", "-base", "binance:BTCUSDT",
				"-catch-up", "none", "-start", "2024-01-01", "-end", "2024-02-01"},
			method: "POST",
			path:   "/composites",
			body: `{"name":"btc-usd","instrument":"BTC/USD",` +
				`"base":{"provider":"binance","symbol":"BTCUSDT","timeframe":"1m"},` +
				`"catch_up":{"kind":"none"},` +
				`"requested_start":"2024-01-01","requested_end":"2024-02-01","timeframes":[]}`,
		},
		{
			name:   "list",
			args:   []string{"composite", "list"},
			method: "GET",
			path:   "/composites",
		},
		{
			name:   "get",
			args:   []string{"composite", "get", "btc-usd"},
			method: "GET",
			path:   "/composites/btc-usd",
		},
		{
			name: "edit",
			args: []string{"composite", "edit", "btc-usd",
				"-instrument", "BTC/USD", "-base", "binance:BTCUSDT",
				"-start", "2024-01-01", "-end", "2024-02-01", "-timeframes", "1d"},
			method: "PUT",
			path:   "/composites/btc-usd",
			body: `{"instrument":"BTC/USD",` +
				`"base":{"provider":"binance","symbol":"BTCUSDT","timeframe":"1m"},` +
				`"requested_start":"2024-01-01","requested_end":"2024-02-01","timeframes":["1d"]}`,
		},
		{
			name:   "build",
			args:   []string{"composite", "build", "btc-usd"},
			method: "POST",
			path:   "/composites/btc-usd/build",
		},
		{
			name:   "quality",
			args:   []string{"composite", "quality", "btc-usd"},
			method: "GET",
			path:   "/composites/btc-usd/quality",
		},
		{
			name:   "delete",
			args:   []string{"composite", "delete", "btc-usd"},
			method: "DELETE",
			path:   "/composites/btc-usd",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.answer(http.StatusOK, answer, nil)
			code, stdout, stderr := f.run(tc.args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
			}
			got := f.only()
			if got.method != tc.method || got.path != tc.path {
				t.Errorf("hit %s %s, want %s %s", got.method, got.path, tc.method, tc.path)
			}
			if tc.body != "" && strings.TrimSpace(got.body) != tc.body {
				t.Errorf("request body = %s,\n           want %s", got.body, tc.body)
			}
			if stdout == "" {
				t.Errorf("printed nothing to stdout")
			}
		})
	}
}

// composite query is the backtester's command line: it hits the bars route
// with exactly what the flags named, writes the body where -o says, and prints
// what the dataset was when it answered — mirroring `data query`, which prints
// its X-Complete verdict the same way.
func TestCompositeQueryHitsTheBarsRouteAndWritesTheBody(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusOK, "PAR1-not-really", map[string]string{
		"X-Composite-State": "stale",
		"X-Composite-Mode":  "research",
		"X-Timeframe":       "1h",
		"X-Range-Start":     "2024-01-01T00:00:00Z",
		"X-Range-End":       "2024-02-01T00:00:00Z",
	})
	path := filepath.Join(t.TempDir(), "bars.parquet")
	code, stdout, stderr := f.run("composite", "query", "btc-usd",
		"-timeframe", "1h", "-start", "2024-01-01", "-end", "2024-02-01", "-o", path)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}

	got := f.only()
	if got.method != "GET" || got.path != "/composites/btc-usd/bars" {
		t.Fatalf("hit %s %s, want GET /composites/btc-usd/bars", got.method, got.path)
	}
	query, err := url.ParseQuery(got.query)
	if err != nil {
		t.Fatalf("parsing the query string %q: %v", got.query, err)
	}
	for name, want := range map[string]string{
		"timeframe": "1h", "start": "2024-01-01", "end": "2024-02-01",
	} {
		if query.Get(name) != want {
			t.Errorf("query %s = %q, want %q", name, query.Get(name), want)
		}
	}
	if query.Has("format") {
		t.Errorf("query carried format=%q, want Parquet by omission", query.Get("format"))
	}

	// What the dataset was is printed, so a stale or research dataset is never
	// invisible behind a binary body.
	for _, want := range []string{"state: stale", "mode: research", "timeframe: 1h",
		"range: 2024-01-01T00:00:00Z .. 2024-02-01T00:00:00Z", "wrote 15 bytes"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading what -o wrote: %v", err)
	}
	if string(body) != "PAR1-not-really" {
		t.Errorf("-o wrote %q, want the response body verbatim", body)
	}
}

// The two bounds and the timeframe are optional: omitting them asks for the
// dataset's own resolved range at its own 1-minute timeline, and the CLI sends
// no query parameter at all rather than an empty one.
func TestCompositeQuerySendsNothingItWasNotGiven(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusOK, "[]", nil)
	code, _, stderr := f.run("composite", "query", "btc-usd", "-format", "json", "-o", "-")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	got := f.only()
	if got.query != "format=json" {
		t.Errorf("query = %q, want only format=json", got.query)
	}
}

// -o is how a query says where the bars go, and there is no default: a stream
// that could be a year of Parquet is never dumped on stdout by accident.
func TestCompositeQueryNeedsAnOutput(t *testing.T) {
	f := newFake(t)
	code, _, _ := f.run("composite", "query", "btc-usd", "-timeframe", "1h")
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if len(f.requests) != 0 {
		t.Errorf("reached the service: %+v", f.requests)
	}
}

// A failure the service reported is the CLI's failure, with the service's own
// message: the client adds no judgement of its own.
func TestCompositeReportsTheServicesFailure(t *testing.T) {
	f := newFake(t)
	f.answer(http.StatusConflict, `{"error":"composite dataset already exists: \"btc-usd\""}`, nil)
	code, _, stderr := f.run("composite", "create", "btc-usd",
		"-instrument", "BTC/USD", "-base", "binance:BTCUSDT",
		"-start", "2024-01-01", "-end", "now")
	if code != exitFailure {
		t.Fatalf("exit = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr, "composite dataset already exists") {
		t.Errorf("stderr = %q, want the service's message", stderr)
	}
}

// A malformed -base or -catch-up is a usage error, decided before any request
// is made.
func TestCompositeRefusesAMalformedSourceFlagWithoutCallingTheService(t *testing.T) {
	for _, args := range [][]string{
		{"composite", "create", "btc-usd", "-instrument", "BTC/USD", "-base", "binance", "-start", "2024-01-01", "-end", "now"},
		{"composite", "create", "btc-usd", "-instrument", "BTC/USD", "-base", "binance:BTCUSDT", "-catch-up", "coinbase", "-start", "2024-01-01", "-end", "now"},
	} {
		f := newFake(t)
		code, _, _ := f.run(args...)
		if code != exitUsage {
			t.Errorf("%v: exit = %d, want %d", args, code, exitUsage)
		}
		if len(f.requests) != 0 {
			t.Errorf("%v: reached the service: %+v", args, f.requests)
		}
	}
}

func TestCompositeWithNoCommandIsAUsageError(t *testing.T) {
	f := newFake(t)
	if code, _, _ := f.run("composite"); code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
	if code, _, _ := f.run("composite", "frobnicate"); code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
}
