package duckdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/domain"
)

// TestUpsertBarsAtBackfillVolume runs the batching path at the volume Phase 1
// accepts on: one January of 1m bars, upserted twice. It is the only test that
// crosses the multi-statement batch boundary.
func TestUpsertBarsAtBackfillVolume(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	const count = 44640 // one January of 1m bars
	bars := make([]domain.Bar, 0, count)
	for i := int64(0); i < count; i++ {
		bars = append(bars, bar(i, "105.00000000"))
	}
	start := time.Now()
	if err := store.UpsertBars(ctx, btcusdt1m, bars); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	t.Logf("first upsert of %d bars: %s", count, time.Since(start))
	start = time.Now()
	if err := store.UpsertBars(ctx, btcusdt1m, bars); err != nil {
		t.Fatalf("re-UpsertBars: %v", err)
	}
	t.Logf("second upsert: %s", time.Since(start))
	got, err := store.Bars(ctx, btcusdt1m, domain.Range{Start: at(0), End: at(count + 10)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != count {
		t.Fatalf("got %d bars, want %d", len(got), count)
	}
	t.Logf("rerun changed nothing: still %d bars", len(got))
}
