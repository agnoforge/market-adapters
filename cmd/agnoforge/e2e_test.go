package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixtureOrigin is the open_time of the first Bar the fake Binance serves. It
// is long past, so the adapter's clipping of the end down to the last fully
// closed Bar never touches the fixture and the real clock is fine.
var fixtureOrigin = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// fixtureBars is how many 1m Bars the fake Binance has — three pages of the
// adapter's 1000-row limit, plus a short last one.
const fixtureBars = 2500

// fakeBinance is a stand-in for the Provider's REST endpoint: it serves
// GET /api/v3/klines from a generated fixture, paging by startTime and limit
// the way the real one does, and records every path it was asked for.
type fakeBinance struct {
	t     *testing.T
	srv   *httptest.Server
	count int

	mu    sync.Mutex
	paths []string
}

// newFakeBinance starts a fake Binance holding count consecutive 1m Bars from
// fixtureOrigin.
func newFakeBinance(t *testing.T, count int) *fakeBinance {
	t.Helper()
	f := &fakeBinance{t: t, count: count}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()

		if r.URL.Path != "/api/v3/klines" {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		body, err := json.Marshal(f.page(
			queryInt(t, query, "startTime", 0),
			queryInt(t, query, "endTime", math.MaxInt64),
			int(queryInt(t, query, "limit", 500)),
		))
		if err != nil {
			t.Errorf("encoding klines: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBinance) url() string { return f.srv.URL }

// page renders the rows of [startTime, endTime] the endpoint would return,
// capped at limit. endTime is inclusive on the wire, as it is at Binance.
func (f *fakeBinance) page(startTime, endTime int64, limit int) [][]any {
	step := time.Minute
	rows := make([][]any, 0, limit)
	for i := range f.count {
		open := fixtureOrigin.Add(time.Duration(i) * step).UnixMilli()
		if open < startTime || open > endTime {
			continue
		}
		rows = append(rows, []any{
			open, "100.00000000", "101.00000000", "99.00000000", "100.50000000", "1.00000000",
			open + step.Milliseconds() - 1,
			"0.00000000", 0, "0.00000000", "0.00000000", "0",
		})
		if len(rows) == limit {
			break
		}
	}
	return rows
}

// assertKlinesOnly fails the test unless the fake was actually used, and used
// only for klines — which is also the proof that no request left for the real
// Binance.
func (f *fakeBinance) assertKlinesOnly() {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.paths) == 0 {
		f.t.Error("the fake Binance was never called")
	}
	for _, path := range f.paths {
		if path != "/api/v3/klines" {
			f.t.Errorf("request went to %q, want %q", path, "/api/v3/klines")
		}
	}
}

// requests reports how many requests the fake has answered.
func (f *fakeBinance) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.paths)
}

// queryInt reads an integer query parameter, falling back to def.
func queryInt(t *testing.T, query map[string][]string, key string, def int64) int64 {
	t.Helper()
	values := query[key]
	if len(values) == 0 || values[0] == "" {
		return def
	}
	n, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		t.Fatalf("query %s=%q is not a number: %v", key, values[0], err)
	}
	return n
}

// The acceptance path, end to end and in one process: a real service over a
// real DuckDB file and a fake Binance, driven only through the CLI.
func TestEndToEndBackfillCompleteQuery(t *testing.T) {
	fake := newFakeBinance(t, fixtureBars)
	dir := t.TempDir()
	env := map[string]string{
		"AGNOFORGE_DB_PATH": filepath.Join(dir, "agnoforge.duckdb"),
		"BINANCE_BASE_URL":  fake.url(),
	}
	svc := startService(t, env)
	env["AGNOFORGE_URL"] = svc.baseURL

	start := fixtureOrigin.Format(time.RFC3339)
	end := fixtureOrigin.Add(fixtureBars * time.Minute).Format(time.RFC3339)

	invoke := func(args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr, envFunc(env))
		return code, stdout.String(), stderr.String()
	}
	backfill := func() {
		t.Helper()
		code, stdout, stderr := invoke("data", "backfill", "binance", "BTCUSDT", "1m", start, end, "-wait", "-interval", "20ms")
		if code != 0 {
			t.Fatalf("backfill exited %d: %s%s", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "state: completed") {
			t.Fatalf("backfill stdout = %q, want a completed state", stdout)
		}
		if want := "bars downloaded: " + strconv.Itoa(fixtureBars); !strings.Contains(stdout, want) {
			t.Errorf("backfill stdout = %q, want %q", stdout, want)
		}
	}
	assertComplete := func(when string) {
		t.Helper()
		code, stdout, stderr := invoke("data", "complete", "binance", "BTCUSDT", "1m", start, end)
		if code != 0 {
			t.Fatalf("%s: complete exited %d: %s%s", when, code, stdout, stderr)
		}
		if !strings.HasPrefix(stdout, "complete: true\n") {
			t.Fatalf("%s: complete stdout = %q, want it to start with %q", when, stdout, "complete: true\n")
		}
	}

	backfill()
	assertComplete("after the first backfill")

	// The Parquet the query writes is a file DuckDB reads back, with one row
	// per Bar of the range.
	out := filepath.Join(dir, "bars.parquet")
	code, stdout, stderr := invoke("data", "query", "binance", "BTCUSDT", "1m", start, end, "-o", out)
	if code != 0 {
		t.Fatalf("query exited %d: %s%s", code, stdout, stderr)
	}
	if !strings.HasPrefix(stdout, "complete: true\n") {
		t.Errorf("query stdout = %q, want it to start with %q", stdout, "complete: true\n")
	}
	if rows := parquetRows(t, out); rows != fixtureBars {
		t.Errorf("the exported Parquet holds %d rows, want %d", rows, fixtureBars)
	}

	// Rerunning the Backfill changes nothing: same Bars, still complete.
	before := fake.requests()
	backfill()
	assertComplete("after the second backfill")
	if fake.requests() <= before {
		t.Error("the second backfill asked the Provider for nothing")
	}
	if rows := parquetRows(t, out); rows != fixtureBars {
		t.Errorf("after the rerun the export holds %d rows, want %d", rows, fixtureBars)
	}

	fake.assertKlinesOnly()
}

// parquetRows counts the rows of a Parquet file with a DuckDB of its own,
// which is what proves the file is one DuckDB can read.
func parquetRows(t *testing.T, path string) int64 {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()
	var rows int64
	if err := db.QueryRow(`SELECT count(*) FROM read_parquet(?)`, path).Scan(&rows); err != nil {
		t.Fatalf("read_parquet(%q): %v", path, err)
	}
	return rows
}
