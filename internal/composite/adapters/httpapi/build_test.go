package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	compositeapp "github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// --- the fake AcquisitionPort -----------------------------------------------

// fakePort is the control plane a test scripts: what the base source is
// covered over, which open Gaps acquisition would report inside it, and what
// the provider behind it can still serve. It answers completeness the way
// acquisition does — a range is complete when it lies inside coverage and no
// open Gap intersects it — and it admits backfills the way acquisition does:
// one at a time per source Dataset, over the range clipped to what the provider
// has.
//
// It has no way to return a bar, because the port it implements has no method
// that returns one: source bars are read from the database, never carried
// across this boundary (decision 33).
type fakePort struct {
	mu sync.Mutex
	// sources is what acquisition knows per source Dataset. A test that never
	// names one is talking about the base source, which is what every
	// single-provider test does; a cross-provider test names the catch-up
	// source too, and the two answer independently — which is the whole point
	// of a second provider.
	sources map[domain.Source]*fakeSource
	// busy is how many more backfill requests are refused the way acquisition
	// refuses a second backfill of one source Dataset.
	busy int
	// failure is the terminal acquisition failure a started backfill ends with.
	failure error
	// landing is what each started backfill will make covered, and repairing is
	// the Gap a started repair is aimed at.
	landing   map[compositeapp.BackfillHandle]request
	repairing map[compositeapp.BackfillHandle]domain.Gap
	handles   int
	// attempts counts every backfill request, admitted or refused; requested
	// records the source and range each one was asked over, and started only
	// the ones acquisition admitted.
	attempts  int
	requested []request
	started   []request
	// fill is what the harness does when a backfill's bars land: write the rows
	// into acquisition's own table, because no port carries a bar.
	fill func(domain.Source, acq.Range)
	// entered receives once per Coverage call, and held is what a held
	// Coverage call waits for. Together they let a test park one Build inside
	// the service while it starts a second.
	entered chan struct{}
	held    chan struct{}
	// controlCalls counts the capabilities a Build over an already-complete
	// range must not need: backfills, waits, gap detection and repairs.
	controlCalls int
}

// fakeSource is one source Dataset as acquisition would know it: what it is
// covered over, which open Gaps it has, and what the provider behind it can
// still serve.
type fakeSource struct {
	coverage []acq.Range
	gaps     []domain.Gap
	// serving is everything the provider behind this source can serve: a
	// backfill acquires the part of its range that falls inside it, and nothing
	// outside it. The zero value serves nothing at all, which is a provider
	// whose earliest-available floor lies past everything it is asked for.
	serving acq.Range
}

// request is one backfill request as it was made: of which source, over which
// range. Which source was asked for what is the cross-provider question.
type request struct {
	Source domain.Source
	Range  acq.Range
}

func newFakePort() *fakePort {
	return &fakePort{
		sources:   map[domain.Source]*fakeSource{},
		landing:   map[compositeapp.BackfillHandle]request{},
		repairing: map[compositeapp.BackfillHandle]domain.Gap{},
	}
}

// source is what acquisition knows about one source Dataset, created empty the
// first time anyone asks. The caller holds the lock.
func (f *fakePort) source(src domain.Source) *fakeSource {
	state, ok := f.sources[src]
	if !ok {
		state = &fakeSource{}
		f.sources[src] = state
	}
	return state
}

// cover says what the base source is covered over.
func (f *fakePort) cover(ranges ...acq.Range) { f.coverOf(baseSource, ranges...) }

// coverOf says what one source is covered over.
func (f *fakePort) coverOf(src domain.Source, ranges ...acq.Range) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.source(src).coverage = ranges
}

// openGap adds one open Gap acquisition would report for the base source.
func (f *fakePort) openGap(id int64, r acq.Range) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.source(baseSource)
	state.gaps = append(state.gaps, domain.Gap{ID: id, Range: r})
}

// serves says what the provider behind the base source can still serve.
func (f *fakePort) serves(r acq.Range) { f.servesOf(baseSource, r) }

// servesOf says what the provider behind one source can still serve.
func (f *fakePort) servesOf(src domain.Source, r acq.Range) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.source(src).serving = r
}

// busyFor refuses the next n backfill requests the way acquisition refuses one
// for a source Dataset that already has a backfill running.
func (f *fakePort) busyFor(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy = n
}

// failsWith ends every started backfill in the terminal acquisition failure.
func (f *fakePort) failsWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failure = err
}

// onFill says what happens when a backfill's bars land.
func (f *fakePort) onFill(fn func(domain.Source, acq.Range)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fill = fn
}

// attemptCount is how many backfill requests were made, refused ones included.
func (f *fakePort) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// requestedRanges is every range a backfill was asked over, whatever the
// source, whether or not acquisition had anything to acquire there.
func (f *fakePort) requestedRanges() []acq.Range { return ranges(f.requests()) }

// requestedRangesOf is every range a backfill of one source was asked over. It
// is how a test asks which provider was sent after what.
func (f *fakePort) requestedRangesOf(src domain.Source) []acq.Range {
	return ranges(only(f.requests(), src))
}

// backfilled is the ranges the admitted backfills were asked over.
func (f *fakePort) backfilled() []acq.Range { return ranges(f.admitted()) }

// backfilledOf is the ranges the admitted backfills of one source were asked
// over.
func (f *fakePort) backfilledOf(src domain.Source) []acq.Range {
	return ranges(only(f.admitted(), src))
}

func (f *fakePort) requests() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requested)
}

func (f *fakePort) admitted() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.started)
}

// only keeps the requests made of one source.
func only(requests []request, src domain.Source) []request {
	out := make([]request, 0, len(requests))
	for _, r := range requests {
		if r.Source == src {
			out = append(out, r)
		}
	}
	return out
}

// ranges is the ranges of a list of requests, in the order they were made.
func ranges(requests []request) []acq.Range {
	out := make([]acq.Range, 0, len(requests))
	for _, r := range requests {
		out = append(out, r.Range)
	}
	return out
}

// hold makes the next Coverage calls block until the returned release runs,
// and hands back the channel that reports one has arrived.
func (f *fakePort) hold() (entered <-chan struct{}, release func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entered = make(chan struct{}, 4)
	f.held = make(chan struct{})
	return f.entered, sync.OnceFunc(func() { close(f.held) })
}

func (f *fakePort) Coverage(_ context.Context, src domain.Source) ([]acq.Range, error) {
	f.mu.Lock()
	coverage := slices.Clone(f.source(src).coverage)
	entered, held := f.entered, f.held
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if held != nil {
		<-held
	}
	return coverage, nil
}

func (f *fakePort) Completeness(_ context.Context, src domain.Source, r acq.Range) (compositeapp.Completeness, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.source(src)
	covered := len(acq.SubtractRanges([]acq.Range{r}, state.coverage)) == 0
	open := state.open(r)
	return compositeapp.Completeness{Complete: covered && len(open) == 0, Gaps: open}, nil
}

// open is the Gaps of a source that intersect r.
func (s *fakeSource) open(r acq.Range) []domain.Gap {
	var out []domain.Gap
	for _, g := range s.gaps {
		if g.Range.Overlaps(r) {
			out = append(out, g)
		}
	}
	return out
}

// The capabilities below are the port's other half: the ones a Build reaches
// for only when the range it was asked for is not already there. A Build over
// a complete range must never need them, so every one of them counts itself.

func (f *fakePort) StartBackfill(_ context.Context, src domain.Source, r acq.Range) (compositeapp.BackfillHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.controlCalls++
	return f.admit(src, r)
}

func (f *fakePort) WaitBackfill(_ context.Context, h compositeapp.BackfillHandle) error {
	f.mu.Lock()
	f.controlCalls++
	landed, ok := f.landing[h]
	if !ok {
		f.mu.Unlock()
		return fmt.Errorf("the fake acquisition port knows no backfill %q", h)
	}
	delete(f.landing, h)
	gap, repairing := f.repairing[h]
	delete(f.repairing, h)
	if f.failure != nil {
		failure := f.failure
		f.mu.Unlock()
		return failure
	}
	// A completed backfill is covered over everything it landed, and a repair
	// whose Gap the provider served in full closes that Gap — which is what
	// acquisition's own terminal gap detection would do.
	state := f.source(landed.Source)
	state.coverage = acq.MergeRanges(append(state.coverage, landed.Range))
	if repairing && !landed.Range.Start.After(gap.Range.Start) && !landed.Range.End.Before(gap.Range.End) {
		state.gaps = slices.DeleteFunc(state.gaps, func(g domain.Gap) bool { return g.ID == gap.ID })
	}
	fill := f.fill
	f.mu.Unlock()

	if fill != nil {
		fill(landed.Source, landed.Range)
	}
	return nil
}

func (f *fakePort) DetectGaps(_ context.Context, src domain.Source, r acq.Range) ([]domain.Gap, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.controlCalls++
	return f.source(src).open(r), nil
}

func (f *fakePort) RepairGap(_ context.Context, g domain.Gap) (compositeapp.BackfillHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.controlCalls++
	h, err := f.admit(f.owner(g), g.Range)
	if err != nil {
		return "", err
	}
	f.repairing[h] = g
	return h, nil
}

// owner is the source Dataset one Gap belongs to. Acquisition names a Gap by
// its own id, so this is the fake's own lookup of what that id was reported
// for. The caller holds the lock.
func (f *fakePort) owner(g domain.Gap) domain.Source {
	for src, state := range f.sources {
		for _, known := range state.gaps {
			if known.ID == g.ID {
				return src
			}
		}
	}
	return baseSource
}

// admit is acquisition's own answer to a backfill request: a second backfill of
// one source Dataset is refused with ErrBackfillBusy, the requested range is
// clipped to what the provider can serve, and a request with nothing left in it
// is refused as having nothing to acquire. The caller holds the lock.
func (f *fakePort) admit(src domain.Source, r acq.Range) (compositeapp.BackfillHandle, error) {
	f.attempts++
	f.requested = append(f.requested, request{Source: src, Range: r})
	if f.busy > 0 {
		f.busy--
		return "", fmt.Errorf("%w: %s", compositeapp.ErrBackfillBusy, src)
	}
	effective := r.Intersect(f.source(src).serving)
	if effective.IsEmpty() {
		return "", fmt.Errorf("%w: nothing to acquire in %s", compositeapp.ErrNothingToAcquire, r)
	}
	f.handles++
	h := compositeapp.BackfillHandle(fmt.Sprintf("backfill-%d", f.handles))
	f.landing[h] = request{Source: src, Range: effective}
	f.started = append(f.started, request{Source: src, Range: r})
	return h, nil
}

func (f *fakePort) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.controlCalls
}

// --- the wire shape a Build answers with ------------------------------------

// detailJSON is what Get and Build answer: the declaration, the ordered
// Segments, and the Quality.
type detailJSON struct {
	compositeJSON
	Segments []struct {
		Kind   string     `json:"kind"`
		Source sourceJSON `json:"source"`
		Start  string     `json:"start"`
		End    string     `json:"end"`
	} `json:"segments"`
	Quality *struct {
		RequestedStart     string  `json:"requested_start"`
		RequestedEnd       string  `json:"requested_end"`
		ResolvedEnd        string  `json:"resolved_end"`
		AvailableStart     string  `json:"available_start"`
		AvailableEnd       string  `json:"available_end"`
		ExpectedBars       int64   `json:"expected_bars"`
		ActualBars         int64   `json:"actual_bars"`
		CoveragePercentage float64 `json:"coverage_percentage"`
		OpenGapCount       int     `json:"open_gap_count"`
		OpenGaps           []struct {
			ID    int64  `json:"id"`
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"open_gaps"`
		TransitionCount int `json:"transition_count"`
		Transitions     []struct {
			At    string     `json:"at"`
			From  sourceJSON `json:"from"`
			To    sourceJSON `json:"to"`
			Close string     `json:"close"`
			Open  string     `json:"open"`
			Delta string     `json:"price_delta"`
		} `json:"transitions"`
		IncompleteWindowCount int `json:"incomplete_window_count"`
		IncompleteWindows     []struct {
			Timeframe string `json:"timeframe"`
			Start     string `json:"start"`
			End       string `json:"end"`
		} `json:"incomplete_windows"`
		Mode                   string `json:"mode"`
		Strict                 bool   `json:"strict"`
		MaterializationVersion int    `json:"materialization_version"`
		LastBuildAt            string `json:"last_build_at"`
	} `json:"quality"`
	// The declaration shape this embeds carries no build outcome, so the three
	// fields a Build writes onto the dataset row are named here.
	State       string `json:"state"`
	ResolvedEnd string `json:"resolved_end"`
	LastError   string `json:"last_error"`
}

// buildRange is the hour every Build test asks for: small enough to seed a
// real bar per minute, long enough to have an inside.
var (
	buildStart = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buildEnd   = time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC)
	buildRange = acq.Range{Start: buildStart, End: buildEnd}
)

// buildable is the declaration the Build tests start from: one hour of a
// 1-minute binance source, no materialized timeframes.
func buildable(name, mode string) map[string]any {
	return map[string]any{
		"name":       name,
		"instrument": instrument,
		"base": map[string]any{
			"instrument": instrument, "provider": "binance",
			"symbol": baseSymbol, "timeframe": "1m",
		},
		"requested_start": "2024-01-01T00:00:00Z",
		"requested_end":   "2024-01-01T01:00:00Z",
		"timeframes":      []string{},
		"mode":            mode,
	}
}

// build posts one Build and insists on the status.
func (h *harness) build(name string, want int) detailJSON {
	h.t.Helper()
	var got detailJSON
	h.decode(h.do("POST", "/composites/"+name+"/build", nil), want, &got)
	return got
}

// get reads one Composite Dataset back with its provenance.
func (h *harness) get(name string) detailJSON {
	h.t.Helper()
	var got detailJSON
	h.decode(h.do("GET", "/composites/"+name, nil), http.StatusOK, &got)
	return got
}

// readyDataset is the whole happy path: a covered, gapless, fully seeded hour.
func readyDataset(t *testing.T, name, mode string) *harness {
	t.Helper()
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(buildable(name, mode))
	return h
}

// --- create → build → get ---------------------------------------------------

func TestBuildAssemblesOneBaseSegmentAndTheDatasetBecomesReady(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	built := h.build("btc-usd", http.StatusOK)

	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	if len(built.Segments) != 1 {
		t.Fatalf("segments = %+v, want exactly one base segment", built.Segments)
	}
	seg := built.Segments[0]
	if seg.Kind != "base" {
		t.Errorf("segment kind = %q, want base", seg.Kind)
	}
	want := sourceJSON{Instrument: instrument, Provider: "binance", Symbol: baseSymbol, Timeframe: "1m"}
	if seg.Source != want {
		t.Errorf("segment source = %+v, want %+v", seg.Source, want)
	}
	if seg.Start != "2024-01-01T00:00:00Z" || seg.End != "2024-01-01T01:00:00Z" {
		t.Errorf("segment range = %s .. %s, want the covered hour", seg.Start, seg.End)
	}
	// The covered-range happy path needs no backfill, no repair and no gap
	// detection: the control plane was asked for coverage and completeness and
	// nothing else.
	if h.port.calls() != 0 {
		t.Errorf("the build reached %d capabilities it should not need", h.port.calls())
	}

	// Get is the provenance API: the same segments and quality come back.
	got := h.get("btc-usd")
	if len(got.Segments) != 1 || got.Segments[0] != seg {
		t.Fatalf("get segments = %+v, want the built one %+v", got.Segments, seg)
	}
	if got.Quality == nil {
		t.Fatal("get answered no quality for a built dataset")
	}
	if got.State != "ready" {
		t.Errorf("get state = %q, want ready", got.State)
	}
}

func TestBuildComputesTheQualityOfWhatItBuilt(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	q := h.build("btc-usd", http.StatusOK).Quality
	if q == nil {
		t.Fatal("a build computed no quality")
	}
	if q.RequestedStart != "2024-01-01T00:00:00Z" || q.RequestedEnd != "2024-01-01T01:00:00Z" {
		t.Errorf("requested range = %s .. %s, want the declared one", q.RequestedStart, q.RequestedEnd)
	}
	if q.ResolvedEnd != "2024-01-01T01:00:00Z" {
		t.Errorf("resolved end = %s, want the fixed end passed through", q.ResolvedEnd)
	}
	if q.AvailableStart != "2024-01-01T00:00:00Z" || q.AvailableEnd != "2024-01-01T01:00:00Z" {
		t.Errorf("available range = %s .. %s, want the covered hour", q.AvailableStart, q.AvailableEnd)
	}
	if q.ExpectedBars != 60 || q.ActualBars != 60 {
		t.Errorf("bars = %d of %d expected, want 60 of 60", q.ActualBars, q.ExpectedBars)
	}
	if q.CoveragePercentage != 100 {
		t.Errorf("coverage = %v%%, want 100", q.CoveragePercentage)
	}
	if q.OpenGapCount != 0 || len(q.OpenGaps) != 0 {
		t.Errorf("open gaps = %+v, want none", q.OpenGaps)
	}
	if q.Mode != "strict" || !q.Strict {
		t.Errorf("mode = %q / strict %v, want strict / true", q.Mode, q.Strict)
	}
	if q.LastBuildAt != "2026-08-31T12:00:00Z" {
		t.Errorf("last build at = %s, want the clock's instant", q.LastBuildAt)
	}
}

// TestQualityCountsOnlyTheBarsThatAreReallyThere is the data plane at work:
// the port says the hour is covered, but only half of it was ever written, and
// the counts say so because they come from the bars table itself.
func TestQualityCountsOnlyTheBarsThatAreReallyThere(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, acq.Range{Start: buildStart, End: buildStart.Add(30 * time.Minute)})
	h.create(buildable("btc-usd", "research"))

	q := h.build("btc-usd", http.StatusOK).Quality
	if q.ActualBars != 30 || q.ExpectedBars != 60 {
		t.Fatalf("bars = %d of %d, want 30 of 60", q.ActualBars, q.ExpectedBars)
	}
	if q.CoveragePercentage != 50 {
		t.Fatalf("coverage = %v%%, want 50", q.CoveragePercentage)
	}
}

// --- resolving the end ------------------------------------------------------

func TestBuildResolvesAnEndOfNowToTheLastFullyClosedMinute(t *testing.T) {
	h := newHarness(t)
	// The clock is mid-minute: the bar opening at 12:34 is still forming, so
	// the resolved end is 12:34 — that bar's open time, and the previous one's
	// close.
	now := time.Date(2024, 1, 1, 12, 34, 20, 0, time.UTC)
	h.setNow(now)
	resolved := time.Date(2024, 1, 1, 12, 34, 0, 0, time.UTC)
	h.port.cover(acq.Range{Start: buildStart, End: resolved})
	h.seedBars("binance", baseSymbol, acq.Range{Start: buildStart, End: buildStart.Add(time.Hour)})

	body := buildable("btc-usd-now", "research")
	body["requested_end"] = "now"
	h.create(body)

	built := h.build("btc-usd-now", http.StatusOK)
	if built.ResolvedEnd != "2024-01-01T12:34:00Z" {
		t.Fatalf("resolved end on the dataset = %q, want 2024-01-01T12:34:00Z", built.ResolvedEnd)
	}
	if built.Quality.ResolvedEnd != "2024-01-01T12:34:00Z" {
		t.Fatalf("resolved end in quality = %q, want 2024-01-01T12:34:00Z", built.Quality.ResolvedEnd)
	}
	if built.Quality.RequestedEnd != "now" {
		t.Errorf("requested end = %q, want the declaration's now", built.Quality.RequestedEnd)
	}
	// Everything is judged against the resolved end: 754 minutes of it.
	if built.Quality.ExpectedBars != 754 {
		t.Errorf("expected bars = %d, want the 754 minutes up to the resolved end", built.Quality.ExpectedBars)
	}
	// It is persisted, not just answered.
	if got := h.get("btc-usd-now").ResolvedEnd; got != "2024-01-01T12:34:00Z" {
		t.Fatalf("resolved end read back = %q, want it persisted", got)
	}
}

func TestBuildPassesAFixedEndThrough(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	h.setNow(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if got := h.build("btc-usd", http.StatusOK).ResolvedEnd; got != "2024-01-01T01:00:00Z" {
		t.Fatalf("resolved end = %q, want the declared fixed end whatever the clock says", got)
	}
}

// --- strict and research ----------------------------------------------------

func TestStrictBuildFailsWithTheGapsReported(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	gap := acq.Range{Start: buildStart.Add(10 * time.Minute), End: buildStart.Add(15 * time.Minute)}
	h.port.openGap(7, gap)

	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusConflict)
	if !strings.Contains(message, "2024-01-01T00:10:00Z") {
		t.Errorf("the failure said %q, want it to name the gap", message)
	}

	got := h.get("btc-usd")
	if got.State != "failed" {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.LastError == "" {
		t.Error("a failed build must preserve its error")
	}
	if len(got.Segments) != 0 {
		t.Errorf("segments = %+v, want none — a build that could not be ready assembled nothing", got.Segments)
	}
	if got.Quality == nil {
		t.Fatal("a failed build must still leave the quality that explains it")
	}
	if got.Quality.OpenGapCount != 1 || len(got.Quality.OpenGaps) != 1 {
		t.Fatalf("open gaps = %+v, want the one that blocked readiness", got.Quality.OpenGaps)
	}
	listed := got.Quality.OpenGaps[0]
	if listed.ID != 7 || listed.Start != "2024-01-01T00:10:00Z" || listed.End != "2024-01-01T00:15:00Z" {
		t.Errorf("listed gap = %+v, want the one acquisition reported", listed)
	}
}

func TestResearchBuildIsReadyWithItsGapsListedAndVisiblyResearch(t *testing.T) {
	h := readyDataset(t, "btc-usd", "research")
	h.port.openGap(7, acq.Range{Start: buildStart.Add(10 * time.Minute), End: buildStart.Add(15 * time.Minute)})

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready — research mode explores imperfect history", built.State)
	}
	if len(built.Segments) != 1 {
		t.Fatalf("segments = %+v, want the one base segment", built.Segments)
	}
	if built.Quality.OpenGapCount != 1 {
		t.Fatalf("open gaps = %d, want the one it is ready in spite of", built.Quality.OpenGapCount)
	}
	// The imperfection is visible, and can never read as a complete dataset.
	if built.Quality.Strict || built.Quality.Mode != "research" {
		t.Errorf("quality says strict %v / mode %q, want false / research", built.Quality.Strict, built.Quality.Mode)
	}
	if built.Mode != "research" {
		t.Errorf("dataset mode = %q, want research", built.Mode)
	}

	// And it is all persisted: Get answers the same segment and the same
	// listed gap, which is the research half of create → build → get.
	got := h.get("btc-usd")
	if got.State != "ready" || len(got.Segments) != 1 {
		t.Fatalf("get = %q with segments %+v, want ready with the base segment", got.State, got.Segments)
	}
	if got.Quality == nil || got.Quality.Strict || got.Quality.OpenGapCount != 1 {
		t.Fatalf("get quality = %+v, want a research one listing its gap", got.Quality)
	}
	if got.Quality.OpenGaps[0].ID != 7 {
		t.Errorf("listed gap = %+v, want the one acquisition reported", got.Quality.OpenGaps[0])
	}
}

func TestStrictBuildFailsWhenTheBaseSourceDoesNotCoverTheWholeRange(t *testing.T) {
	h := newHarness(t)
	h.port.cover(acq.Range{Start: buildStart, End: buildStart.Add(30 * time.Minute)})
	h.seedBars("binance", baseSymbol, acq.Range{Start: buildStart, End: buildStart.Add(30 * time.Minute)})
	h.create(buildable("btc-usd", "strict"))

	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusConflict)
	if !strings.Contains(message, "2024-01-01T00:30:00Z") {
		t.Errorf("the failure said %q, want it to name the part that is not supplied", message)
	}
	got := h.get("btc-usd")
	if got.State != "failed" {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.Quality.AvailableEnd != "2024-01-01T00:30:00Z" {
		t.Errorf("available end = %q, want where the data really stops", got.Quality.AvailableEnd)
	}
}

// TestResearchBuildStartsWhereTheDataStarts is the research half of the same
// shortfall: the dataset is ready, and Quality records that it begins later
// than it was asked to.
func TestResearchBuildStartsWhereTheDataStarts(t *testing.T) {
	h := newHarness(t)
	available := acq.Range{Start: buildStart.Add(20 * time.Minute), End: buildEnd}
	h.port.cover(available)
	h.seedBars("binance", baseSymbol, available)
	h.create(buildable("btc-usd", "research"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready", built.State)
	}
	if built.Quality.AvailableStart != "2024-01-01T00:20:00Z" {
		t.Errorf("available start = %q, want where the data starts", built.Quality.AvailableStart)
	}
	if built.Segments[0].Start != "2024-01-01T00:20:00Z" {
		t.Errorf("segment start = %q, want where the data starts", built.Segments[0].Start)
	}
	if built.Quality.ActualBars != 40 || built.Quality.ExpectedBars != 60 {
		t.Errorf("bars = %d of %d, want 40 of 60", built.Quality.ActualBars, built.Quality.ExpectedBars)
	}
}

func TestABuildOverARangeTheBaseSourceSuppliesNothingOfFails(t *testing.T) {
	h := newHarness(t)
	h.create(buildable("btc-usd", "research"))
	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusConflict)
	if !strings.Contains(message, "supplies nothing") {
		t.Errorf("the failure said %q, want it to say the base source supplies nothing", message)
	}
	if got := h.get("btc-usd"); got.State != "failed" || got.LastError == "" {
		t.Fatalf("state = %q with error %q, want failed with the error preserved", got.State, got.LastError)
	}
}

// --- lifecycle --------------------------------------------------------------

func TestABuildOfAStaleDatasetMakesItReadyAgain(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	h.build("btc-usd", http.StatusOK)
	h.setState("btc-usd", domain.StateStale)
	if got := h.build("btc-usd", http.StatusOK).State; got != "ready" {
		t.Fatalf("state = %q, want ready — a build is what reconciles a stale dataset", got)
	}
}

// TestASecondBuildReplacesTheSegmentsWholesale proves segments are build
// output, not an accumulating log.
func TestASecondBuildReplacesTheSegmentsWholesale(t *testing.T) {
	h := readyDataset(t, "btc-usd", "research")
	h.build("btc-usd", http.StatusOK)

	// The base source now covers only half the hour, and the next build says so.
	h.port.cover(acq.Range{Start: buildStart, End: buildStart.Add(30 * time.Minute)})
	built := h.build("btc-usd", http.StatusOK)
	if len(built.Segments) != 1 {
		t.Fatalf("segments = %+v, want exactly one — they are replaced, not appended", built.Segments)
	}
	if built.Segments[0].End != "2024-01-01T00:30:00Z" {
		t.Fatalf("segment end = %q, want the range the second build found", built.Segments[0].End)
	}
}

func TestASecondConcurrentBuildOfOneDatasetIsRejected(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	entered, release := h.port.hold()

	first := make(chan *http.Response, 1)
	go func() { first <- h.do("POST", "/composites/btc-usd/build", nil) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first build never reached the acquisition port")
	}

	// A build in flight is a state on the row, not just a lock in memory.
	if got := h.get("btc-usd").State; got != "building" {
		t.Errorf("state during a build = %q, want building", got)
	}

	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusConflict)
	if !strings.Contains(message, "already running") {
		t.Errorf("the rejection said %q, want it to say a build is already running", message)
	}

	release()
	var built detailJSON
	h.decode(<-first, http.StatusOK, &built)
	if built.State != "ready" {
		t.Fatalf("the first build ended %q, want ready", built.State)
	}
}

func TestBuildingADatasetThatDoesNotExistIsNotFound(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("POST", "/composites/nope/build", nil), http.StatusNotFound)
}

// --- get before any build ---------------------------------------------------

func TestGetAnswersNoSegmentsAndNoQualityForADraft(t *testing.T) {
	h := newHarness(t)
	h.create(buildable("btc-usd", "strict"))
	got := h.get("btc-usd")
	if got.State != "draft" {
		t.Fatalf("state = %q, want draft", got.State)
	}
	if len(got.Segments) != 0 {
		t.Errorf("segments = %+v, want none before a build", got.Segments)
	}
	if got.Quality != nil {
		t.Errorf("quality = %+v, want none before a build", got.Quality)
	}
}
