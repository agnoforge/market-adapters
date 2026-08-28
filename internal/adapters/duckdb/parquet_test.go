package duckdb_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/agnos/agnoforge/internal/domain"
	_ "github.com/duckdb/duckdb-go/v2"
)

// readBack opens a second, independent in-memory DuckDB and points it at the
// Parquet file, which is how another service consumes an export.
func readBack(t *testing.T, query string) *sql.Rows {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("second DuckDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	res, err := db.Query(query)
	if err != nil {
		t.Fatalf("reading the export back: %v", err)
	}
	t.Cleanup(func() { res.Close() })
	return res
}

func TestExportParquetRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	bars := make([]domain.Bar, 0, 120)
	for minute := int64(0); minute < 120; minute++ {
		bars = append(bars, bar(minute, "105.00000000"))
	}
	if err := store.UpsertBars(ctx, btcusdt1m, bars); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}

	var buf bytes.Buffer
	exported := rng(10, 60) // half-open: 50 bars
	if err := store.ExportParquet(ctx, btcusdt1m, exported, &buf); err != nil {
		t.Fatalf("ExportParquet: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("ExportParquet wrote nothing")
	}

	path := filepath.Join(t.TempDir(), "bars.parquet")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("writing the export: %v", err)
	}

	res := readBack(t, `SELECT count(*) FROM read_parquet('`+path+`')`)
	if !res.Next() {
		t.Fatal("count query returned nothing")
	}
	var count int64
	if err := res.Scan(&count); err != nil {
		t.Fatalf("scanning count: %v", err)
	}
	if count != 50 {
		t.Errorf("read_parquet counted %d bars, want 50", count)
	}
}

func TestExportParquetColumnsAndValues(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	if err := store.UpsertBars(ctx, btcusdt1m, []domain.Bar{bar(1, "105.25000000")}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}

	var buf bytes.Buffer
	if err := store.ExportParquet(ctx, btcusdt1m, rng(0, 100), &buf); err != nil {
		t.Fatalf("ExportParquet: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bars.parquet")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("writing the export: %v", err)
	}

	res := readBack(t, `DESCRIBE SELECT * FROM read_parquet('`+path+`')`)
	var names, types []string
	for res.Next() {
		columns, err := res.Columns()
		if err != nil {
			t.Fatalf("columns: %v", err)
		}
		cells := make([]any, len(columns))
		holders := make([]any, len(columns))
		for i := range cells {
			holders[i] = &cells[i]
		}
		if err := res.Scan(holders...); err != nil {
			t.Fatalf("scanning DESCRIBE: %v", err)
		}
		names = append(names, cells[0].(string))
		types = append(types, cells[1].(string))
	}

	wantNames := []string{"open_time", "open", "high", "low", "close", "volume"}
	wantTypes := []string{"BIGINT", "DECIMAL(20,8)", "DECIMAL(20,8)", "DECIMAL(20,8)", "DECIMAL(20,8)", "DECIMAL(20,8)"}
	if len(names) != len(wantNames) {
		t.Fatalf("exported columns %v, want %v", names, wantNames)
	}
	for i := range wantNames {
		if names[i] != wantNames[i] {
			t.Errorf("column %d is %q, want %q", i, names[i], wantNames[i])
		}
		if types[i] != wantTypes[i] {
			t.Errorf("column %q is %s, want %s", names[i], types[i], wantTypes[i])
		}
	}

	// The exported values survive the trip exactly.
	values := readBack(t, `SELECT open_time, CAST("close" AS VARCHAR) FROM read_parquet('`+path+`')`)
	if !values.Next() {
		t.Fatal("no bar in the export")
	}
	var openTime int64
	var closePrice string
	if err := values.Scan(&openTime, &closePrice); err != nil {
		t.Fatalf("scanning: %v", err)
	}
	if openTime != 60_000 {
		t.Errorf("open_time = %d, want 60000", openTime)
	}
	if closePrice != "105.25000000" {
		t.Errorf("close = %q, want %q", closePrice, "105.25000000")
	}
}

func TestExportParquetOfNothingIsStillReadable(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	var buf bytes.Buffer
	if err := store.ExportParquet(ctx, btcusdt1m, rng(0, 100), &buf); err != nil {
		t.Fatalf("ExportParquet: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bars.parquet")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("writing the export: %v", err)
	}
	res := readBack(t, `SELECT count(*) FROM read_parquet('`+path+`')`)
	if !res.Next() {
		t.Fatal("count query returned nothing")
	}
	var count int64
	if err := res.Scan(&count); err != nil {
		t.Fatalf("scanning count: %v", err)
	}
	if count != 0 {
		t.Errorf("an empty export holds %d bars, want 0", count)
	}
}

func TestExportParquetLeavesNoTemporaryFile(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	if err := store.UpsertBars(ctx, btcusdt1m, []domain.Bar{bar(1, "105.00000000")}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}

	pattern := filepath.Join(os.TempDir(), "agnoforge-parquet-*")
	before, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var buf bytes.Buffer
	if err := store.ExportParquet(ctx, btcusdt1m, rng(0, 100), &buf); err != nil {
		t.Fatalf("ExportParquet: %v", err)
	}

	after, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("ExportParquet left %d temporary directories behind", len(after)-len(before))
	}
}
