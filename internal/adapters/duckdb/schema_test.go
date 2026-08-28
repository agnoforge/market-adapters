package duckdb_test

import (
	"context"
	"slices"
	"testing"
)

func TestSchemaPrimaryKeys(t *testing.T) {
	store := open(t)
	ctx := context.Background()

	want := map[string][]string{
		"bars":     {"provider", "symbol", "timeframe", "open_time"},
		"coverage": {"provider", "symbol", "timeframe", "start_ms", "end_ms"},
		"gaps":     {"id"},
	}
	for table, columns := range want {
		got, err := store.PrimaryKeyForTest(ctx, table)
		if err != nil {
			t.Fatalf("primary key of %s: %v", table, err)
		}
		if !slices.Equal(got, columns) {
			t.Errorf("primary key of %s = %v, want %v", table, got, columns)
		}
	}
}

func TestSchemaDecimalColumns(t *testing.T) {
	store := open(t)
	ctx := context.Background()

	for _, column := range []string{"open", "high", "low", "close", "volume"} {
		declared, err := store.ColumnTypeForTest(ctx, "bars", column)
		if err != nil {
			t.Fatalf("type of bars.%s: %v", column, err)
		}
		if declared != "DECIMAL(20,8)" {
			t.Errorf("bars.%s is %s, want DECIMAL(20,8)", column, declared)
		}
	}
	declared, err := store.ColumnTypeForTest(ctx, "bars", "open_time")
	if err != nil {
		t.Fatalf("type of bars.open_time: %v", err)
	}
	if declared != "BIGINT" {
		t.Errorf("bars.open_time is %s, want BIGINT", declared)
	}
}
