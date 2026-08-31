package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// The rules that make an assembled Segment list one timeline: the boundaries
// between two providers are Transitions, and a Transition that cannot be
// safely resolved is refused rather than accepted.

var (
	transitionStart = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	binanceSource   = domain.Source{
		Instrument: "BTC/USD", Provider: "binance",
		Symbol: acq.Symbol("BTCUSDT"), Timeframe: domain.TF1m,
	}
	coinbaseSource = domain.Source{
		Instrument: "BTC/USD", Provider: "coinbase",
		Symbol: acq.Symbol("BTC-USD"), Timeframe: domain.TF1m,
	}
)

// at is an instant so many minutes into the timeline these tests use.
func at(minutes int) time.Time {
	return transitionStart.Add(time.Duration(minutes) * time.Minute)
}

// timeline is a base segment followed by a catch-up one over the given bounds.
func timeline(baseEnd, catchUpStart, catchUpEnd int) []domain.Segment {
	return []domain.Segment{
		{Kind: domain.SegmentBase, Source: binanceSource,
			Range: acq.Range{Start: at(0), End: at(baseEnd)}},
		{Kind: domain.SegmentCatchUp, Source: coinbaseSource,
			Range: acq.Range{Start: at(catchUpStart), End: at(catchUpEnd)}},
	}
}

func TestAbuttingSegmentsOfTwoProvidersAreAValidTransition(t *testing.T) {
	if err := domain.ValidateTransitions(timeline(30, 30, 60)); err != nil {
		t.Fatalf("ValidateTransitions = %v, want nil — the segments abut exactly", err)
	}
}

func TestASingleSegmentTimelineHasNothingToValidate(t *testing.T) {
	segments := timeline(30, 30, 60)[:1]
	if err := domain.ValidateTransitions(segments); err != nil {
		t.Fatalf("ValidateTransitions = %v, want nil", err)
	}
	if got := domain.TransitionsOf(segments); len(got) != 0 {
		t.Fatalf("transitions = %+v, want none", got)
	}
}

func TestOverlappingSegmentsAreRefusedAsAConflict(t *testing.T) {
	err := domain.ValidateTransitions(timeline(30, 20, 60))
	if !errors.Is(err, domain.ErrTransitionInvalid) {
		t.Fatalf("ValidateTransitions = %v, want an invalid transition", err)
	}
	// The reason is in the message: which range both providers supply, and
	// that the policy refuses rather than merges it.
	for _, want := range []string{"00:20:00Z", "00:30:00Z", "reject_conflict", "binance", "coinbase"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal said %q, want it to name %q", err, want)
		}
	}
}

func TestATimestampHoleBetweenSegmentsIsRefused(t *testing.T) {
	err := domain.ValidateTransitions(timeline(30, 40, 60))
	if !errors.Is(err, domain.ErrTransitionInvalid) {
		t.Fatalf("ValidateTransitions = %v, want an invalid transition", err)
	}
	for _, want := range []string{"00:30:00Z", "00:40:00Z", "abut"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal said %q, want it to name %q", err, want)
		}
	}
}

func TestASegmentDeclaringAnotherInstrumentIsRefused(t *testing.T) {
	segments := timeline(30, 30, 60)
	segments[1].Source.Instrument = "ETH/USD"
	err := domain.ValidateTransitions(segments)
	if !errors.Is(err, domain.ErrTransitionInvalid) {
		t.Fatalf("ValidateTransitions = %v, want an invalid transition", err)
	}
	if !strings.Contains(err.Error(), "ETH/USD") || !strings.Contains(err.Error(), "BTC/USD") {
		t.Errorf("the refusal said %q, want it to name both instruments", err)
	}
}

func TestASegmentOfAnotherCanonicalTimeframeIsRefused(t *testing.T) {
	segments := timeline(30, 30, 60)
	segments[1].Source.Timeframe = domain.TF5m
	err := domain.ValidateTransitions(segments)
	if !errors.Is(err, domain.ErrTransitionInvalid) {
		t.Fatalf("ValidateTransitions = %v, want an invalid transition", err)
	}
	if !strings.Contains(err.Error(), "5m") || !strings.Contains(err.Error(), "1m") {
		t.Errorf("the refusal said %q, want it to name both timeframes", err)
	}
}

func TestASegmentThatCoversNoTimeIsRefused(t *testing.T) {
	segments := timeline(30, 30, 30)
	if err := domain.ValidateTransitions(segments); !errors.Is(err, domain.ErrTransitionInvalid) {
		t.Fatalf("ValidateTransitions = %v, want an invalid transition", err)
	}
}

func TestATransitionIsRecordedForEveryProviderBoundary(t *testing.T) {
	got := domain.TransitionsOf(timeline(30, 30, 60))
	if len(got) != 1 {
		t.Fatalf("transitions = %+v, want exactly one", got)
	}
	if !got[0].At.Equal(at(30)) {
		t.Errorf("transition at %s, want %s", got[0].At, at(30))
	}
	if got[0].From != binanceSource || got[0].To != coinbaseSource {
		t.Errorf("transition = %s, want binance → coinbase", got[0])
	}
	// The domain invents no prices: they are facts in acquisition's bars.
	if got[0].Priced() {
		t.Errorf("transition = %+v, want no price until one is read from the bars", got[0])
	}
}

func TestTwoSegmentsOfTheSameSourceAreNotATransition(t *testing.T) {
	segments := timeline(30, 30, 60)
	segments[1].Source = binanceSource
	if got := domain.TransitionsOf(segments); len(got) != 0 {
		t.Fatalf("transitions = %+v, want none — the same source did not hand over", got)
	}
}
