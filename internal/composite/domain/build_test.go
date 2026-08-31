package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

var (
	buildStart = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buildEnd   = time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
)

func baseSource() domain.Source {
	return domain.Source{
		Instrument: "BTC/USD", Provider: "binance",
		Symbol: acq.Symbol("BTCUSDT"), Timeframe: domain.TF1m,
	}
}

// --- lifecycle --------------------------------------------------------------

func TestBeginBuildMovesEveryRestingStateIntoBuilding(t *testing.T) {
	for _, s := range []domain.State{domain.StateDraft, domain.StateStale, domain.StateReady, domain.StateFailed} {
		got, err := s.BeginBuild()
		if err != nil {
			t.Errorf("%s.BeginBuild() = %v, want building", s, err)
			continue
		}
		if got != domain.StateBuilding {
			t.Errorf("%s.BeginBuild() = %q, want building", s, got)
		}
	}
}

func TestBeginBuildRefusesADatasetAlreadyBuilding(t *testing.T) {
	_, err := domain.StateBuilding.BeginBuild()
	if !errors.Is(err, domain.ErrBuildRunning) {
		t.Fatalf("building.BeginBuild() = %v, want ErrBuildRunning", err)
	}
}

func TestSettledIsReadyOrFailed(t *testing.T) {
	if got := domain.Settled(true); got != domain.StateReady {
		t.Errorf("Settled(true) = %q, want ready", got)
	}
	if got := domain.Settled(false); got != domain.StateFailed {
		t.Errorf("Settled(false) = %q, want failed", got)
	}
}

// --- resolved end -----------------------------------------------------------

func TestResolveEndTakesTheLastFullyClosedMinuteForNow(t *testing.T) {
	now := time.Date(2024, 3, 5, 12, 34, 20, 500, time.UTC)
	got := domain.NowEnd().Resolve(now)
	want := time.Date(2024, 3, 5, 12, 34, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Resolve(%s) = %s, want %s — the bar opening at 12:34 is still forming", now, got, want)
	}
}

func TestResolveEndOnAMinuteBoundaryIsThatBoundary(t *testing.T) {
	now := time.Date(2024, 3, 5, 12, 34, 0, 0, time.UTC)
	if got := domain.NowEnd().Resolve(now); !got.Equal(now) {
		t.Fatalf("Resolve(%s) = %s, want the boundary itself", now, got)
	}
}

func TestResolveEndPassesAFixedEndThrough(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := domain.FixedEnd(buildEnd).Resolve(now); !got.Equal(buildEnd) {
		t.Fatalf("Resolve of a fixed end = %s, want %s", got, buildEnd)
	}
}

// --- segments ---------------------------------------------------------------

func TestASegmentNamesItsProviderAndRange(t *testing.T) {
	seg := domain.Segment{
		Kind: domain.SegmentBase, Source: baseSource(),
		Range: acq.Range{Start: buildStart, End: buildEnd},
	}
	if got := seg.String(); got == "" {
		t.Fatal("a Segment must render as something a log line can carry")
	}
}

func TestSegmentKindsAreTheThreeReservedOnes(t *testing.T) {
	// `live` is reserved by the model and never assembled in v1 (decision 1).
	for _, k := range []domain.SegmentKind{domain.SegmentBase, domain.SegmentCatchUp, domain.SegmentLive} {
		if !k.Valid() {
			t.Errorf("%q is not a valid segment kind", k)
		}
	}
	if domain.SegmentKind("chunk").Valid() {
		t.Error(`"chunk" is not a segment kind`)
	}
}

// --- quality ----------------------------------------------------------------

func TestNewQualityRecordsWhatWasAskedForAndExpectsAMinuteBarPerMinute(t *testing.T) {
	cfg := domain.Config{
		Instrument: "BTC/USD", Base: baseSource(),
		RequestedStart: buildStart, RequestedEnd: domain.NowEnd(),
		Mode: domain.ModeResearch,
	}
	at := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	q := domain.NewQuality(cfg, buildEnd, at)

	if !q.RequestedStart.Equal(buildStart) || q.RequestedEnd.String() != domain.NowLiteral {
		t.Errorf("requested range = %s .. %s, want the declared one", q.RequestedStart, q.RequestedEnd)
	}
	if !q.ResolvedEnd.Equal(buildEnd) {
		t.Errorf("resolved end = %s, want %s", q.ResolvedEnd, buildEnd)
	}
	if q.ExpectedBars != 1440 {
		t.Errorf("expected bars = %d, want 1440 minutes in a day", q.ExpectedBars)
	}
	if q.Mode != domain.ModeResearch || !q.LastBuildAt.Equal(at) {
		t.Errorf("mode/build time = %q / %s, want research / %s", q.Mode, q.LastBuildAt, at)
	}
}

func TestQualityCoveragePercentageIsActualOverExpected(t *testing.T) {
	q := domain.Quality{ExpectedBars: 1440, ActualBars: 720}
	if got := q.Coverage(); got != 50 {
		t.Fatalf("coverage = %v, want 50", got)
	}
	empty := domain.Quality{}
	if got := empty.Coverage(); got != 0 {
		t.Fatalf("coverage of nothing = %v, want 0", got)
	}
}

func TestQualityIsStrictOnlyInStrictMode(t *testing.T) {
	if !(domain.Quality{Mode: domain.ModeStrict}).Strict() {
		t.Error("a strict dataset's quality must say so")
	}
	if (domain.Quality{Mode: domain.ModeResearch}).Strict() {
		t.Error("a research dataset's quality must be visibly not strict")
	}
}
