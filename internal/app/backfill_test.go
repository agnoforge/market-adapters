package app_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/domain"
)

// origin is the arbitrary UTC instant every test range starts from.
var origin = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

const tf = domain.TF1m

// step is one Bar at the Timeframe these tests use.
var step = tf.Duration()

// at returns the open_time of the i-th Bar after origin.
func at(i int) time.Time { return origin.Add(time.Duration(i) * step) }

// rng returns the half-open range from the i-th to the j-th Bar boundary.
func rng(i, j int) domain.Range { return domain.Range{Start: at(i), End: at(j)} }

// newStore opens an in-memory DuckDB Store — the real Store these tests run
// against — and closes it when the test ends.
func newStore(t *testing.T) *duckdb.Store {
	t.Helper()
	store, err := duckdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

// newService builds the Service under test with a silent logger.
func newService(store app.Store, providers ...app.Provider) *app.Service {
	return app.New(store, providers, app.WithLogger(slog.New(slog.DiscardHandler)))
}

// provider returns a fakeProvider whose whole history starts at origin.
func provider(name string, pages ...fakePage) *fakeProvider {
	return &fakeProvider{
		name:       name,
		timeframes: []domain.Timeframe{tf, domain.TF1h},
		earliest:   origin,
		pages:      pages,
	}
}

// request asks p for [origin, origin+n bars) of BTCUSDT.
func request(name string, n int) app.BackfillRequest {
	return app.BackfillRequest{
		Provider:  name,
		Symbol:    "BTCUSDT",
		Timeframe: tf,
		Range:     rng(0, n),
	}
}

// dataset is the Dataset a request built by request lands in.
func dataset(name string) domain.DatasetID {
	return domain.DatasetID{Provider: name, Symbol: "BTCUSDT", Timeframe: tf}
}

// runToEnd starts the Backfill and waits for its terminal state and terminal
// work to finish.
func runToEnd(t *testing.T, svc *app.Service, req app.BackfillRequest) app.BackfillStatus {
	t.Helper()
	started, err := svc.StartBackfill(context.Background(), req)
	if err != nil {
		t.Fatalf("StartBackfill: %v", err)
	}
	final, ok := svc.Wait(started.ID)
	if !ok {
		t.Fatal("Wait: backfill not found")
	}
	return final
}

func coverageOf(t *testing.T, store *duckdb.Store, ds domain.DatasetID) []domain.Range {
	t.Helper()
	cov, err := store.Coverage(context.Background(), ds)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	return cov
}

func barsIn(t *testing.T, store *duckdb.Store, ds domain.DatasetID, r domain.Range) []domain.Bar {
	t.Helper()
	bars, err := store.Bars(context.Background(), ds, r)
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	return bars
}

func gapsOf(t *testing.T, store *duckdb.Store, ds domain.DatasetID) []domain.Gap {
	t.Helper()
	gaps, err := store.Gaps(context.Background(), ds, app.GapFilter{})
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	return gaps
}

// hexID matches the 16 random bytes a BackfillID is rendered from.
var hexID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestStartBackfillReturnsIDAndEffectiveRange(t *testing.T) {
	store := newStore(t)
	// The Provider's history starts at the 60th Bar, so the effective range
	// is clipped up to it.
	p := provider("fake", fakePage{bars: testBars(at(60), tf, 10)})
	p.earliest = at(60)
	svc := newService(store, p)

	got, err := svc.StartBackfill(context.Background(), request("fake", 120))
	if err != nil {
		t.Fatalf("StartBackfill: %v", err)
	}

	if !hexID.MatchString(string(got.ID)) {
		t.Errorf("id = %q, want 32 hex characters", got.ID)
	}
	want := rng(60, 120)
	if got.Range != want {
		t.Errorf("effective range = %s, want %s", got.Range, want)
	}
	if got.Dataset != dataset("fake") {
		t.Errorf("dataset = %s, want %s", got.Dataset, dataset("fake"))
	}
	if got.State != app.StateRunning {
		t.Errorf("state = %q, want %q", got.State, app.StateRunning)
	}

	final, ok := svc.Wait(got.ID)
	if !ok {
		t.Fatal("Wait: backfill not found")
	}
	if final.State != app.StateCompleted {
		t.Fatalf("final state = %q (%s), want %q", final.State, final.LastError, app.StateCompleted)
	}
	// The effective range, not the requested one, is what the Provider is
	// asked for.
	if r := p.requestedRange(); r != want {
		t.Errorf("Bars called with %s, want %s", r, want)
	}
	if final.BarsDownloaded != 10 {
		t.Errorf("bars_downloaded = %d, want 10", final.BarsDownloaded)
	}
	if !final.Position.Equal(at(69)) {
		t.Errorf("position = %s, want %s", final.Position, at(69))
	}
}

func TestStartBackfillRejectsUnknownProviderAndTimeframe(t *testing.T) {
	svc := newService(newStore(t), provider("fake"))

	if _, err := svc.StartBackfill(context.Background(), request("nope", 10)); !errors.Is(err, app.ErrUnknownProvider) {
		t.Errorf("unknown provider: err = %v, want ErrUnknownProvider", err)
	}

	req := request("fake", 10)
	req.Timeframe = domain.TF3d // canonical, but this Provider does not offer it
	if _, err := svc.StartBackfill(context.Background(), req); !errors.Is(err, domain.ErrUnsupportedTimeframe) {
		t.Errorf("unsupported timeframe: err = %v, want ErrUnsupportedTimeframe", err)
	}
}

func TestStartBackfillUnknownSymbolFailsWithoutRegistering(t *testing.T) {
	p := provider("fake", fakePage{bars: testBars(at(0), tf, 5)})
	// Unknown on the first ask only, so the second Start can prove the first
	// left no running Backfill behind.
	p.earliestErr = func(call int) error {
		if call == 1 {
			return fmt.Errorf("probe: %w", domain.ErrUnknownSymbol)
		}
		return nil
	}
	svc := newService(newStore(t), p)

	got, err := svc.StartBackfill(context.Background(), request("fake", 5))
	if !errors.Is(err, domain.ErrUnknownSymbol) {
		t.Fatalf("err = %v, want ErrUnknownSymbol", err)
	}
	if got.ID != "" {
		t.Errorf("id = %q, want none: nothing was started", got.ID)
	}

	// Nothing was registered: the Dataset is free.
	if _, err := svc.StartBackfill(context.Background(), request("fake", 5)); err != nil {
		t.Fatalf("second StartBackfill: %v, want success (no registry entry from the first)", err)
	}
	if calls := p.earliestCalls(); calls != 2 {
		t.Errorf("EarliestAvailable calls = %d, want 2", calls)
	}
}

func TestSecondStartOnRunningDatasetConflicts(t *testing.T) {
	release := make(chan struct{})
	p := provider("fake", fakePage{bars: testBars(at(0), tf, 5)})
	p.onPage = func(i int) { <-release }
	svc := newService(newStore(t), p)

	first, err := svc.StartBackfill(context.Background(), request("fake", 5))
	if err != nil {
		t.Fatalf("first StartBackfill: %v", err)
	}
	if _, err := svc.StartBackfill(context.Background(), request("fake", 5)); !errors.Is(err, domain.ErrBackfillRunning) {
		t.Fatalf("second StartBackfill: err = %v, want ErrBackfillRunning", err)
	}

	close(release)
	if final, _ := svc.Wait(first.ID); final.State != app.StateCompleted {
		t.Fatalf("state = %q, want %q", final.State, app.StateCompleted)
	}
	// The Dataset is free again once the Backfill is no longer running.
	if _, err := svc.StartBackfill(context.Background(), request("fake", 5)); err != nil {
		t.Errorf("StartBackfill after completion: %v", err)
	}
}

func TestDifferentDatasetsRunConcurrently(t *testing.T) {
	inA, inB := make(chan struct{}), make(chan struct{})
	// Each Backfill announces that it is inside the Provider and then waits
	// for the other: they can only both get past this if they overlap.
	meet := func(mine, theirs chan struct{}) func(int) {
		return func(i int) {
			if i != 0 {
				return
			}
			close(mine)
			select {
			case <-theirs:
			case <-time.After(10 * time.Second):
				t.Error("the two Backfills never overlapped")
			}
		}
	}
	a := provider("fakea", fakePage{bars: testBars(at(0), tf, 5)})
	a.onPage = meet(inA, inB)
	b := provider("fakeb", fakePage{bars: testBars(at(0), tf, 5)})
	b.onPage = meet(inB, inA)
	svc := newService(newStore(t), a, b)

	startedA, err := svc.StartBackfill(context.Background(), request("fakea", 5))
	if err != nil {
		t.Fatalf("StartBackfill a: %v", err)
	}
	startedB, err := svc.StartBackfill(context.Background(), request("fakeb", 5))
	if err != nil {
		t.Fatalf("StartBackfill b: %v", err)
	}
	for _, started := range []app.BackfillStatus{startedA, startedB} {
		if final, _ := svc.Wait(started.ID); final.State != app.StateCompleted {
			t.Errorf("%s: state = %q (%s), want %q", final.Dataset, final.State, final.LastError, app.StateCompleted)
		}
	}
}

func TestEachPageIsPersistedAndCoveredBeforeTheNextIsRequested(t *testing.T) {
	store := newStore(t)
	ds := dataset("fake")
	const perPage, pages = 5, 3
	p := provider("fake")
	for i := range pages {
		p.pages = append(p.pages, fakePage{bars: testBars(at(i*perPage), tf, perPage)})
	}
	// Before page i is asked for, every earlier page must already be in the
	// Store, with its Coverage.
	p.onPage = func(i int) {
		if bars := barsIn(t, store, ds, rng(0, perPage*pages)); len(bars) != i*perPage {
			t.Errorf("before page %d: %d bars stored, want %d", i, len(bars), i*perPage)
		}
		want := []domain.Range{rng(0, i*perPage)}
		if i == 0 {
			want = nil
		}
		if cov := coverageOf(t, store, ds); !slices.Equal(cov, want) {
			t.Errorf("before page %d: coverage = %v, want %v", i, cov, want)
		}
	}
	svc := newService(store, p)

	final := runToEnd(t, svc, request("fake", perPage*pages))
	if final.State != app.StateCompleted {
		t.Fatalf("state = %q (%s), want %q", final.State, final.LastError, app.StateCompleted)
	}
	if final.BarsDownloaded != perPage*pages {
		t.Errorf("bars_downloaded = %d, want %d", final.BarsDownloaded, perPage*pages)
	}
}

func TestCancelLeavesLandedPagesAndCoverage(t *testing.T) {
	store := newStore(t)
	ds := dataset("fake")
	arrived, resume := make(chan struct{}), make(chan struct{})
	p := provider("fake",
		fakePage{bars: testBars(at(0), tf, 5)},
		fakePage{bars: testBars(at(5), tf, 5)},
	)
	p.onPage = func(i int) {
		if i == 1 { // page 0 has landed by now
			close(arrived)
			<-resume
		}
	}
	svc := newService(store, p)

	started, err := svc.StartBackfill(context.Background(), request("fake", 10))
	if err != nil {
		t.Fatalf("StartBackfill: %v", err)
	}
	<-arrived
	if err := svc.CancelBackfill(started.ID); err != nil {
		t.Fatalf("CancelBackfill: %v", err)
	}
	close(resume)

	final, _ := svc.Wait(started.ID)
	if final.State != app.StateCancelled {
		t.Fatalf("state = %q (%s), want %q", final.State, final.LastError, app.StateCancelled)
	}
	if final.BarsDownloaded != 5 {
		t.Errorf("bars_downloaded = %d, want 5", final.BarsDownloaded)
	}
	if bars := barsIn(t, store, ds, rng(0, 10)); len(bars) != 5 {
		t.Errorf("%d bars stored, want the 5 that landed", len(bars))
	}
	if cov := coverageOf(t, store, ds); !slices.Equal(cov, []domain.Range{rng(0, 5)}) {
		t.Errorf("coverage = %v, want %v", cov, []domain.Range{rng(0, 5)})
	}
}

func TestCancelUnknownBackfill(t *testing.T) {
	svc := newService(newStore(t))
	if err := svc.CancelBackfill("nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestProviderErrorMidRunFails(t *testing.T) {
	store := newStore(t)
	ds := dataset("fake")
	boom := errors.New("provider exploded")
	p := provider("fake",
		fakePage{bars: testBars(at(0), tf, 5)},
		fakePage{err: boom},
		fakePage{bars: testBars(at(5), tf, 5)}, // never reached
	)
	svc := newService(store, p)

	final := runToEnd(t, svc, request("fake", 10))
	if final.State != app.StateFailed {
		t.Fatalf("state = %q, want %q", final.State, app.StateFailed)
	}
	if !strings.Contains(final.LastError, boom.Error()) {
		t.Errorf("last_error = %q, want it to mention %q", final.LastError, boom)
	}
	if final.BarsDownloaded != 5 {
		t.Errorf("bars_downloaded = %d, want 5", final.BarsDownloaded)
	}
	// Coverage reflects the landed page only, never the range that was asked
	// for.
	if cov := coverageOf(t, store, ds); !slices.Equal(cov, []domain.Range{rng(0, 5)}) {
		t.Errorf("coverage = %v, want %v", cov, []domain.Range{rng(0, 5)})
	}
	if bars := barsIn(t, store, ds, rng(0, 10)); len(bars) != 5 {
		t.Errorf("%d bars stored, want 5", len(bars))
	}
}

// upsertFailingStore is the real Store with the n-th UpsertBars broken, which
// is how a test reaches the case where a page never lands.
type upsertFailingStore struct {
	app.Store

	failOn int
	mu     sync.Mutex
	calls  int
}

func (s *upsertFailingStore) UpsertBars(ctx context.Context, id domain.DatasetID, bars []domain.Bar) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == s.failOn {
		return errors.New("disk on fire")
	}
	return s.Store.UpsertBars(ctx, id, bars)
}

func TestCoverageIsNotExtendedForAPageThatDidNotLand(t *testing.T) {
	store := newStore(t)
	ds := dataset("fake")
	p := provider("fake",
		fakePage{bars: testBars(at(0), tf, 5)},
		fakePage{bars: testBars(at(5), tf, 5)},
	)
	svc := newService(&upsertFailingStore{Store: store, failOn: 2}, p)

	final := runToEnd(t, svc, request("fake", 10))
	if final.State != app.StateFailed {
		t.Fatalf("state = %q, want %q", final.State, app.StateFailed)
	}
	if !strings.Contains(final.LastError, "disk on fire") {
		t.Errorf("last_error = %q, want it to mention the write failure", final.LastError)
	}
	if final.BarsDownloaded != 5 {
		t.Errorf("bars_downloaded = %d, want 5", final.BarsDownloaded)
	}
	if cov := coverageOf(t, store, ds); !slices.Equal(cov, []domain.Range{rng(0, 5)}) {
		t.Errorf("coverage = %v, want only the page that landed %v", cov, []domain.Range{rng(0, 5)})
	}
}

func TestCompletedBackfillCoversTheWholeEffectiveRange(t *testing.T) {
	store := newStore(t)
	ds := dataset("fake")
	// The Provider has no Bar for the last five minutes at all.
	p := provider("fake", fakePage{bars: testBars(at(0), tf, 5)})
	svc := newService(store, p)

	if final := runToEnd(t, svc, request("fake", 10)); final.State != app.StateCompleted {
		t.Fatalf("state = %q (%s), want %q", final.State, final.LastError, app.StateCompleted)
	}
	if cov := coverageOf(t, store, ds); !slices.Equal(cov, []domain.Range{rng(0, 10)}) {
		t.Errorf("coverage = %v, want the full effective range %v", cov, []domain.Range{rng(0, 10)})
	}
}

// recordingStore is the real Store with one thing added: it remembers the
// range every gap replacement was made over. That range is the landed range
// DetectGaps ran on, which is otherwise indistinguishable from the effective
// range in the Store's contents.
type recordingStore struct {
	app.Store

	mu       sync.Mutex
	replaced []domain.Range
}

func (s *recordingStore) ReplaceOpenGaps(ctx context.Context, id domain.DatasetID, r domain.Range, gaps []domain.Gap) error {
	s.mu.Lock()
	s.replaced = append(s.replaced, r)
	s.mu.Unlock()
	return s.Store.ReplaceOpenGaps(ctx, id, r, gaps)
}

// detectedOver returns the ranges gap detection ran over, in order.
func (s *recordingStore) detectedOver() []domain.Range {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.replaced)
}

func TestDetectGapsRunsOnEveryTerminalState(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		store := newStore(t)
		ds := dataset("fake")
		// Bars 10..14 are missing from the middle of the requested range.
		p := provider("fake",
			fakePage{bars: testBars(at(0), tf, 10)},
			fakePage{bars: testBars(at(15), tf, 5)},
		)
		rec := &recordingStore{Store: store}
		svc := newService(rec, p)

		if final := runToEnd(t, svc, request("fake", 20)); final.State != app.StateCompleted {
			t.Fatalf("state = %q (%s), want %q", final.State, final.LastError, app.StateCompleted)
		}
		assertGaps(t, gapsOf(t, store, ds), rng(10, 15))
		// A completed Backfill landed the whole effective range.
		assertDetectedOver(t, rec, rng(0, 20))
	})

	t.Run("cancelled", func(t *testing.T) {
		store := newStore(t)
		ds := dataset("fake")
		arrived, resume := make(chan struct{}), make(chan struct{})
		p := provider("fake",
			// Bar 2 is missing from the page that lands.
			fakePage{bars: testBarsAt(origin, tf, 0, 1, 3, 4)},
			fakePage{bars: testBars(at(5), tf, 5)},
		)
		p.onPage = func(i int) {
			if i == 1 {
				close(arrived)
				<-resume
			}
		}
		rec := &recordingStore{Store: store}
		svc := newService(rec, p)

		started, err := svc.StartBackfill(context.Background(), request("fake", 20))
		if err != nil {
			t.Fatalf("StartBackfill: %v", err)
		}
		<-arrived
		if err := svc.CancelBackfill(started.ID); err != nil {
			t.Fatalf("CancelBackfill: %v", err)
		}
		close(resume)

		if final, _ := svc.Wait(started.ID); final.State != app.StateCancelled {
			t.Fatalf("state = %q, want %q", final.State, app.StateCancelled)
		}
		// The landed range is [0,5): the gap inside it is found, and the
		// twenty minutes that were asked for are not judged.
		assertGaps(t, gapsOf(t, store, ds), rng(2, 3))
		assertDetectedOver(t, rec, rng(0, 5))
	})

	t.Run("failed", func(t *testing.T) {
		store := newStore(t)
		ds := dataset("fake")
		p := provider("fake",
			fakePage{bars: testBarsAt(origin, tf, 0, 1, 3, 4)},
			fakePage{err: errors.New("provider exploded")},
		)
		rec := &recordingStore{Store: store}
		svc := newService(rec, p)

		if final := runToEnd(t, svc, request("fake", 20)); final.State != app.StateFailed {
			t.Fatalf("state = %q, want %q", final.State, app.StateFailed)
		}
		assertGaps(t, gapsOf(t, store, ds), rng(2, 3))
		assertDetectedOver(t, rec, rng(0, 5))
	})
}

// assertDetectedOver checks gap detection ran exactly once, over want.
func assertDetectedOver(t *testing.T, rec *recordingStore, want domain.Range) {
	t.Helper()
	if got := rec.detectedOver(); !slices.Equal(got, []domain.Range{want}) {
		t.Errorf("gap detection ran over %v, want %v", got, []domain.Range{want})
	}
}

// assertGaps checks the Dataset's Gaps are exactly the open ranges wanted.
func assertGaps(t *testing.T, gaps []domain.Gap, want ...domain.Range) {
	t.Helper()
	if len(gaps) != len(want) {
		t.Fatalf("%d gaps, want %d: %v", len(gaps), len(want), gaps)
	}
	for i, g := range gaps {
		if g.Range != want[i] {
			t.Errorf("gap %d range = %s, want %s", i, g.Range, want[i])
		}
		if g.Status != domain.GapOpen {
			t.Errorf("gap %d status = %q, want %q", i, g.Status, domain.GapOpen)
		}
	}
}

func TestRerunningTheSameBackfillChangesNothing(t *testing.T) {
	store := newStore(t)
	ds := dataset("fake")
	pages := []fakePage{
		{bars: testBars(at(0), tf, 10)},
		{bars: testBars(at(15), tf, 5)},
	}

	first := runToEnd(t, newService(store, provider("fake", pages...)), request("fake", 20))
	firstBars := len(barsIn(t, store, ds, rng(0, 20)))
	firstCoverage := coverageOf(t, store, ds)
	firstGaps := gapsOf(t, store, ds)

	second := runToEnd(t, newService(store, provider("fake", pages...)), request("fake", 20))

	if second.BarsDownloaded != first.BarsDownloaded {
		t.Errorf("bars_downloaded = %d, want %d", second.BarsDownloaded, first.BarsDownloaded)
	}
	if got := len(barsIn(t, store, ds, rng(0, 20))); got != firstBars {
		t.Errorf("%d bars stored, want %d", got, firstBars)
	}
	if got := coverageOf(t, store, ds); !slices.Equal(got, firstCoverage) {
		t.Errorf("coverage = %v, want %v", got, firstCoverage)
	}
	got := gapsOf(t, store, ds)
	if len(got) != len(firstGaps) {
		t.Fatalf("%d gaps, want %d", len(got), len(firstGaps))
	}
	for i := range got {
		if got[i].Range != firstGaps[i].Range || got[i].Status != firstGaps[i].Status {
			t.Errorf("gap %d = %s %s, want %s %s", i, got[i].Range, got[i].Status, firstGaps[i].Range, firstGaps[i].Status)
		}
	}
}

func TestProvidersLists(t *testing.T) {
	svc := newService(newStore(t), provider("zeta"), provider("alpha"))
	got := svc.Providers()
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Fatalf("providers = %v, want alpha then zeta", got)
	}
	if !slices.Equal(got[0].Timeframes, []domain.Timeframe{tf, domain.TF1h}) {
		t.Errorf("timeframes = %v", got[0].Timeframes)
	}
}

// TestAppImportsNoAdapter is the seam the acceptance criteria rest on: the use
// cases know the ports, the domain, the standard library and the
// vendor-neutral tracing API, and nothing else. The tracing SDK behind that
// API is wiring and stays in cmd (ADR 0003).
func TestAppImportsNoAdapter(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/agnos/agnoforge/internal/app").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	allowed := []string{"github.com/agnos/agnoforge/internal/app", "github.com/agnos/agnoforge/internal/domain"}
	for _, dep := range strings.Fields(string(out)) {
		// A standard library package's first path element carries no dot.
		if !strings.Contains(strings.SplitN(dep, "/", 2)[0], ".") {
			continue
		}
		if strings.HasPrefix(dep, "go.opentelemetry.io/otel/sdk") ||
			strings.HasPrefix(dep, "go.opentelemetry.io/contrib/") {
			t.Errorf("internal/app depends on %s, which belongs to cmd", dep)
			continue
		}
		if slices.Contains(tracingAPI(t), dep) {
			continue
		}
		if !slices.Contains(allowed, dep) {
			t.Errorf("internal/app depends on %s; only the standard library, internal/domain and the OpenTelemetry API are allowed", dep)
		}
	}
}

// tracingAPI is everything the vendor-neutral OpenTelemetry API pulls in,
// computed rather than listed so the guard above stays honest when the API's
// own dependencies change.
func tracingAPI(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps",
		"go.opentelemetry.io/otel",
		"go.opentelemetry.io/otel/trace",
		"go.opentelemetry.io/otel/attribute",
		"go.opentelemetry.io/otel/codes").Output()
	if err != nil {
		t.Fatalf("go list -deps of the tracing API: %v", err)
	}
	return strings.Fields(string(out))
}

// A completed Backfill must not claim Coverage past the last closed Bar: the
// adapter clips its end there, so the app must too, or every not-yet-closed
// minute up to the requested end would be reported as an open Gap.
func TestEffectiveRangeIsClippedToTheLastClosedBar(t *testing.T) {
	store := newStore(t)
	p := provider("fake", fakePage{bars: testBars(at(0), tf, 5)})
	now := at(5).Add(30 * time.Second) // Bar 5 is still forming
	svc := app.New(store, []app.Provider{p}, app.WithLogger(slog.New(slog.DiscardHandler)), app.WithClock(func() time.Time { return now }))

	started, err := svc.StartBackfill(context.Background(), request("fake", 60))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if want := rng(0, 5); started.Range != want {
		t.Fatalf("effective range = %s, want %s", started.Range, want)
	}
	final, _ := svc.Wait(started.ID)
	if final.State != app.StateCompleted {
		t.Fatalf("state = %s", final.State)
	}
	cov, err := store.Coverage(context.Background(), started.Dataset)
	if err != nil || len(cov) != 1 || cov[0] != rng(0, 5) {
		t.Fatalf("coverage = %v (%v), want [%s]", cov, err, rng(0, 5))
	}
	gaps, err := store.Gaps(context.Background(), started.Dataset, app.GapFilter{})
	if err != nil || len(gaps) != 0 {
		t.Fatalf("gaps = %v (%v), want none", gaps, err)
	}

	_, err = svc.StartBackfill(context.Background(), app.BackfillRequest{Provider: "fake", Symbol: "BTCUSDT", Timeframe: tf, Range: rng(5, 60)})
	if !errors.Is(err, app.ErrEmptyRange) {
		t.Fatalf("future-only request err = %v, want ErrEmptyRange", err)
	}
}
