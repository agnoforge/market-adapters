package duckdb_test

import (
	"context"
	"testing"
	"time"

	acquisitionduckdb "github.com/agnos/agnoforge/internal/acquisition/adapters/duckdb"
	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	compositeduckdb "github.com/agnos/agnoforge/internal/composite/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// Pricing a Transition is the second read this package makes of acquisition's
// bars table, and the one place a price is arithmetic rather than a value that
// is passed along. The arithmetic happens in SQL over the DECIMAL(20,8)
// columns, so these tests are about exactness: what comes back must be the
// decimal a person would write down, to the last of its eight places.

var catchUpSrc = domain.Source{
	Instrument: "BTC/USD", Provider: "coinbase",
	Symbol: acq.Symbol("BTC-USD"), Timeframe: domain.TF1m,
}

// seedBar writes one bar of a source Dataset at t, with the open and close a
// test cares about and prices around them that no assertion reads.
func seedBar(t *testing.T, source *acquisitionduckdb.Store, src domain.Source, at time.Time, open, closed string) {
	t.Helper()
	id := acq.DatasetID{Provider: src.Provider, Symbol: src.Symbol, Timeframe: acq.TF1m}
	if err := source.UpsertBars(context.Background(), id, []acq.Bar{{
		OpenTime: at,
		Open:     open, High: "999999.00000000", Low: "0.00000001",
		Close: closed, Volume: "1.00000000",
	}}); err != nil {
		t.Fatalf("seeding a bar of %s at %s: %v", src, at, err)
	}
}

// pricedStore is a composite store over a database seeded with the last bar of
// one source and the first bar of another, meeting at `boundary`.
func pricedStore(t *testing.T, closed, open string) (*compositeduckdb.Store, time.Time) {
	t.Helper()
	path := dbPath(t)
	source, err := acquisitionduckdb.Open(path)
	if err != nil {
		t.Fatalf("opening the acquisition store: %v", err)
	}
	t.Cleanup(func() { source.Close() })

	boundary := start.Add(30 * time.Minute)
	seedBar(t, source, baseSrc, boundary.Add(-time.Minute), "1.00000000", closed)
	seedBar(t, source, catchUpSrc, boundary, open, "2.00000000")
	return openStoreAt(t, path), boundary
}

func TestTransitionDeltaIsTheExactDecimalDifference(t *testing.T) {
	for _, tc := range []struct {
		name         string
		closed, open string
		wantDelta    string
	}{
		{"a step up", "100.50000000", "100.75000000", "0.25000000"},
		{"a step down", "100.50000000", "99.25000000", "-1.25000000"},
		{"no movement at all", "100.50000000", "100.50000000", "0.00000000"},
		// The two cases a float would get wrong: the smallest decimal the
		// column can hold, and a sum of tenths that has no binary form.
		{"one satoshi", "100.00000000", "100.00000001", "0.00000001"},
		{"a tenth of a cent", "0.30000000", "0.10000000", "-0.20000000"},
		{"a price no float holds exactly", "70123.45678901", "70123.45678902", "0.00000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, boundary := pricedStore(t, tc.closed, tc.open)
			got, err := store.TransitionDelta(context.Background(), baseSrc, catchUpSrc, boundary)
			if err != nil {
				t.Fatalf("TransitionDelta: %v", err)
			}
			if !got.Priced {
				t.Fatal("the transition came back unpriced, but both its bars are there")
			}
			if got.Close != tc.closed || got.Open != tc.open {
				t.Errorf("close/open = %q / %q, want %q / %q", got.Close, got.Open, tc.closed, tc.open)
			}
			if got.Delta != tc.wantDelta {
				t.Errorf("delta = %q, want exactly %q", got.Delta, tc.wantDelta)
			}
		})
	}
}

// TestTransitionDeltaTakesTheLastCloseBeforeTheBoundary: the outgoing price is
// the close of the bar that ends at the boundary, not of any earlier one, and
// the incoming price is the open of the bar that starts there.
func TestTransitionDeltaTakesTheLastCloseBeforeTheBoundary(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	source, err := acquisitionduckdb.Open(path)
	if err != nil {
		t.Fatalf("opening the acquisition store: %v", err)
	}
	t.Cleanup(func() { source.Close() })

	boundary := start.Add(30 * time.Minute)
	seedBar(t, source, baseSrc, boundary.Add(-2*time.Minute), "1.00000000", "10.00000000")
	seedBar(t, source, baseSrc, boundary.Add(-time.Minute), "1.00000000", "20.00000000")
	// A later bar of the outgoing source must not be the one that prices it.
	seedBar(t, source, baseSrc, boundary, "1.00000000", "30.00000000")
	seedBar(t, source, catchUpSrc, boundary, "25.00000000", "2.00000000")
	seedBar(t, source, catchUpSrc, boundary.Add(time.Minute), "99.00000000", "2.00000000")

	got, err := openStoreAt(t, path).TransitionDelta(ctx, baseSrc, catchUpSrc, boundary)
	if err != nil {
		t.Fatalf("TransitionDelta: %v", err)
	}
	if got.Close != "20.00000000" || got.Open != "25.00000000" || got.Delta != "5.00000000" {
		t.Fatalf("priced %q → %q = %q, want the last close before the boundary and the open at it",
			got.Close, got.Open, got.Delta)
	}
}

// TestATransitionWithoutBarsIsUnpricedNotAnError: a boundary one of the two
// sources has no bar at is recorded without a price rather than failing the
// Build that recorded it.
func TestATransitionWithoutBarsIsUnpricedNotAnError(t *testing.T) {
	store, boundary := pricedStore(t, "100.50000000", "100.75000000")
	ctx := context.Background()

	missingIncoming := catchUpSrc
	missingIncoming.Symbol = acq.Symbol("ETH-USD")
	got, err := store.TransitionDelta(ctx, baseSrc, missingIncoming, boundary)
	if err != nil {
		t.Fatalf("TransitionDelta over a source with no bars: %v", err)
	}
	if got.Priced || got.Delta != "" {
		t.Errorf("delta = %+v, want an unpriced transition", got)
	}

	// The same when the outgoing source has nothing before the boundary.
	got, err = store.TransitionDelta(ctx, baseSrc, catchUpSrc, start)
	if err != nil {
		t.Fatalf("TransitionDelta at the very start: %v", err)
	}
	if got.Priced {
		t.Errorf("delta = %+v, want an unpriced transition — nothing closed before the start", got)
	}
}

// TestSaveBuildRoundTripsTheTransitions: the Transitions a Build recorded come
// back with Quality, prices and all, and are replaced wholesale like the rest
// of it.
func TestSaveBuildRoundTripsTheTransitions(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	boundary := start.Add(30 * time.Minute)
	crossed := []domain.Segment{
		{Kind: domain.SegmentBase, Source: baseSrc, Range: acq.Range{Start: start, End: boundary}},
		{Kind: domain.SegmentCatchUp, Source: catchUpSrc, Range: acq.Range{Start: boundary, End: hour.End}},
	}
	q := builtQuality()
	q.Transitions = []domain.Transition{{
		At: boundary, From: baseSrc, To: catchUpSrc,
		Close: "100.50000000", Open: "100.75000000", Delta: "0.25000000",
	}}
	if err := store.SaveBuild(ctx, built("btc-usd"), crossed, q); err != nil {
		t.Fatalf("SaveBuild: %v", err)
	}

	got, hasQuality, err := store.Quality(ctx, "btc-usd")
	if err != nil || !hasQuality {
		t.Fatalf("Quality = %v, %v, want the build's own", hasQuality, err)
	}
	if got.TransitionCount() != 1 {
		t.Fatalf("transitions = %+v, want the one that was recorded", got.Transitions)
	}
	if !got.Transitions[0].At.Equal(boundary) {
		t.Errorf("transition at %s, want %s", got.Transitions[0].At, boundary)
	}
	if got.Transitions[0].From != baseSrc || got.Transitions[0].To != catchUpSrc {
		t.Errorf("transition = %s, want binance → coinbase", got.Transitions[0])
	}
	if got.Transitions[0].Delta != "0.25000000" {
		t.Errorf("delta read back = %q, want the exact decimal it was stored as", got.Transitions[0].Delta)
	}

	// A build that crosses no provider boundary leaves none behind.
	if err := store.SaveBuild(ctx, built("btc-usd"), segments, builtQuality()); err != nil {
		t.Fatalf("second SaveBuild: %v", err)
	}
	after, _, err := store.Quality(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Quality after the second build: %v", err)
	}
	if after.TransitionCount() != 0 {
		t.Fatalf("transitions = %+v, want none — they are replaced, not accumulated", after.Transitions)
	}
}
