package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
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
