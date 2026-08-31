package domain

import (
	"fmt"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
)

// A Transition is the boundary between two adjacent Segments that come from
// different sources — the moment a composite timeline stops answering with one
// provider's data and starts answering with another's.
//
// It is the thing this context refuses to hide. Every Transition is validated
// when a Build assembles the Segments (no hole, no overlap, same declared
// Instrument, same canonical timeframe), and the price movement across it is
// recorded in Quality so a researcher can see how far the two providers
// disagreed at the seam. No threshold is enforced: the delta is evidence, not
// a rule (decision 26).
type Transition struct {
	// At is the instant the timeline changes hands: the exclusive end of the
	// outgoing Segment and the inclusive start of the incoming one, which are
	// the same instant because Segments abut exactly.
	At time.Time
	// From is the source that answered up to At, To the one that answers from
	// At onwards.
	From Source
	To   Source
	// Close is the close price of the outgoing source's last bar before At,
	// Open the open price of the incoming source's first bar at At, and Delta
	// their exact difference — Open minus Close.
	//
	// All three are decimal strings, never floats: prices cross this context
	// as the exact decimals acquisition stored, and the difference is computed
	// exactly over those decimals. All three are empty when one of the two
	// bars is not there to price the Transition with.
	Close string
	Open  string
	Delta string
}

// Priced reports whether the price movement across this Transition could be
// computed — both bars existed.
func (t Transition) Priced() bool { return t.Delta != "" }

// String renders a Transition the way a log line names one.
func (t Transition) String() string {
	return t.From.String() + " → " + t.To.String() + " at " + instant(t.At)
}

// TransitionsOf is the Transitions of an ordered Segment list: one per
// boundary between Segments of different sources. Two adjacent Segments of the
// same source are one continuous stretch of one provider's data, not a
// Transition — there is nothing to validate and nothing to price.
//
// The prices are left empty here: this package computes no prices, because a
// price is a fact in acquisition's bars table and never a domain invention.
func TransitionsOf(segments []Segment) []Transition {
	var out []Transition
	for i := 1; i < len(segments); i++ {
		prev, next := segments[i-1], segments[i]
		if prev.Source == next.Source {
			continue
		}
		out = append(out, Transition{At: next.Range.Start.UTC(), From: prev.Source, To: next.Source})
	}
	return out
}

// ValidateTransitions is the rule that makes a composite timeline one timeline:
// the Segments are ordered, each is a real range, and each boundary between two
// of them is a Transition that can be safely resolved.
//
// It refuses, with an error wrapping ErrTransitionInvalid:
//
//   - an overlap — two sources holding data for the same range. There is one
//     merge policy and it is reject_conflict: conflicting provider data is
//     surfaced, never merged by guesswork (decision 5).
//   - a hole — a stretch of the requested timeline no Segment answers for,
//     which would make the ordered Segment list claim a continuity it does not
//     have.
//   - a different declared Instrument or a different canonical timeframe
//     across the boundary, which would silently splice two markets or two
//     resolutions into one timeline.
//
// A Build that cannot validate its Transitions fails and says why. Nothing is
// silently accepted.
func ValidateTransitions(segments []Segment) error {
	for i, seg := range segments {
		if seg.Range.IsEmpty() {
			return fmt.Errorf("%w: the %s segment %s covers no time at all",
				ErrTransitionInvalid, seg.Kind, seg.Source)
		}
		if i == 0 {
			continue
		}
		if err := validateBoundary(segments[i-1], seg); err != nil {
			return err
		}
	}
	return nil
}

// validateBoundary is the whole rule for one boundary, in the order a reader
// wants to hear it: what the two sources are, then where they meet.
func validateBoundary(prev, next Segment) error {
	if prev.Source.Instrument != next.Source.Instrument {
		return fmt.Errorf("%w: %s answers for instrument %q and %s for %q — one timeline is one market",
			ErrTransitionInvalid, prev.Source, prev.Source.Instrument,
			next.Source, next.Source.Instrument)
	}
	if prev.Source.Timeframe != next.Source.Timeframe {
		return fmt.Errorf("%w: %s is a %q source and %s a %q one — one timeline is one canonical timeframe",
			ErrTransitionInvalid, prev.Source, prev.Source.Timeframe,
			next.Source, next.Source.Timeframe)
	}
	switch {
	case next.Range.Start.Before(prev.Range.End):
		overlap := acq.Range{Start: next.Range.Start, End: earlier(prev.Range.End, next.Range.End)}
		return fmt.Errorf(
			"%w: %s and %s both supply %s — reject_conflict refuses overlapping source data rather than merging it",
			ErrTransitionInvalid, prev.Source, next.Source, overlap)
	case next.Range.Start.After(prev.Range.End):
		hole := acq.Range{Start: prev.Range.End, End: next.Range.Start}
		return fmt.Errorf("%w: nothing supplies %s between %s and %s — the segments must abut exactly",
			ErrTransitionInvalid, hole, prev.Source, next.Source)
	}
	return nil
}

// earlier is the earlier of two instants.
func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
