package duckdb_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	acquisitionduckdb "github.com/agnos/agnoforge/internal/adapters/duckdb"
	compositeduckdb "github.com/agnos/agnoforge/internal/composite/adapters/duckdb"
	compositeapp "github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// Materialization against a real database: the SQL aggregation reads
// acquisition's own bars read-only (ADR-0005) and derives higher-timeframe bars
// from them — decimal-exact, on the calendar boundaries the domain defines, and
// idempotent under a rebuild.

// matBar is one materialized bar as it is stored: the prices as the exact
// decimal strings the database holds, never as floats.
type matBar struct {
	OpenTime                       time.Time
	Open, High, Low, Close, Volume string
	Version                        int
}

func (b matBar) String() string {
	return fmt.Sprintf("%s o=%s h=%s l=%s c=%s v=%s (v%d)",
		b.OpenTime.UTC().Format(time.RFC3339), b.Open, b.High, b.Low, b.Close, b.Volume, b.Version)
}

// fixture is one database file with both contexts open on it: acquisition's
// store to seed real source bars with, and the composite store under test.
type fixture struct {
	t      *testing.T
	path   string
	store  *compositeduckdb.Store
	source *acquisitionduckdb.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := dbPath(t)
	source, err := acquisitionduckdb.Open(path)
	if err != nil {
		t.Fatalf("opening the acquisition store: %v", err)
	}
	t.Cleanup(func() { source.Close() })
	f := &fixture{t: t, path: path, store: openStoreAt(t, path), source: source}
	if err := f.store.CreateDataset(context.Background(), declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	return f
}

// seed writes one source bar per minute of r for a source, priced by a function
// of the minute so every bar is distinguishable.
func (f *fixture) seed(src domain.Source, r acq.Range, price func(i int) acq.Bar) {
	f.t.Helper()
	id := acq.DatasetID{Provider: src.Provider, Symbol: src.Symbol, Timeframe: acq.TF1m}
	var bars []acq.Bar
	for i, t := 0, r.Start.UTC(); t.Before(r.End); i, t = i+1, t.Add(time.Minute) {
		bar := price(i)
		bar.OpenTime = t
		bars = append(bars, bar)
	}
	if len(bars) == 0 {
		return
	}
	if err := f.source.UpsertBars(context.Background(), id, bars); err != nil {
		f.t.Fatalf("seeding the bars of %s over %s: %v", src, r, err)
	}
}

// seedMinutes writes bars only for the minutes select says yes to — the way a
// gap in the source data really looks.
func (f *fixture) seedMinutes(src domain.Source, r acq.Range, keep func(i int) bool, price func(i int) acq.Bar) {
	f.t.Helper()
	id := acq.DatasetID{Provider: src.Provider, Symbol: src.Symbol, Timeframe: acq.TF1m}
	var bars []acq.Bar
	for i, t := 0, r.Start.UTC(); t.Before(r.End); i, t = i+1, t.Add(time.Minute) {
		if !keep(i) {
			continue
		}
		bar := price(i)
		bar.OpenTime = t
		bars = append(bars, bar)
	}
	if len(bars) == 0 {
		return
	}
	if err := f.source.UpsertBars(context.Background(), id, bars); err != nil {
		f.t.Fatalf("seeding the bars of %s over %s: %v", src, r, err)
	}
}

// materialize derives the frames of the dataset over the segments.
func (f *fixture) materialize(segments []domain.Segment, frames ...domain.Timeframe) int64 {
	f.t.Helper()
	written, err := f.store.Materialize(context.Background(), "btc-usd", compositeapp.Materialization{
		Segments: segments, Timeframes: frames, Version: domain.MaterializationVersion,
	})
	if err != nil {
		f.t.Fatalf("Materialize: %v", err)
	}
	return written
}

// bars reads the materialized bars of one timeframe back through a connection
// of its own, as the exact decimal text the database stores. Reading them as
// strings is the point: a float anywhere in the aggregation would show up here.
func (f *fixture) bars(tf domain.Timeframe) []matBar {
	f.t.Helper()
	db, err := sql.Open("duckdb", f.path)
	if err != nil {
		f.t.Fatalf("opening the database to read the materialized bars: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT open_time,
		       CAST("open" AS VARCHAR), CAST(high AS VARCHAR), CAST(low AS VARCHAR),
		       CAST("close" AS VARCHAR), CAST(volume AS VARCHAR), mat_version
		FROM composite_materialized_bars
		WHERE dataset = 'btc-usd' AND timeframe = ?
		ORDER BY open_time`, tf.String())
	if err != nil {
		f.t.Fatalf("reading the materialized %s bars: %v", tf, err)
	}
	defer rows.Close()

	var out []matBar
	for rows.Next() {
		var (
			b  matBar
			ms int64
		)
		if err := rows.Scan(&ms, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume, &b.Version); err != nil {
			f.t.Fatalf("scanning a materialized bar: %v", err)
		}
		b.OpenTime = time.UnixMilli(ms).UTC()
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("reading the materialized %s bars: %v", tf, err)
	}
	return out
}

// base is the one segment most of these tests derive from.
func base(r acq.Range) []domain.Segment {
	return []domain.Segment{{Kind: domain.SegmentBase, Source: baseSrc, Range: r}}
}

// TestMaterializeDerivesEachWindowExactly is the aggregation rule itself: first
// open, max high, min low, last close, summed volume — over decimals that stay
// decimals. The volumes are three tenths, which a float would sum to
// 0.30000000000000004; the prices differ in the eighth decimal, which a float
// price would lose entirely.
func TestMaterializeDerivesEachWindowExactly(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(10 * time.Minute)}
	// Minute i is worth 100.0000000i, with a high two ticks above and a low two
	// below, so the extremes of a window are never its first or last bar.
	f.seed(f.dataset(), r, func(i int) acq.Bar {
		return acq.Bar{
			Open:   fmt.Sprintf("100.0000000%d", i),
			High:   fmt.Sprintf("101.0000000%d", (i+3)%10),
			Low:    fmt.Sprintf("99.0000000%d", (i+7)%10),
			Close:  fmt.Sprintf("100.5000000%d", i),
			Volume: "0.10000000",
		}
	})
	f.materialize(base(r), domain.TF5m)

	got := f.bars(domain.TF5m)
	want := []matBar{
		{
			OpenTime: start,
			// Minutes 0..4: opens 100.00000000..100.00000004, highs
			// 101.00000003..101.00000007, lows wrapping through 99.00000000.
			Open: "100.00000000", High: "101.00000007", Low: "99.00000000",
			Close: "100.50000004", Volume: "0.50000000", Version: domain.MaterializationVersion,
		},
		{
			OpenTime: start.Add(5 * time.Minute),
			// Minutes 5..9: highs wrap through 101.00000008, 101.00000009,
			// 101.00000000…; lows through 99.00000002…99.00000006.
			Open: "100.00000005", High: "101.00000009", Low: "99.00000002",
			Close: "100.50000009", Volume: "0.50000000", Version: domain.MaterializationVersion,
		},
	}
	assertBars(t, got, want)
}

// TestMaterializeSumsVolumeWithoutFloat is the decimal-exactness claim on its
// own terms: ten hundredths summed in float are 0.9999999999999999, and here
// they are a tenth of a unit, exactly.
func TestMaterializeSumsVolumeWithoutFloat(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(10 * time.Minute)}
	f.seed(f.dataset(), r, func(int) acq.Bar {
		return acq.Bar{Open: "0.00000001", High: "0.00000003", Low: "0.00000001", Close: "0.00000002", Volume: "0.10000000"}
	})
	f.materialize(base(r), domain.TF1h)

	got := f.bars(domain.TF1h)
	if len(got) != 1 {
		t.Fatalf("materialized bars = %v, want the single hour", got)
	}
	if got[0].Volume != "1.00000000" {
		t.Errorf("volume = %s, want exactly 1.00000000", got[0].Volume)
	}
	if got[0].Open != "0.00000001" || got[0].High != "0.00000003" || got[0].Low != "0.00000001" || got[0].Close != "0.00000002" {
		t.Errorf("bar = %s, want the eighth decimal preserved end to end", got[0])
	}
}

// TestMaterializeKeepsPricesTooPreciseForAFloat is the no-float claim made
// decisive. These prices carry thirteen significant digits, which a float64
// cannot hold: the spacing between neighbouring doubles up here is wider than
// the eighth decimal, so an aggregation that passed a price through a float
// would come back with a different number — not a rounding that happens to
// cancel out. Every value below is the exact decimal that went in.
func TestMaterializeKeepsPricesTooPreciseForAFloat(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(10 * time.Minute)}
	f.seed(f.dataset(), r, func(i int) acq.Bar {
		return acq.Bar{
			Open:  fmt.Sprintf("12345678901.0000000%d", i),
			High:  fmt.Sprintf("12345678902.0000000%d", (i+3)%10),
			Low:   fmt.Sprintf("12345678900.0000000%d", (i+7)%10),
			Close: fmt.Sprintf("12345678901.0000000%d", i),
			// Ten of these summed in a float would land on 12345678901.00000024.
			Volume: "1234567890.10000001",
		}
	})
	f.materialize(base(r), domain.TF1h)

	assertBars(t, f.bars(domain.TF1h), []matBar{{
		OpenTime: start,
		Open:     "12345678901.00000000",
		High:     "12345678902.00000009",
		Low:      "12345678900.00000000",
		Close:    "12345678901.00000009",
		Volume:   "12345678901.00000010",
		Version:  domain.MaterializationVersion,
	}})
}

// TestMaterializeGroupsFixedFramesOnEpochBoundaries and its calendar sibling
// below are the agreement this whole feature rests on: the SQL grouping and the
// domain's own window arithmetic must name the same open time for every bar.
func TestMaterializeGroupsFixedFramesOnEpochBoundaries(t *testing.T) {
	// A range that starts mid-window on purpose: 00:07, deliberately not a 5m,
	// 15m, 30m, 1h or 4h boundary.
	from := time.Date(2024, 3, 28, 22, 7, 0, 0, time.UTC)
	r := acq.Range{Start: from, End: from.Add(6 * time.Hour)}
	for _, tf := range []domain.Timeframe{domain.TF5m, domain.TF15m, domain.TF30m, domain.TF1h, domain.TF4h, domain.TF1d} {
		t.Run(tf.String(), func(t *testing.T) {
			f := newFixture(t)
			f.seed(f.dataset(), r, flatBar)
			f.materialize(base(r), tf)
			assertWindowsMatchTheDomain(t, f, tf, r)
		})
	}
}

// TestMaterializeGroupsCalendarFramesTheWayTicket02Defines is the one the SQL
// could plausibly get wrong: DuckDB's own week semantics have to agree with
// Monday 00:00 UTC, and its months with the 1st at 00:00 UTC.
func TestMaterializeGroupsCalendarFramesTheWayTicket02Defines(t *testing.T) {
	// Wednesday 2024-02-28 through Tuesday 2024-03-05: a leap-year month
	// boundary inside a week, so weeks and months cannot be the same grouping.
	from := time.Date(2024, 2, 28, 21, 0, 0, 0, time.UTC)
	r := acq.Range{Start: from, End: from.Add(7 * 24 * time.Hour)}
	for _, tf := range []domain.Timeframe{domain.TF1w, domain.TF1M} {
		t.Run(tf.String(), func(t *testing.T) {
			f := newFixture(t)
			// Hourly bars keep the seed small; the grouping is what is asserted.
			f.seedMinutes(f.dataset(), r, func(i int) bool { return i%60 == 0 }, flatBar)
			f.materialize(base(r), tf)
			assertWindowsMatchTheDomain(t, f, tf, r)
		})
	}

	// And the boundaries are the ones a chart draws: the week of Monday
	// 2024-02-26 and the week of Monday 2024-03-04, February and March.
	f := newFixture(t)
	f.seedMinutes(f.dataset(), r, func(i int) bool { return i%60 == 0 }, flatBar)
	f.materialize(base(r), domain.TF1w, domain.TF1M)
	assertOpenTimes(t, f.bars(domain.TF1w), []time.Time{
		time.Date(2024, 2, 26, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC),
	})
	assertOpenTimes(t, f.bars(domain.TF1M), []time.Time{
		time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
	})
}

// TestAWindowWithASourceBarEmitsOneAndAWindowWithoutDoesNot is the emission
// rule over a gap: the window holding the surviving bars is materialized from
// them, and the window the source has nothing in at all produces no bar rather
// than a fabricated one.
func TestAWindowWithASourceBarEmitsOneAndAWindowWithoutDoesNot(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(20 * time.Minute)}
	// Minutes 5..14 are missing: the 5m window at :05 and the one at :10 have
	// no bar at all, and the windows either side are whole.
	f.seedMinutes(f.dataset(), r, func(i int) bool { return i < 5 || i >= 15 }, flatBar)
	f.materialize(base(r), domain.TF5m)

	assertOpenTimes(t, f.bars(domain.TF5m), []time.Time{start, start.Add(15 * time.Minute)})

	// The hour over the same data is one bar derived from the ten bars that are
	// really there — an incomplete window, emitted and flagged, never hidden.
	f.materialize(base(r), domain.TF5m, domain.TF1h)
	hourly := f.bars(domain.TF1h)
	if len(hourly) != 1 || hourly[0].Volume != "10.00000000" {
		t.Fatalf("hourly bars = %v, want one bar summing only the ten bars that exist", hourly)
	}
}

// TestAPartlyCoveredWindowIsDerivedFromWhatIsThere: a window the timeline only
// covers part of is derived from that part, not from source bars outside the
// segment — the composite timeline is what the Segments say it is.
func TestAPartlyCoveredWindowIsDerivedFromWhatIsThere(t *testing.T) {
	f := newFixture(t)
	// The source has a whole hour, but the dataset's timeline is only the first
	// half of it.
	whole := acq.Range{Start: start, End: start.Add(time.Hour)}
	f.seed(f.dataset(), whole, flatBar)
	timeline := acq.Range{Start: start, End: start.Add(30 * time.Minute)}
	f.materialize(base(timeline), domain.TF1h)

	got := f.bars(domain.TF1h)
	if len(got) != 1 || got[0].Volume != "30.00000000" {
		t.Fatalf("hourly bars = %v, want one bar over the 30 minutes the segment covers", got)
	}
}

// TestRebuildConvergesToIdenticalBars is idempotence: the same timeline
// materialized again replaces the affected windows with bars that are equal
// down to the last decimal, and a timeline that shrank leaves no bar behind it.
func TestRebuildConvergesToIdenticalBars(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(time.Hour)}
	f.seed(f.dataset(), r, func(i int) acq.Bar {
		return acq.Bar{
			Open: fmt.Sprintf("100.0000000%d", i%10), High: "101.00000009",
			Low: "99.00000001", Close: fmt.Sprintf("100.5000000%d", i%10), Volume: "0.10000000",
		}
	})

	first := f.materialize(base(r), domain.TF5m, domain.TF1h)
	before := append(f.bars(domain.TF5m), f.bars(domain.TF1h)...)
	second := f.materialize(base(r), domain.TF5m, domain.TF1h)
	after := append(f.bars(domain.TF5m), f.bars(domain.TF1h)...)

	if first != second {
		t.Errorf("the rebuild wrote %d bars where the first build wrote %d", second, first)
	}
	assertBars(t, after, before)

	// A rebuild over a shorter timeline replaces the windows it touches and
	// takes the windows that are no longer materialized with it: what is stored
	// always describes the build it came from.
	half := acq.Range{Start: start, End: start.Add(30 * time.Minute)}
	f.materialize(base(half), domain.TF5m, domain.TF1h)
	shrunk := f.bars(domain.TF5m)
	if len(shrunk) != 6 {
		t.Fatalf("5m bars after the shorter rebuild = %v, want the six windows of the half hour", shrunk)
	}
}

// TestMaterializeRemovesTheTimeframesTheBuildNoLongerDerives, including all of
// them: a Build that cannot be ready leaves no derived bars behind.
func TestMaterializeRemovesTheTimeframesTheBuildNoLongerDerives(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(time.Hour)}
	f.seed(f.dataset(), r, flatBar)
	f.materialize(base(r), domain.TF5m, domain.TF1h)
	if len(f.bars(domain.TF5m)) == 0 || len(f.bars(domain.TF1h)) == 0 {
		t.Fatal("the first materialization wrote no bars")
	}

	f.materialize(base(r), domain.TF1h)
	if got := f.bars(domain.TF5m); len(got) != 0 {
		t.Errorf("5m bars = %v, want none: the build no longer derives that frame", got)
	}
	if got := f.bars(domain.TF1h); len(got) != 1 {
		t.Errorf("1h bars = %v, want the one still derived", got)
	}

	f.materialize(nil)
	if got := append(f.bars(domain.TF5m), f.bars(domain.TF1h)...); len(got) != 0 {
		t.Errorf("bars = %v, want none at all", got)
	}
}

// TestMaterializeDerivesAcrossAProviderTransition: a window the timeline
// changes hands inside is one bar over both providers' data, opened by the
// outgoing source and closed by the incoming one.
func TestMaterializeDerivesAcrossAProviderTransition(t *testing.T) {
	f := newFixture(t)
	other := domain.Source{Instrument: "BTC/USD", Provider: "coinbase", Symbol: acq.Symbol("BTC-USD"), Timeframe: domain.TF1m}
	first := acq.Range{Start: start, End: start.Add(30 * time.Minute)}
	second := acq.Range{Start: start.Add(30 * time.Minute), End: start.Add(time.Hour)}

	f.seed(f.dataset(), acq.Range{Start: start, End: start.Add(time.Hour)}, func(int) acq.Bar {
		return acq.Bar{Open: "100.00000000", High: "100.00000000", Low: "100.00000000", Close: "100.00000000", Volume: "1.00000000"}
	})
	f.seed(other, acq.Range{Start: start, End: start.Add(time.Hour)}, func(int) acq.Bar {
		return acq.Bar{Open: "200.00000000", High: "200.00000000", Low: "200.00000000", Close: "200.00000000", Volume: "1.00000000"}
	})

	f.materialize([]domain.Segment{
		{Kind: domain.SegmentBase, Source: baseSrc, Range: first},
		{Kind: domain.SegmentCatchUp, Source: other, Range: second},
	}, domain.TF1h)

	got := f.bars(domain.TF1h)
	if len(got) != 1 {
		t.Fatalf("hourly bars = %v, want the one hour", got)
	}
	want := matBar{
		OpenTime: start, Open: "100.00000000", High: "200.00000000",
		Low: "100.00000000", Close: "200.00000000", Volume: "60.00000000",
		Version: domain.MaterializationVersion,
	}
	assertBars(t, got, []matBar{want})

	// And only the segments' own instants are read: the coinbase bars of the
	// first half hour, which the timeline does not claim, are not in the sum.
	if got[0].Volume != "60.00000000" {
		t.Errorf("volume = %s, want the 60 minutes of the timeline, not the 120 bars in the table", got[0].Volume)
	}
}

// TestTheDatasetRowCarriesTheMaterializationVersion: the version the bars were
// derived by lives on the dataset row as well as on every bar, which is what a
// later service compares to decide the dataset is stale.
func TestTheDatasetRowCarriesTheMaterializationVersion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := acq.Range{Start: start, End: start.Add(time.Hour)}
	f.seed(f.dataset(), r, flatBar)
	f.materialize(base(r), domain.TF1h)

	d := built("btc-usd")
	d.MatVersion = domain.MaterializationVersion
	q := builtQuality()
	q.MatVersion = domain.MaterializationVersion
	if err := f.store.SaveBuild(ctx, d, base(r), q); err != nil {
		t.Fatalf("SaveBuild: %v", err)
	}

	read, err := f.store.Dataset(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Dataset: %v", err)
	}
	if read.MatVersion != domain.MaterializationVersion {
		t.Errorf("dataset row's materialization version = %d, want %d",
			read.MatVersion, domain.MaterializationVersion)
	}
	if read.Observed().State != domain.StateReady {
		t.Errorf("state = %q, want ready: the bars are this version's", read.Observed().State)
	}
	// The bars carry the same version, and the quality records it too.
	for _, b := range f.bars(domain.TF1h) {
		if b.Version != domain.MaterializationVersion {
			t.Errorf("bar %s is stamped v%d, want v%d", b, b.Version, domain.MaterializationVersion)
		}
	}
	storedQuality, ok, err := f.store.Quality(ctx, "btc-usd")
	if err != nil || !ok {
		t.Fatalf("Quality = %v, %v, want the stored one", ok, err)
	}
	if storedQuality.MatVersion != domain.MaterializationVersion {
		t.Errorf("quality's materialization version = %d, want %d",
			storedQuality.MatVersion, domain.MaterializationVersion)
	}

	// A row stamped by an earlier version reads as stale, whatever it says.
	stale := read
	stale.MatVersion = domain.MaterializationVersion - 1
	if err := f.store.UpdateDataset(ctx, stale); err != nil {
		t.Fatalf("UpdateDataset: %v", err)
	}
	back, err := f.store.Dataset(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Dataset: %v", err)
	}
	if back.MatVersion != domain.MaterializationVersion-1 || back.Observed().State != domain.StateStale {
		t.Errorf("dataset = v%d reading as %q, want v%d reading as stale",
			back.MatVersion, back.Observed().State, domain.MaterializationVersion-1)
	}
}

// TestQualityRoundTripsTheIncompleteWindows: the windows a Build flagged are
// stored and read back per timeframe, in the order Quality lists them.
func TestQualityRoundTripsTheIncompleteWindows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	q := builtQuality()
	q.IncompleteWindows = []domain.IncompleteWindow{
		{Timeframe: domain.TF5m, Range: acq.Range{Start: start.Add(10 * time.Minute), End: start.Add(15 * time.Minute)}},
		{Timeframe: domain.TF1h, Range: acq.Range{Start: start, End: start.Add(time.Hour)}},
	}
	if err := f.store.SaveBuild(ctx, built("btc-usd"), segments, q); err != nil {
		t.Fatalf("SaveBuild: %v", err)
	}
	got, ok, err := f.store.Quality(ctx, "btc-usd")
	if err != nil || !ok {
		t.Fatalf("Quality = %v, %v, want the stored one", ok, err)
	}
	if got.IncompleteWindowCount() != 2 {
		t.Fatalf("incomplete windows = %v, want the two stored ones", got.IncompleteWindows)
	}
	for i, want := range q.IncompleteWindows {
		if got.IncompleteWindows[i].Timeframe != want.Timeframe ||
			!got.IncompleteWindows[i].Range.Start.Equal(want.Range.Start) ||
			!got.IncompleteWindows[i].Range.End.Equal(want.Range.End) {
			t.Errorf("incomplete window %d = %s, want %s", i, got.IncompleteWindows[i], want)
		}
	}

	// They are replaced wholesale like the rest of a build's quality.
	if err := f.store.SaveBuild(ctx, built("btc-usd"), segments, builtQuality()); err != nil {
		t.Fatalf("second SaveBuild: %v", err)
	}
	if again, _, _ := f.store.Quality(ctx, "btc-usd"); again.IncompleteWindowCount() != 0 {
		t.Errorf("incomplete windows = %v, want the second build's none", again.IncompleteWindows)
	}
}

// TestDeleteRemovesTheMaterializedBars: deleting a dataset removes everything
// derived from it, and never a source bar.
func TestDeleteRemovesTheMaterializedBars(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := acq.Range{Start: start, End: start.Add(time.Hour)}
	f.seed(f.dataset(), r, flatBar)
	f.materialize(base(r), domain.TF1h)

	if err := f.store.DeleteDataset(ctx, "btc-usd"); err != nil {
		t.Fatalf("DeleteDataset: %v", err)
	}
	if got := f.bars(domain.TF1h); len(got) != 0 {
		t.Errorf("materialized bars after the delete = %v, want none", got)
	}
	// The source data is untouched: this context only ever reads it.
	bars, err := f.store.SourceBars(ctx, baseSrc, r)
	if err != nil || bars.Count != 60 {
		t.Errorf("source bars = %d, %v, want the 60 seeded ones untouched", bars.Count, err)
	}
}

// dataset is the base source these tests seed and derive from.
func (f *fixture) dataset() domain.Source { return baseSrc }

// flatBar is one bar worth a flat unit price and one unit of volume.
func flatBar(int) acq.Bar {
	return acq.Bar{Open: "1.00000000", High: "1.00000000", Low: "1.00000000", Close: "1.00000000", Volume: "1.00000000"}
}

// assertBars insists on exactly these materialized bars, in this order.
func assertBars(t *testing.T, got, want []matBar) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("materialized bars = %v, want %v", got, want)
	}
	for i := range want {
		if !got[i].OpenTime.Equal(want[i].OpenTime) || got[i].Open != want[i].Open ||
			got[i].High != want[i].High || got[i].Low != want[i].Low ||
			got[i].Close != want[i].Close || got[i].Volume != want[i].Volume ||
			got[i].Version != want[i].Version {
			t.Fatalf("materialized bar %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// assertOpenTimes insists on exactly these window open times.
func assertOpenTimes(t *testing.T, got []matBar, want []time.Time) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("materialized bars = %v, want %d windows %v", got, len(want), want)
	}
	for i := range want {
		if !got[i].OpenTime.Equal(want[i]) {
			t.Fatalf("window %d opens at %s, want %s", i, got[i].OpenTime, want[i])
		}
	}
}

// assertWindowsMatchTheDomain insists the SQL grouped every bar of r into the
// window the domain's own arithmetic (ticket 02) puts it in: same window starts,
// in the same order, with nothing invented and nothing dropped.
func assertWindowsMatchTheDomain(t *testing.T, f *fixture, tf domain.Timeframe, r acq.Range) {
	t.Helper()
	var want []time.Time
	for s := range tf.WindowStarts(r) {
		want = append(want, s)
	}
	assertOpenTimes(t, f.bars(tf), want)
}
