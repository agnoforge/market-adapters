package duckdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// btcusdt1m is the Dataset every test writes to unless it needs a second one.
var btcusdt1m = domain.DatasetID{
	Provider:  "binance",
	Symbol:    "BTCUSDT",
	Timeframe: domain.TF1m,
}

// open returns a Store on an in-memory DuckDB, closed when the test ends. No
// test in this package ever names a path on disk.
func open(t *testing.T) *duckdb.Store {
	t.Helper()
	store, err := duckdb.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

// at is the open_time of the minute bar at the given epoch minute.
func at(minute int64) time.Time { return time.UnixMilli(minute * 60_000).UTC() }

// bar builds a valid Bar opening at the given epoch minute and closing at the
// given price. High and low are widened around open and close so the Bar
// always passes domain validation.
func bar(minute int64, closePrice string) domain.Bar {
	return domain.Bar{
		OpenTime: at(minute),
		Open:     "100.00000000",
		High:     "1000.00000000",
		Low:      "0.00000001",
		Close:    closePrice,
		Volume:   "1.50000000",
	}
}

func TestOpenCreatesSchemaIdempotently(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	if err := store.UpsertBars(ctx, btcusdt1m, []domain.Bar{bar(1, "105.00000000")}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}

	// Opening a database that already holds the schema must not fail and must
	// not wipe it: this is the statement set Open runs every time.
	if err := store.ReapplySchemaForTest(ctx); err != nil {
		t.Fatalf("reapplying the schema: %v", err)
	}
	stored, err := store.Bars(ctx, btcusdt1m, domain.Range{Start: at(0), End: at(100)})
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("reapplying the schema left %d bars, want 1", len(stored))
	}
}

func TestBarsPrimaryKeyRejectsDuplicate(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	if err := store.UpsertBars(ctx, btcusdt1m, []domain.Bar{bar(1, "105.00000000")}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	// A plain INSERT of the same key must violate the primary key. Only the
	// PK on (provider, symbol, timeframe, open_time) can produce this.
	err := store.ExecForTest(ctx, `INSERT INTO bars VALUES ('binance','BTCUSDT','1m',60000,1,1,1,1,1)`)
	if err == nil {
		t.Fatal("plain INSERT of a duplicate key succeeded; the primary key is missing")
	}
	t.Logf("duplicate key rejected: %v", err)
}

func TestUpsertBarsIsIdempotentAndOverwrites(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	bars := []domain.Bar{bar(1, "105.00000000"), bar(2, "106.00000000"), bar(3, "107.00000000")}

	if err := store.UpsertBars(ctx, btcusdt1m, bars); err != nil {
		t.Fatalf("first UpsertBars: %v", err)
	}
	if err := store.UpsertBars(ctx, btcusdt1m, bars); err != nil {
		t.Fatalf("second UpsertBars: %v", err)
	}

	whole := domain.Range{Start: at(0), End: at(100)}
	stored, err := store.Bars(ctx, btcusdt1m, whole)
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("after upserting the same 3 bars twice: got %d bars, want 3", len(stored))
	}

	// A changed value at an existing open_time replaces the stored one.
	if err := store.UpsertBars(ctx, btcusdt1m, []domain.Bar{bar(2, "999.00000000")}); err != nil {
		t.Fatalf("overwriting UpsertBars: %v", err)
	}
	stored, err = store.Bars(ctx, btcusdt1m, whole)
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("after overwriting one bar: got %d bars, want 3", len(stored))
	}
	if stored[1].Close != "999.00000000" {
		t.Errorf("close of the overwritten bar = %q, want %q", stored[1].Close, "999.00000000")
	}
	if !stored[1].OpenTime.Equal(at(2)) {
		t.Errorf("open_time = %s, want %s", stored[1].OpenTime, at(2))
	}
}

func TestUpsertBarsKeepsDatasetsApart(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	other := domain.DatasetID{Provider: "binance", Symbol: "ETHUSDT", Timeframe: domain.TF1m}

	if err := store.UpsertBars(ctx, btcusdt1m, []domain.Bar{bar(1, "105.00000000")}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	if err := store.UpsertBars(ctx, other, []domain.Bar{bar(1, "205.00000000")}); err != nil {
		t.Fatalf("UpsertBars other: %v", err)
	}

	whole := domain.Range{Start: at(0), End: at(100)}
	mine, err := store.Bars(ctx, btcusdt1m, whole)
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if len(mine) != 1 || mine[0].Close != "105.00000000" {
		t.Fatalf("same open_time in another Dataset leaked: %+v", mine)
	}
}

func TestUpsertBarsRejectsInvalidBar(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	broken := bar(1, "105.00000000")
	broken.Low = "200.00000000" // above min(open, close)

	if err := store.UpsertBars(ctx, btcusdt1m, []domain.Bar{broken}); err == nil {
		t.Fatal("UpsertBars accepted a bar that fails Validate")
	}
	stored, err := store.Bars(ctx, btcusdt1m, domain.Range{Start: at(0), End: at(100)})
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("a rejected batch still landed %d bars", len(stored))
	}
}
