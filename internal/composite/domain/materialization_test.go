package domain_test

import (
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// The rules a Build materializes higher timeframes by: which windows one
// covers, which of them the source data does not fully back, and when the bars
// on disk were derived by a version this service no longer produces.

var jan = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func rng(start, end time.Time) acq.Range { return acq.Range{Start: start, End: end} }

// The composite timeline every incomplete-window test judges: the first hour of
// 2024, supplied in full unless the test says otherwise.
var (
	hour   = rng(jan, jan.Add(time.Hour))
	frames = []domain.Timeframe{domain.TF5m, domain.TF1h}
)

func TestNoGapAndNoShortfallLeavesEveryWindowComplete(t *testing.T) {
	got := domain.IncompleteWindows(frames, hour, hour, nil)
	if len(got) != 0 {
		t.Fatalf("incomplete windows = %v, want none over a fully supplied timeline", got)
	}
}

func TestAWindowOverlappingAnOpenGapIsIncomplete(t *testing.T) {
	// Twelve minutes of missing bars: the 5m windows at :10, :15 and :20 all
	// overlap it, and so does the hour.
	gaps := []domain.Gap{{ID: 1, Range: rng(jan.Add(12*time.Minute), jan.Add(24*time.Minute))}}
	got := domain.IncompleteWindows(frames, hour, hour, gaps)

	want := []domain.IncompleteWindow{
		{Timeframe: domain.TF5m, Range: rng(jan.Add(10*time.Minute), jan.Add(15*time.Minute))},
		{Timeframe: domain.TF5m, Range: rng(jan.Add(20*time.Minute), jan.Add(25*time.Minute))},
		{Timeframe: domain.TF1h, Range: hour},
	}
	assertWindows(t, got, want)
}

// A window with no source bar at all emits no bar, so it is not an incomplete
// materialization: it is simply not materialized, and the coverage figures are
// what report it.
func TestAWindowEntirelyInsideAGapIsNotAnIncompleteWindow(t *testing.T) {
	gaps := []domain.Gap{{ID: 1, Range: rng(jan.Add(10*time.Minute), jan.Add(20*time.Minute))}}
	got := domain.IncompleteWindows([]domain.Timeframe{domain.TF5m}, hour, hour, gaps)
	if len(got) != 0 {
		t.Fatalf("incomplete windows = %v, want none: the two 5m windows inside the gap emit no bar at all", got)
	}
}

// The head the sources could not supply is missing data like any other: the one
// window that straddles where the data starts is incomplete, and the windows
// before it — which hold nothing — are not.
func TestTheWindowWhereTheDataStartsIsIncomplete(t *testing.T) {
	available := rng(jan.Add(12*time.Minute), hour.End)
	got := domain.IncompleteWindows(frames, hour, available, nil)

	want := []domain.IncompleteWindow{
		{Timeframe: domain.TF5m, Range: rng(jan.Add(10*time.Minute), jan.Add(15*time.Minute))},
		{Timeframe: domain.TF1h, Range: hour},
	}
	assertWindows(t, got, want)
}

// A window reaching past the requested range is not incomplete for that: the
// dataset was never asked for those instants.
func TestAWindowReachingOutsideTheResolvedRangeIsNotIncompleteForThat(t *testing.T) {
	// A Wednesday-to-Wednesday request: both weeks are half outside it.
	resolved := rng(jan.AddDate(0, 0, 2), jan.AddDate(0, 0, 9))
	got := domain.IncompleteWindows([]domain.Timeframe{domain.TF1w}, resolved, resolved, nil)
	if len(got) != 0 {
		t.Fatalf("incomplete windows = %v, want none: nothing inside the requested range is missing", got)
	}
}

func TestOnlyTheConfiguredFramesAreJudged(t *testing.T) {
	gaps := []domain.Gap{{ID: 1, Range: rng(jan.Add(12*time.Minute), jan.Add(24*time.Minute))}}
	got := domain.IncompleteWindows(nil, hour, hour, gaps)
	if len(got) != 0 {
		t.Fatalf("incomplete windows = %v, want none: no timeframe is materialized", got)
	}
}

// assertWindows insists on exactly these incomplete windows, in this order.
func assertWindows(t *testing.T, got, want []domain.IncompleteWindow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("incomplete windows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Timeframe != want[i].Timeframe ||
			!got[i].Range.Start.Equal(want[i].Range.Start) || !got[i].Range.End.Equal(want[i].Range.End) {
			t.Fatalf("incomplete window %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// --- the materialization version ---------------------------------------------

func TestABuiltDatasetIsStaleWhenItsBarsCameFromAnotherVersion(t *testing.T) {
	d := domain.Dataset{Name: "btc-usd", State: domain.StateReady, MatVersion: domain.MaterializationVersion}
	if got := d.Observed(); got.State != domain.StateReady {
		t.Errorf("state = %q, want ready: the bars were derived by this version", got.State)
	}

	d.MatVersion = domain.MaterializationVersion - 1
	if got := d.Observed(); got.State != domain.StateStale {
		t.Errorf("state = %q, want stale: the bars were derived by another version", got.State)
	}
}

func TestOnlyABuiltDatasetCanBeStaleOverItsVersion(t *testing.T) {
	// A draft has no materialized bars to be out of date, and a failed build
	// left none either.
	for _, state := range []domain.State{domain.StateDraft, domain.StateFailed, domain.StateStale} {
		d := domain.Dataset{Name: "btc-usd", State: state}
		if got := d.Observed(); got.State != state {
			t.Errorf("a %q dataset reads as %q, want %q", state, got.State, state)
		}
	}
}
