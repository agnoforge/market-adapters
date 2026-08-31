package duckdb_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	acquisitionduckdb "github.com/agnos/agnoforge/internal/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
	_ "github.com/duckdb/duckdb-go/v2" // the "duckdb" database/sql driver
)

// The storage side of a Build: what SaveBuild writes, what Segments and
// Quality read back, and the one read this package makes of acquisition's own
// table.

var (
	hour     = acq.Range{Start: start, End: start.Add(time.Hour)}
	baseSrc  = domain.Source{Instrument: "BTC/USD", Provider: "binance", Symbol: acq.Symbol("BTCUSDT"), Timeframe: domain.TF1m}
	builtAt  = made.Add(time.Hour)
	segments = []domain.Segment{{Kind: domain.SegmentBase, Source: baseSrc, Range: hour}}
)

// builtQuality is the Quality of a build over the hour, with one open gap.
func builtQuality() domain.Quality {
	return domain.Quality{
		RequestedStart: start,
		RequestedEnd:   domain.NowEnd(),
		ResolvedEnd:    hour.End,
		AvailableStart: hour.Start,
		AvailableEnd:   hour.End,
		ExpectedBars:   60,
		ActualBars:     55,
		OpenGaps: []domain.Gap{{ID: 7, Range: acq.Range{
			Start: start.Add(10 * time.Minute), End: start.Add(15 * time.Minute)}}},
		Mode:        domain.ModeResearch,
		LastBuildAt: builtAt,
	}
}

// built is the declaration as a successful Build leaves it.
func built(name string) domain.Dataset {
	d := declaration(name)
	d.State = domain.StateReady
	d.ResolvedEnd = hour.End
	d.UpdatedAt = builtAt
	return d
}

func TestSaveBuildRoundTripsTheSegmentsAndTheQuality(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	if err := store.SaveBuild(ctx, built("btc-usd"), segments, builtQuality()); err != nil {
		t.Fatalf("SaveBuild: %v", err)
	}

	d, err := store.Dataset(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Dataset: %v", err)
	}
	if d.State != domain.StateReady || !d.ResolvedEnd.Equal(hour.End) {
		t.Errorf("dataset = %q with resolved end %s, want ready at %s", d.State, d.ResolvedEnd, hour.End)
	}

	gotSegments, err := store.Segments(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if len(gotSegments) != 1 {
		t.Fatalf("segments = %+v, want one", gotSegments)
	}
	if gotSegments[0].Kind != domain.SegmentBase || gotSegments[0].Source != baseSrc ||
		!gotSegments[0].Range.Start.Equal(hour.Start) || !gotSegments[0].Range.End.Equal(hour.End) {
		t.Errorf("segment = %+v, want %+v", gotSegments[0], segments[0])
	}

	q, ok, err := store.Quality(ctx, "btc-usd")
	if err != nil || !ok {
		t.Fatalf("Quality = %v, %v, want the stored one", ok, err)
	}
	want := builtQuality()
	if q.ExpectedBars != want.ExpectedBars || q.ActualBars != want.ActualBars {
		t.Errorf("bars = %d of %d, want %d of %d", q.ActualBars, q.ExpectedBars, want.ActualBars, want.ExpectedBars)
	}
	if !q.RequestedEnd.Now {
		t.Errorf("requested end = %s, want the declaration's now", q.RequestedEnd)
	}
	if !q.ResolvedEnd.Equal(want.ResolvedEnd) || !q.AvailableStart.Equal(want.AvailableStart) ||
		!q.AvailableEnd.Equal(want.AvailableEnd) {
		t.Errorf("quality range = %s / %s .. %s, want %s / %s .. %s",
			q.ResolvedEnd, q.AvailableStart, q.AvailableEnd,
			want.ResolvedEnd, want.AvailableStart, want.AvailableEnd)
	}
	if q.Mode != domain.ModeResearch || q.Strict() {
		t.Errorf("mode = %q / strict %v, want research / false", q.Mode, q.Strict())
	}
	if !q.LastBuildAt.Equal(builtAt) {
		t.Errorf("last build at = %s, want %s", q.LastBuildAt, builtAt)
	}
	if len(q.OpenGaps) != 1 || q.OpenGaps[0].ID != 7 ||
		!q.OpenGaps[0].Range.Start.Equal(want.OpenGaps[0].Range.Start) {
		t.Errorf("open gaps = %+v, want %+v", q.OpenGaps, want.OpenGaps)
	}
}

func TestSaveBuildReplacesWhatTheLastBuildLeft(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	if err := store.SaveBuild(ctx, built("btc-usd"), segments, builtQuality()); err != nil {
		t.Fatalf("first SaveBuild: %v", err)
	}

	half := acq.Range{Start: start, End: start.Add(30 * time.Minute)}
	next := builtQuality()
	next.AvailableEnd, next.OpenGaps = half.End, nil
	if err := store.SaveBuild(ctx, built("btc-usd"),
		[]domain.Segment{{Kind: domain.SegmentBase, Source: baseSrc, Range: half}}, next); err != nil {
		t.Fatalf("second SaveBuild: %v", err)
	}

	gotSegments, err := store.Segments(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if len(gotSegments) != 1 || !gotSegments[0].Range.End.Equal(half.End) {
		t.Fatalf("segments = %+v, want only the second build's", gotSegments)
	}
	q, _, err := store.Quality(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Quality: %v", err)
	}
	if len(q.OpenGaps) != 0 {
		t.Fatalf("open gaps = %+v, want the second build's none", q.OpenGaps)
	}
}

func TestSaveBuildOfAnUnknownDatasetIsNotFound(t *testing.T) {
	err := openStore(t).SaveBuild(context.Background(), built("nope"), segments, builtQuality())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("SaveBuild = %v, want ErrNotFound", err)
	}
}

func TestAnUnbuiltDatasetHasNoSegmentsAndNoQuality(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	got, err := store.Segments(ctx, "btc-usd")
	if err != nil || len(got) != 0 {
		t.Errorf("Segments = %+v, %v, want none and no error", got, err)
	}
	if _, ok, err := store.Quality(ctx, "btc-usd"); ok || err != nil {
		t.Errorf("Quality = %v, %v, want no quality and no error", ok, err)
	}
}

func TestDeleteRemovesWhatABuildDerived(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	if err := store.SaveBuild(ctx, built("btc-usd"), segments, builtQuality()); err != nil {
		t.Fatalf("SaveBuild: %v", err)
	}
	if err := store.DeleteDataset(ctx, "btc-usd"); err != nil {
		t.Fatalf("DeleteDataset: %v", err)
	}
	// A dataset declared again under the same name starts with no provenance
	// at all: the old build's rows went with the old declaration.
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset again: %v", err)
	}
	if got, err := store.Segments(ctx, "btc-usd"); err != nil || len(got) != 0 {
		t.Errorf("Segments after delete = %+v, %v, want none", got, err)
	}
	if _, ok, err := store.Quality(ctx, "btc-usd"); ok || err != nil {
		t.Errorf("Quality after delete = %v, %v, want none", ok, err)
	}
}

func TestAFailedBuildKeepsItsError(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	failed := built("btc-usd")
	failed.State = domain.StateFailed
	failed.LastError = "strict mode refuses the range: 1 open gap"
	if err := store.SaveBuild(ctx, failed, nil, builtQuality()); err != nil {
		t.Fatalf("SaveBuild: %v", err)
	}
	d, err := store.Dataset(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Dataset: %v", err)
	}
	if d.State != domain.StateFailed || d.LastError != failed.LastError {
		t.Fatalf("dataset = %q with error %q, want failed with the error preserved", d.State, d.LastError)
	}
}

// TestSourceBarsReadsAcquisitionsTable is the data plane: the composite store
// counts the source bars in the same file, without a bar ever crossing the
// acquisition port (ADR-0005).
func TestSourceBarsReadsAcquisitionsTable(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	source, err := acquisitionduckdb.Open(path)
	if err != nil {
		t.Fatalf("opening the acquisition store: %v", err)
	}
	t.Cleanup(func() { source.Close() })

	id := acq.DatasetID{Provider: "binance", Symbol: "BTCUSDT", Timeframe: acq.TF1m}
	var bars []acq.Bar
	for i := 0; i < 30; i++ {
		bars = append(bars, acq.Bar{
			OpenTime: start.Add(time.Duration(i) * time.Minute),
			Open:     "100.00000000", High: "101.00000000",
			Low: "99.00000000", Close: "100.50000000", Volume: "1.00000000",
		})
	}
	if err := source.UpsertBars(ctx, id, bars); err != nil {
		t.Fatalf("seeding bars: %v", err)
	}

	store := openStoreAt(t, path)
	got, err := store.SourceBars(ctx, baseSrc, hour)
	if err != nil {
		t.Fatalf("SourceBars: %v", err)
	}
	if got.Count != 30 {
		t.Errorf("count = %d, want the 30 seeded bars", got.Count)
	}
	if !got.First.Equal(start) || !got.Last.Equal(start.Add(29*time.Minute)) {
		t.Errorf("first/last = %s / %s, want %s / %s", got.First, got.Last, start, start.Add(29*time.Minute))
	}

	// The range is half-open on open_time, and a source nobody wrote is empty
	// rather than an error.
	clipped, err := store.SourceBars(ctx, baseSrc, acq.Range{Start: start, End: start.Add(10 * time.Minute)})
	if err != nil || clipped.Count != 10 {
		t.Errorf("SourceBars over ten minutes = %d, %v, want 10 and no error", clipped.Count, err)
	}
	other := baseSrc
	other.Symbol = "ETHUSDT"
	empty, err := store.SourceBars(ctx, other, hour)
	if err != nil || empty.Count != 0 || !empty.First.IsZero() {
		t.Errorf("SourceBars of an unwritten source = %+v, %v, want nothing and no error", empty, err)
	}
}

// TestOpenBringsAnEarlierDatabaseUpToDate proves the schema is applied to a
// database that predates the build columns rather than refused: the columns
// are added, and what was already declared survives.
func TestOpenBringsAnEarlierDatabaseUpToDate(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)

	db, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("opening the database directly: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE composite_datasets (
			name               VARCHAR PRIMARY KEY,
			instrument         VARCHAR NOT NULL,
			base_provider      VARCHAR NOT NULL,
			base_symbol        VARCHAR NOT NULL,
			base_timeframe     VARCHAR NOT NULL,
			catch_up_kind      VARCHAR NOT NULL,
			catch_up_provider  VARCHAR NOT NULL,
			catch_up_symbol    VARCHAR NOT NULL,
			catch_up_timeframe VARCHAR NOT NULL,
			requested_start_ms BIGINT  NOT NULL,
			requested_end_ms   BIGINT,
			timeframes         VARCHAR NOT NULL,
			mode               VARCHAR NOT NULL,
			state              VARCHAR NOT NULL,
			created_at_ms      BIGINT  NOT NULL,
			updated_at_ms      BIGINT  NOT NULL
		);
		INSERT INTO composite_datasets VALUES
			('btc-usd','BTC/USD','binance','BTCUSDT','1m','base','','','1m',
			 1704067200000, 1706745600000, '5m,1h,1M', 'strict', 'draft',
			 1787140800000, 1787140800000);`); err != nil {
		t.Fatalf("creating the earlier schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	store := openStoreAt(t, path)
	d, err := store.Dataset(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Dataset from an upgraded database: %v", err)
	}
	if d.State != domain.StateDraft || d.LastError != "" || !d.ResolvedEnd.IsZero() {
		t.Errorf("dataset = %+v, want the declaration it was, with no build on it", d)
	}
	// And it can be built from here.
	if err := store.SaveBuild(ctx, built("btc-usd"), segments, builtQuality()); err != nil {
		t.Fatalf("SaveBuild against an upgraded database: %v", err)
	}
}
