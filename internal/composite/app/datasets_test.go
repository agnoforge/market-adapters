package app_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// fakeStore is the Store port with nothing behind it but a map, so a test can
// say what the store did without a database in the way.
type fakeStore struct {
	datasets map[domain.Name]domain.Dataset
	writes   int
}

func newFakeStore() *fakeStore {
	return &fakeStore{datasets: map[domain.Name]domain.Dataset{}}
}

func (f *fakeStore) CreateDataset(_ context.Context, d domain.Dataset) error {
	f.writes++
	if _, taken := f.datasets[d.Name]; taken {
		return domain.ErrDuplicateName
	}
	f.datasets[d.Name] = d
	return nil
}

func (f *fakeStore) Dataset(_ context.Context, name domain.Name) (domain.Dataset, error) {
	d, ok := f.datasets[name]
	if !ok {
		return domain.Dataset{}, domain.ErrNotFound
	}
	return d, nil
}

func (f *fakeStore) Datasets(context.Context) ([]domain.Dataset, error) {
	out := make([]domain.Dataset, 0, len(f.datasets))
	for _, d := range f.datasets {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeStore) UpdateDataset(_ context.Context, d domain.Dataset) error {
	f.writes++
	if _, ok := f.datasets[d.Name]; !ok {
		return domain.ErrNotFound
	}
	f.datasets[d.Name] = d
	return nil
}

func (f *fakeStore) DeleteDataset(_ context.Context, name domain.Name) error {
	f.writes++
	if _, ok := f.datasets[name]; !ok {
		return domain.ErrNotFound
	}
	delete(f.datasets, name)
	return nil
}

// The build side of the Store port. These use cases never reach it — the
// declaration ones this file covers do not build anything — so it is here to
// satisfy the port and nothing else.

func (f *fakeStore) SaveBuild(_ context.Context, d domain.Dataset, _ []domain.Segment, _ domain.Quality) error {
	f.writes++
	if _, ok := f.datasets[d.Name]; !ok {
		return domain.ErrNotFound
	}
	f.datasets[d.Name] = d
	return nil
}

func (f *fakeStore) Materialize(context.Context, domain.Name, app.Materialization) (int64, error) {
	return 0, nil
}

func (f *fakeStore) Segments(context.Context, domain.Name) ([]domain.Segment, error) {
	return nil, nil
}

// Bars and ExportBars answer nothing: a bar is a row in the database, and these
// tests are about the declaration use cases, which never read one.
func (f *fakeStore) Bars(context.Context, app.BarQuery) ([]acq.Bar, error) {
	return nil, nil
}

func (f *fakeStore) ExportBars(context.Context, app.BarQuery, io.Writer) error {
	return nil
}

func (f *fakeStore) Quality(context.Context, domain.Name) (domain.Quality, bool, error) {
	return domain.Quality{}, false, nil
}

func (f *fakeStore) SourceBars(context.Context, domain.Source, acq.Range) (app.SourceBars, error) {
	return app.SourceBars{}, nil
}

// TransitionDelta prices nothing: these tests never assemble a timeline, and a
// price is a fact in acquisition's bars rather than anything a map can hold.
func (f *fakeStore) TransitionDelta(context.Context, domain.Source, domain.Source, time.Time) (app.PriceDelta, error) {
	return app.PriceDelta{}, nil
}

var clock = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

// newService builds the declaration use cases. They never ask acquisition
// anything, so there is no port behind them here.
func newService(store app.Store, now *time.Time) *app.Service {
	return app.New(store, nil, app.WithClock(func() time.Time { return *now }))
}

func config() domain.Config {
	return domain.Config{
		Instrument: "BTC/USD",
		Base: domain.Source{
			Instrument: "BTC/USD", Provider: "binance",
			Symbol: acq.Symbol("BTCUSDT"), Timeframe: domain.TF1m,
		},
		CatchUp:        domain.CatchUp{Kind: domain.CatchUpBase},
		RequestedStart: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		RequestedEnd:   domain.NowEnd(),
		Timeframes:     []domain.Timeframe{domain.TF1h},
		Mode:           domain.ModeStrict,
	}
}

// A refused declaration never reaches the store: validation happens before
// persistence, so nothing half-declared can be left behind.
func TestCreateValidatesBeforeItWrites(t *testing.T) {
	store, now := newFakeStore(), clock
	svc := newService(store, &now)

	bad := config()
	bad.Base.Instrument = "ETH/USD"
	if _, err := svc.Create(context.Background(), "btc-usd", bad); !errors.Is(err, domain.ErrInvalidConfig) {
		t.Fatalf("Create = %v, want ErrInvalidConfig", err)
	}
	if store.writes != 0 {
		t.Fatalf("the store was written %d times, want none", store.writes)
	}
}

// A name that is not a slug is refused before the store is asked anything.
func TestCreateRefusesANameThatIsNotASlug(t *testing.T) {
	store, now := newFakeStore(), clock
	svc := newService(store, &now)
	if _, err := svc.Create(context.Background(), "BTC_USD", config()); !errors.Is(err, domain.ErrInvalidConfig) {
		t.Fatalf("Create = %v, want ErrInvalidConfig", err)
	}
	if store.writes != 0 {
		t.Fatalf("the store was written %d times, want none", store.writes)
	}
}

// Editing keeps the identity and the moment of declaration, and moves only
// what an edit can move: the config, the state, and when it was last touched.
func TestEditKeepsIdentityAndCreationAndStalesABuiltDataset(t *testing.T) {
	store, now := newFakeStore(), clock
	svc := newService(store, &now)
	ctx := context.Background()

	created, err := svc.Create(ctx, "btc-usd", config())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Stand in for a Build having happened.
	built := created
	built.State = domain.StateReady
	if err := store.UpdateDataset(ctx, built); err != nil {
		t.Fatalf("UpdateDataset: %v", err)
	}

	now = clock.Add(time.Hour)
	edited := config()
	edited.Mode = domain.ModeResearch
	got, err := svc.Edit(ctx, "btc-usd", edited)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if got.State != domain.StateStale {
		t.Errorf("state = %q, want stale", got.State)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("created at = %s, want the moment of declaration %s", got.CreatedAt, created.CreatedAt)
	}
	if !got.UpdatedAt.Equal(clock.Add(time.Hour)) {
		t.Errorf("updated at = %s, want the moment of the edit", got.UpdatedAt)
	}
	if got.Config.Mode != domain.ModeResearch {
		t.Errorf("mode = %q, want research", got.Config.Mode)
	}
}

// An invalid edit leaves the stored declaration exactly as it was.
func TestEditValidatesBeforeItWrites(t *testing.T) {
	store, now := newFakeStore(), clock
	svc := newService(store, &now)
	ctx := context.Background()
	if _, err := svc.Create(ctx, "btc-usd", config()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	writes := store.writes

	bad := config()
	bad.Timeframes = []domain.Timeframe{"2m"}
	if _, err := svc.Edit(ctx, "btc-usd", bad); !errors.Is(err, domain.ErrInvalidConfig) {
		t.Fatalf("Edit = %v, want ErrInvalidConfig", err)
	}
	if store.writes != writes {
		t.Fatalf("the store was written again, want it untouched")
	}
}

// Every use case that names a dataset reports one that does not exist the
// same way.
func TestUnknownDatasetsAreNotFound(t *testing.T) {
	store, now := newFakeStore(), clock
	svc := newService(store, &now)
	ctx := context.Background()
	if _, err := svc.Dataset(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Dataset = %v, want ErrNotFound", err)
	}
	if _, err := svc.Edit(ctx, "nope", config()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Edit = %v, want ErrNotFound", err)
	}
	if _, err := svc.Delete(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete = %v, want ErrNotFound", err)
	}
}

// Delete answers with the declaration it removed, so a caller can see what
// went.
func TestDeleteAnswersTheDeclarationItRemoved(t *testing.T) {
	store, now := newFakeStore(), clock
	svc := newService(store, &now)
	ctx := context.Background()
	created, err := svc.Create(ctx, "btc-usd", config())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	deleted, err := svc.Delete(ctx, "btc-usd")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleted.Name != created.Name || deleted.Config.Instrument != created.Config.Instrument {
		t.Errorf("Delete = %+v, want the declaration it removed", deleted)
	}
	if len(store.datasets) != 0 {
		t.Errorf("the store still holds %d declarations", len(store.datasets))
	}
}
