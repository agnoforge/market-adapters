package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2" // the second DuckDB a test reads an export back with
)

func TestShowCoverage(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)

	var got []struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	h.decode(h.do("GET", "/datasets/fake/BTCUSDT/1m/coverage", nil), http.StatusOK, &got)
	if len(got) != 1 || got[0].Start != at(0) || got[0].End != at(10) {
		t.Fatalf("coverage = %+v, want the single range [%s,%s)", got, at(0), at(10))
	}
}

// A Dataset nothing was ever acquired for has an empty Coverage, which is an
// empty array and never null.
func TestShowCoverageEmpty(t *testing.T) {
	h := newHarness(t)
	res := h.do("GET", "/datasets/fake/ETHUSDT/1m/coverage", nil)
	if body := string(h.body(res)); body != "[]\n" {
		t.Errorf("coverage body = %q, want an empty array", body)
	}
}

func TestShowCoverageRejectsBadTimeframe(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("GET", "/datasets/fake/BTCUSDT/2w/coverage", nil), http.StatusBadRequest)
}

func TestShowCompleteness(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)

	var holed struct {
		Complete bool      `json:"complete"`
		Gaps     []gapJSON `json:"gaps"`
	}
	h.decode(h.do("GET", "/datasets/fake/BTCUSDT/1m/complete?start="+at(0)+"&end="+at(10), nil), http.StatusOK, &holed)
	if holed.Complete {
		t.Error("complete = true over the range holding the Gap, want false")
	}
	if len(holed.Gaps) != 1 || holed.Gaps[0].Range.Start != at(4) {
		t.Errorf("gaps = %+v, want the one Gap at minute 4", holed.Gaps)
	}

	var clean struct {
		Complete bool      `json:"complete"`
		Gaps     []gapJSON `json:"gaps"`
	}
	h.decode(h.do("GET", "/datasets/fake/BTCUSDT/1m/complete?start="+at(0)+"&end="+at(4), nil), http.StatusOK, &clean)
	if !clean.Complete {
		t.Error("complete = false over the range before the Gap, want true")
	}
	if len(clean.Gaps) != 0 {
		t.Errorf("gaps = %+v, want none", clean.Gaps)
	}
}

func TestShowCompletenessRejects(t *testing.T) {
	h := newHarness(t)
	cases := map[string]string{
		"missing start": "/datasets/fake/BTCUSDT/1m/complete?end=" + at(10),
		"missing end":   "/datasets/fake/BTCUSDT/1m/complete?start=" + at(0),
		"bad start":     "/datasets/fake/BTCUSDT/1m/complete?start=soon&end=" + at(10),
		"empty range":   "/datasets/fake/BTCUSDT/1m/complete?start=" + at(10) + "&end=" + at(0),
		"bad timeframe": "/datasets/fake/BTCUSDT/1w/complete?start=" + at(0) + "&end=" + at(10),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			h.expectError(h.do("GET", path, nil), http.StatusBadRequest)
		})
	}
}

// The default response is a Parquet stream another DuckDB reads back, flagged
// with the completeness of the range it covers. It is never refused for being
// incomplete.
func TestShowBarsParquet(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)

	res := h.do("GET", "/datasets/fake/BTCUSDT/1m/bars?start="+at(0)+"&end="+at(10), nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/vnd.apache.parquet" {
		t.Errorf("content type = %q, want application/vnd.apache.parquet", ct)
	}
	if got := res.Header.Get("X-Complete"); got != "false" {
		t.Errorf("X-Complete = %q over the range holding the Gap, want false", got)
	}
	var gaps []gapJSON
	if err := json.Unmarshal([]byte(res.Header.Get("X-Gaps")), &gaps); err != nil {
		t.Fatalf("X-Gaps %q is not JSON: %v", res.Header.Get("X-Gaps"), err)
	}
	if len(gaps) != 1 || gaps[0].Range.Start != at(4) || gaps[0].Range.End != at(5) {
		t.Errorf("X-Gaps = %+v, want the one Gap at minute 4", gaps)
	}

	body := h.body(res)
	if len(body) == 0 {
		t.Fatal("the Parquet response is empty")
	}
	path := filepath.Join(t.TempDir(), "bars.parquet")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing the export: %v", err)
	}
	if rows := parquetRows(t, path); rows != 9 {
		t.Errorf("read_parquet counted %d rows, want the 9 Bars that landed", rows)
	}
}

// The same request over a range with no Gap in it is flagged complete.
func TestShowBarsParquetComplete(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)

	res := h.do("GET", "/datasets/fake/BTCUSDT/1m/bars?start="+at(0)+"&end="+at(4), nil)
	if got := res.Header.Get("X-Complete"); got != "true" {
		t.Errorf("X-Complete = %q over the range before the Gap, want true", got)
	}
	if got := res.Header.Get("X-Gaps"); got != "[]" {
		t.Errorf("X-Gaps = %q, want an empty array", got)
	}

	path := filepath.Join(t.TempDir(), "bars.parquet")
	if err := os.WriteFile(path, h.body(res), 0o600); err != nil {
		t.Fatalf("writing the export: %v", err)
	}
	if rows := parquetRows(t, path); rows != 4 {
		t.Errorf("read_parquet counted %d rows, want 4", rows)
	}
}

func TestShowBarsJSON(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)

	res := h.do("GET", "/datasets/fake/BTCUSDT/1m/bars?start="+at(0)+"&end="+at(10)+"&format=json", nil)
	if got := res.Header.Get("X-Complete"); got != "false" {
		t.Errorf("X-Complete = %q, want false", got)
	}
	if got := res.Header.Get("X-Gaps"); got == "" || got == "[]" {
		t.Errorf("X-Gaps = %q, want the Gap the range holds", got)
	}

	var bars []struct {
		OpenTime string `json:"open_time"`
		Open     string `json:"open"`
		High     string `json:"high"`
		Low      string `json:"low"`
		Close    string `json:"close"`
		Volume   string `json:"volume"`
	}
	h.decode(res, http.StatusOK, &bars)
	if len(bars) != 9 {
		t.Fatalf("bars = %d, want the 9 that landed", len(bars))
	}
	if bars[0].OpenTime != at(0) || bars[8].OpenTime != at(9) {
		t.Errorf("open_times run %s…%s, want %s…%s", bars[0].OpenTime, bars[8].OpenTime, at(0), at(9))
	}
	if bars[4].OpenTime != at(5) {
		t.Errorf("bar 4 opens at %s, want %s: minute 4 never landed", bars[4].OpenTime, at(5))
	}
	if bars[0].Open != "10.00000000" || bars[0].Volume != "100.00000000" {
		t.Errorf("bar 0 = %+v, want the decimal strings it was stored as", bars[0])
	}
}

func TestShowBarsRejects(t *testing.T) {
	h := newHarness(t)
	cases := map[string]string{
		"missing start": "/datasets/fake/BTCUSDT/1m/bars?end=" + at(10),
		"bad end":       "/datasets/fake/BTCUSDT/1m/bars?start=" + at(0) + "&end=later",
		"empty range":   "/datasets/fake/BTCUSDT/1m/bars?start=" + at(4) + "&end=" + at(4),
		"bad timeframe": "/datasets/fake/BTCUSDT/1w/bars?start=" + at(0) + "&end=" + at(10),
		"bad format":    "/datasets/fake/BTCUSDT/1m/bars?start=" + at(0) + "&end=" + at(10) + "&format=csv",
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			h.expectError(h.do("GET", path, nil), http.StatusBadRequest)
		})
	}
}

// parquetRows opens a second, independent in-memory DuckDB and counts the
// rows of the export, which is how another service consumes one.
func parquetRows(t *testing.T, path string) int64 {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("second DuckDB: %v", err)
	}
	defer db.Close()

	var rows int64
	if err := db.QueryRow(`SELECT count(*) FROM read_parquet('` + path + `')`).Scan(&rows); err != nil {
		t.Fatalf("reading the export back: %v", err)
	}
	return rows
}
