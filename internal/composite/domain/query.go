package domain

import (
	"fmt"
	"strings"
	"time"

	acq "github.com/agnos/agnoforge/internal/domain"
)

// The two rules a bars query is decided by, both of them properties of the
// declaration rather than of the request: which Timeframes a Composite Dataset
// serves, and which range it serves them over when the caller names neither
// bound.
//
// A consumer of this context asks for a dataset by name, a Timeframe and a
// range, and knows nothing about providers, gaps or storage (user story 26).
// Everything below is what turns that question into one this service can
// answer, or into the reason it cannot.

// Serves reports why a bars query for tf cannot be answered by this Composite
// Dataset, or nil when it can.
//
// Two kinds of Timeframe are servable and no others: the 1-minute composite
// timeline itself, which is the Segments' own frame and is always available,
// and the higher frames the declaration says to materialize. A canonical frame
// the declaration does not name has no bars — asking for it is answered with
// ErrTimeframeNotMaterialized naming the frames that do exist, never with an
// empty result that would read as "no bars in that range".
func (d Dataset) Serves(tf Timeframe) error {
	if !tf.Valid() {
		return fmt.Errorf("%w: unknown timeframe %q", ErrInvalidConfig, tf)
	}
	if tf == TF1m {
		return nil
	}
	for _, frame := range d.Config.Timeframes {
		if frame == tf {
			return nil
		}
	}
	return fmt.Errorf("%w: %q serves %s, not %q",
		ErrTimeframeNotMaterialized, d.Name, d.ServedTimeframes(), tf)
}

// ServedTimeframes spells every Timeframe this Composite Dataset can answer a
// bars query for, ascending: the 1-minute timeline first, then the frames the
// declaration materializes.
func (d Dataset) ServedTimeframes() string {
	frames := make([]string, 0, len(d.Config.Timeframes)+1)
	frames = append(frames, TF1m.String())
	for _, tf := range d.Config.Timeframes {
		frames = append(frames, tf.String())
	}
	return strings.Join(frames, ", ")
}

// QueryRange is the half-open range a bars query covers. A bound the caller
// omitted — the zero instant — is the dataset's own: the requested start it was
// declared with, and the Resolved End the last Build settled on. A backtester
// that names neither therefore gets exactly the timeline the dataset stands
// for, without having to know what `now` resolved to.
//
// An empty range is refused rather than answered with no bars: there are no
// instants in [t, t), so the question was not the one the caller meant to ask.
func (d Dataset) QueryRange(start, end time.Time) (acq.Range, error) {
	r := acq.Range{Start: start.UTC(), End: end.UTC()}
	if start.IsZero() {
		r.Start = d.Config.RequestedStart.UTC()
	}
	if end.IsZero() {
		r.End = d.ResolvedEnd.UTC()
	}
	if r.End.IsZero() {
		return acq.Range{}, fmt.Errorf("%w: %q has no resolved end to default to", ErrNotBuilt, d.Name)
	}
	if r.IsEmpty() {
		return acq.Range{}, fmt.Errorf("%w: start %s must be before end %s",
			ErrInvalidConfig, instant(r.Start), instant(r.End))
	}
	return r, nil
}
