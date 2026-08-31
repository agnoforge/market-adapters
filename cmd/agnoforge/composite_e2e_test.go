package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEndToEndCompositeDefinitions runs the real wiring: one process, one
// DuckDB file, acquisition's routes and the composites resource on the same
// port, driven by the CLI exactly as an operator drives it. It proves the
// composite context's DDL is applied on startup against the file acquisition
// opened, and that a declaration survives create → get → list → edit → delete.
func TestEndToEndCompositeDefinitions(t *testing.T) {
	binance := newFakeBinance(t, 1)
	dbPath := filepath.Join(t.TempDir(), "agnoforge.duckdb")
	svc := startService(t, map[string]string{
		"AGNOFORGE_DB_PATH": dbPath,
		"BINANCE_BASE_URL":  binance.url(),
	})
	env := envFunc(map[string]string{"AGNOFORGE_URL": svc.baseURL})

	cli := func(args ...string) (int, string) {
		t.Helper()
		var stdout, stderr strings.Builder
		code := run(args, &stdout, &stderr, env)
		if code != 0 {
			t.Fatalf("%v: exit %d (stderr: %s)", args, code, stderr.String())
		}
		return code, stdout.String()
	}

	_, out := cli("composite", "create", "btc-usd",
		"-instrument", "BTC/USD", "-base", "binance:BTCUSDT",
		"-start", "2024-01-01", "-end", "now",
		"-timeframes", "5m,1h,1d", "-mode", "research")
	if !strings.Contains(out, `"state": "draft"`) {
		t.Fatalf("create printed %s, want a draft", out)
	}

	_, out = cli("composite", "get", "btc-usd")
	for _, want := range []string{`"name": "btc-usd"`, `"instrument": "BTC/USD"`,
		`"requested_end": "now"`, `"mode": "research"`, `"state": "draft"`} {
		if !strings.Contains(out, want) {
			t.Errorf("get printed %s, want it to contain %s", out, want)
		}
	}

	_, out = cli("composite", "list")
	if !strings.Contains(out, `"name": "btc-usd"`) {
		t.Fatalf("list printed %s, want the declared dataset", out)
	}

	_, out = cli("composite", "edit", "btc-usd",
		"-instrument", "BTC/USD", "-base", "binance:BTCUSDT",
		"-start", "2024-01-01", "-end", "2025-01-01", "-timeframes", "1w")
	if !strings.Contains(out, `"requested_end": "2025-01-01T00:00:00Z"`) ||
		!strings.Contains(out, `"mode": "strict"`) {
		t.Fatalf("edit printed %s, want the replaced declaration", out)
	}

	// The acquisition resource is still on the same port, untouched by any of
	// this: one service, two contexts.
	res, err := http.Get(svc.baseURL + "/providers")
	if err != nil {
		t.Fatalf("GET /providers: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("GET /providers = %d (%s)", res.StatusCode, body)
	}
	var providers []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&providers); err != nil {
		t.Fatalf("decoding /providers: %v", err)
	}
	if len(providers) != 1 || providers[0].Name != "binance" {
		t.Fatalf("providers = %+v, want the one binance provider", providers)
	}

	cli("composite", "delete", "btc-usd")
	_, out = cli("composite", "list")
	if strings.Contains(out, "btc-usd") {
		t.Fatalf("list after delete printed %s, want no datasets", out)
	}
}

// TestEndToEndCompositeBuild is the real wiring of a Build: acquisition really
// backfills two hours from the fake Binance, and the composite Build asks the
// production port adapter — the acquisition application service, in-process —
// what that left behind. Nothing is faked composite-side, and no bar crosses
// the port: the counts come from the bars table in the same file.
func TestEndToEndCompositeBuild(t *testing.T) {
	const bars = 120
	binance := newFakeBinance(t, bars)
	dbPath := filepath.Join(t.TempDir(), "agnoforge.duckdb")
	svc := startService(t, map[string]string{
		"AGNOFORGE_DB_PATH": dbPath,
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
		"-start", start, "-end", end)

	out := cli("composite", "build", "btc-usd")
	for _, want := range []string{
		`"state": "ready"`,
		`"kind": "base"`,
		`"resolved_end": "` + end + `"`,
		`"expected_bars": 120`,
		`"actual_bars": 120`,
		`"coverage_percentage": 100`,
		`"open_gap_count": 0`,
		`"strict": true`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("build printed %s,\nwant it to contain %s", out, want)
		}
	}

	// The provenance survives the build: Get answers the same segment list.
	out = cli("composite", "get", "btc-usd")
	if !strings.Contains(out, `"kind": "base"`) || !strings.Contains(out, `"provider": "binance"`) {
		t.Fatalf("get printed %s, want the base segment", out)
	}

	// And the consumer contract over the real wiring: the quality endpoint, and
	// a query that streams the composite timeline to a file the way a backtester
	// loads one.
	out = cli("composite", "quality", "btc-usd")
	for _, want := range []string{`"state": "ready"`, `"mode": "strict"`, `"actual_bars": 120`} {
		if !strings.Contains(out, want) {
			t.Errorf("quality printed %s,\nwant it to contain %s", out, want)
		}
	}

	parquet := filepath.Join(t.TempDir(), "bars.parquet")
	out = cli("composite", "query", "btc-usd", "-o", parquet)
	if !strings.Contains(out, "state: ready") || !strings.Contains(out, "timeframe: 1m") {
		t.Errorf("query printed %s, want the ready 1m answer", out)
	}
	written, err := os.Stat(parquet)
	if err != nil {
		t.Fatalf("the query wrote no file: %v", err)
	}
	if written.Size() == 0 {
		t.Fatal("the query wrote an empty parquet file")
	}

	// The same range as JSON is the same bars, spelled the way the acquisition
	// bars route spells one.
	out = cli("composite", "query", "btc-usd", "-format", "json", "-o", "-")
	var jsonBars []struct {
		OpenTime string `json:"open_time"`
		Open     string `json:"open"`
		Close    string `json:"close"`
	}
	body := out[strings.Index(out, "["):]
	if err := json.Unmarshal([]byte(body), &jsonBars); err != nil {
		t.Fatalf("decoding the queried bars %q: %v", body, err)
	}
	if len(jsonBars) != bars {
		t.Fatalf("queried %d bars, want the %d that were acquired", len(jsonBars), bars)
	}
	if jsonBars[0].OpenTime != start {
		t.Errorf("first bar opens at %s, want %s", jsonBars[0].OpenTime, start)
	}
	if strings.Contains(jsonBars[0].Open, "e") || !strings.Contains(jsonBars[0].Open, ".") {
		t.Errorf("open = %q, want a decimal string", jsonBars[0].Open)
	}
}
