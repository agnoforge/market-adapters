package duckdb_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	acquisitionduckdb "github.com/agnos/agnoforge/internal/adapters/duckdb"
	compositeduckdb "github.com/agnos/agnoforge/internal/composite/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

var (
	start = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end   = time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	made  = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
)

// dbPath is a database file of this test's own, never the repository's.
func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "agnoforge.duckdb")
}

// openStore opens a composite Store on its own file.
func openStore(t *testing.T) *compositeduckdb.Store {
	t.Helper()
	return openStoreAt(t, dbPath(t))
}

func openStoreAt(t *testing.T, path string) *compositeduckdb.Store {
	t.Helper()
	store, err := compositeduckdb.Open(path)
	if err != nil {
		t.Fatalf("opening the composite store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// declaration is the Composite Dataset every test writes.
func declaration(name string) domain.Dataset {
	return domain.Dataset{
		Name: domain.Name(name),
		Config: domain.Config{
			Instrument: "BTC/USD",
			Base: domain.Source{
				Instrument: "BTC/USD", Provider: "binance",
				Symbol: acq.Symbol("BTCUSDT"), Timeframe: domain.TF1m,
			},
			CatchUp:        domain.CatchUp{Kind: domain.CatchUpBase},
			RequestedStart: start,
			RequestedEnd:   domain.FixedEnd(end),
			Timeframes:     []domain.Timeframe{domain.TF5m, domain.TF1h, domain.TF1M},
			Mode:           domain.ModeStrict,
		},
		State:     domain.StateDraft,
		CreatedAt: made,
		UpdatedAt: made,
	}
}

func TestCreateAndReadBackTheWholeDeclaration(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	want := declaration("btc-usd")
	if err := store.CreateDataset(ctx, want); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	got, err := store.Dataset(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Dataset: %v", err)
	}
	assertSame(t, got, want)
}

func TestANowEndSurvivesTheRoundTrip(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	want := declaration("btc-usd-now")
	want.Config.RequestedEnd = domain.NowEnd()
	if err := store.CreateDataset(ctx, want); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	got, err := store.Dataset(ctx, "btc-usd-now")
	if err != nil {
		t.Fatalf("Dataset: %v", err)
	}
	if !got.Config.RequestedEnd.Now {
		t.Fatalf("requested end came back as %s, want now", got.Config.RequestedEnd)
	}
	assertSame(t, got, want)
}

func TestACatchUpSourceSurvivesTheRoundTrip(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	want := declaration("btc-usd-catch-up")
	want.Config.CatchUp = domain.CatchUp{Kind: domain.CatchUpSource, Source: domain.Source{
		Instrument: "BTC/USD", Provider: "coinbase", Symbol: acq.Symbol("BTC-USD"), Timeframe: domain.TF1m,
	}}
	if err := store.CreateDataset(ctx, want); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	got, err := store.Dataset(ctx, want.Name)
	if err != nil {
		t.Fatalf("Dataset: %v", err)
	}
	assertSame(t, got, want)
}

func TestCreateRefusesANameAlreadyTaken(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	other := declaration("btc-usd")
	other.Config.Instrument = "ETH/USD"
	if err := store.CreateDataset(ctx, other); !errors.Is(err, domain.ErrDuplicateName) {
		t.Fatalf("second CreateDataset = %v, want ErrDuplicateName", err)
	}
	// The refused declaration left nothing behind.
	got, err := store.Dataset(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Dataset: %v", err)
	}
	if got.Config.Instrument != "BTC/USD" {
		t.Fatalf("instrument = %q, want the first declaration's BTC/USD", got.Config.Instrument)
	}
}

func TestDatasetsAreOrderedByName(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	for _, name := range []string{"eth-usd", "btc-usd", "sol-usd"} {
		if err := store.CreateDataset(ctx, declaration(name)); err != nil {
			t.Fatalf("CreateDataset(%q): %v", name, err)
		}
	}
	all, err := store.Datasets(ctx)
	if err != nil {
		t.Fatalf("Datasets: %v", err)
	}
	var names []string
	for _, d := range all {
		names = append(names, d.Name.String())
	}
	want := []string{"btc-usd", "eth-usd", "sol-usd"}
	if len(names) != len(want) {
		t.Fatalf("Datasets = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("Datasets = %v, want %v", names, want)
		}
	}
}

func TestDatasetsIsEmptyNotAnErrorOnAFreshDatabase(t *testing.T) {
	all, err := openStore(t).Datasets(context.Background())
	if err != nil {
		t.Fatalf("Datasets: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("Datasets = %v, want none", all)
	}
}

func TestUpdateReplacesTheConfigAndState(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	edited := declaration("btc-usd")
	edited.Config.Timeframes = []domain.Timeframe{domain.TF1d}
	edited.Config.Mode = domain.ModeResearch
	edited.State = domain.StateStale
	edited.UpdatedAt = made.Add(time.Hour)
	if err := store.UpdateDataset(ctx, edited); err != nil {
		t.Fatalf("UpdateDataset: %v", err)
	}
	got, err := store.Dataset(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Dataset: %v", err)
	}
	assertSame(t, got, edited)
}

func TestMissingDatasetsAreNotFound(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if _, err := store.Dataset(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Dataset = %v, want ErrNotFound", err)
	}
	if err := store.UpdateDataset(ctx, declaration("nope")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateDataset = %v, want ErrNotFound", err)
	}
	if err := store.DeleteDataset(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("DeleteDataset = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesTheDeclaration(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	if err := store.DeleteDataset(ctx, "btc-usd"); err != nil {
		t.Fatalf("DeleteDataset: %v", err)
	}
	if _, err := store.Dataset(ctx, "btc-usd"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Dataset after delete = %v, want ErrNotFound", err)
	}
}

// TestTheSchemaIsIdempotentOverTheAcquisitionFile is the storage half of "the
// composite context owns its own DDL, applied on startup against the same
// DuckDB file acquisition uses": both contexts open the same file, in either
// order, twice over, and each still sees its own tables and its own rows.
func TestTheSchemaIsIdempotentOverTheAcquisitionFile(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)

	acquisition, err := acquisitionduckdb.Open(path)
	if err != nil {
		t.Fatalf("opening the acquisition store: %v", err)
	}
	t.Cleanup(func() { acquisition.Close() })

	store := openStoreAt(t, path)
	if err := store.CreateDataset(ctx, declaration("btc-usd")); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}

	// Applying either schema again — the startup path of a second process, or
	// of a restart — changes nothing and loses nothing.
	reopened := openStoreAt(t, path)
	if _, err := acquisitionduckdb.Open(path); err != nil {
		t.Fatalf("re-opening the acquisition store: %v", err)
	}
	got, err := reopened.Dataset(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Dataset after both schemas ran again: %v", err)
	}
	assertSame(t, got, declaration("btc-usd"))

	// Acquisition's own tables are untouched by any of it.
	id := acq.DatasetID{Provider: "binance", Symbol: "BTCUSDT", Timeframe: acq.TF1m}
	if err := acquisition.ExtendCoverage(ctx, id, acq.Range{Start: start, End: end}); err != nil {
		t.Fatalf("acquisition ExtendCoverage: %v", err)
	}
	coverage, err := acquisition.Coverage(ctx, id)
	if err != nil {
		t.Fatalf("acquisition Coverage: %v", err)
	}
	if len(coverage) != 1 {
		t.Fatalf("acquisition coverage = %v, want the one range it was given", coverage)
	}
}

// assertSame compares two declarations field by field, which is the whole
// observable content of a stored one.
func assertSame(t *testing.T, got, want domain.Dataset) {
	t.Helper()
	if got.Name != want.Name {
		t.Errorf("name = %q, want %q", got.Name, want.Name)
	}
	if got.State != want.State {
		t.Errorf("state = %q, want %q", got.State, want.State)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("created at = %s, want %s", got.CreatedAt, want.CreatedAt)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("updated at = %s, want %s", got.UpdatedAt, want.UpdatedAt)
	}
	g, w := got.Config, want.Config
	if g.Instrument != w.Instrument {
		t.Errorf("instrument = %q, want %q", g.Instrument, w.Instrument)
	}
	if g.Base != w.Base {
		t.Errorf("base = %#v, want %#v", g.Base, w.Base)
	}
	if g.CatchUp != w.CatchUp {
		t.Errorf("catch-up = %#v, want %#v", g.CatchUp, w.CatchUp)
	}
	if !g.RequestedStart.Equal(w.RequestedStart) {
		t.Errorf("requested start = %s, want %s", g.RequestedStart, w.RequestedStart)
	}
	if g.RequestedEnd.String() != w.RequestedEnd.String() {
		t.Errorf("requested end = %s, want %s", g.RequestedEnd, w.RequestedEnd)
	}
	if g.Mode != w.Mode {
		t.Errorf("mode = %q, want %q", g.Mode, w.Mode)
	}
	if len(g.Timeframes) != len(w.Timeframes) {
		t.Fatalf("timeframes = %v, want %v", g.Timeframes, w.Timeframes)
	}
	for i := range w.Timeframes {
		if g.Timeframes[i] != w.Timeframes[i] {
			t.Fatalf("timeframes = %v, want %v", g.Timeframes, w.Timeframes)
		}
	}
}
