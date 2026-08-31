package domain

import (
	"math"
	"time"

	acq "github.com/agnos/agnoforge/internal/domain"
)

// Gap is one open acquisition Gap as this context records it: a range inside
// the composite's own timeline where source bars are expected and absent. The
// ID is acquisition's, so a repair can name the same Gap acquisition knows.
type Gap struct {
	ID    int64
	Range acq.Range
}

// Quality is what a Build computed about a Composite Dataset: enough for a
// researcher to judge fitness before running anything against it, and enough
// to explain afterwards what that build actually used.
//
// Every judgement here is made against the Resolved End, never against the
// literal `now` a configuration may have declared.
type Quality struct {
	// RequestedStart and RequestedEnd are the range as it was declared —
	// RequestedEnd possibly the literal `now`.
	RequestedStart time.Time
	RequestedEnd   RequestedEnd
	// ResolvedEnd is the concrete end this Build judged everything against.
	ResolvedEnd time.Time
	// AvailableStart and AvailableEnd are the range the sources actually
	// supply inside the resolved one. Both are zero when they supply nothing.
	AvailableStart time.Time
	AvailableEnd   time.Time
	// ExpectedBars is how many 1-minute bars the resolved range holds;
	// ActualBars is how many of them the sources really have.
	ExpectedBars int64
	ActualBars   int64
	// OpenGaps are the open Gaps intersecting the resolved range, ascending.
	OpenGaps []Gap
	// Mode is the readiness rule this build was judged by.
	Mode Mode
	// LastBuildAt is when the Build that computed this ran.
	LastBuildAt time.Time
}

// NewQuality starts the Quality of one Build: what was declared, the end the
// Build resolved it to, and when it ran. The counts, the available range and
// the gaps are filled in as the Build learns them.
func NewQuality(cfg Config, resolvedEnd, at time.Time) Quality {
	return Quality{
		RequestedStart: cfg.RequestedStart.UTC(),
		RequestedEnd:   cfg.RequestedEnd,
		ResolvedEnd:    resolvedEnd.UTC(),
		ExpectedBars:   MinuteBars(acq.Range{Start: cfg.RequestedStart, End: resolvedEnd}),
		Mode:           cfg.Mode,
		LastBuildAt:    at.UTC(),
	}
}

// Coverage is the percentage of the expected 1-minute bars the sources really
// have, rounded to two decimals. A range that expects nothing is 0.
func (q Quality) Coverage() float64 {
	if q.ExpectedBars <= 0 {
		return 0
	}
	return math.Round(float64(q.ActualBars)/float64(q.ExpectedBars)*10000) / 100
}

// OpenGapCount is how many open Gaps intersect the resolved range.
func (q Quality) OpenGapCount() int { return len(q.OpenGaps) }

// Strict reports whether this dataset was judged by the strict readiness rule.
// It is the one boolean that separates a strict dataset from a research one
// (decision 8), so a research dataset can never be mistaken for a complete one.
func (q Quality) Strict() bool { return q.Mode != ModeResearch }

// Available is the range the sources supply inside the resolved one, or the
// empty range when they supply nothing.
func (q Quality) Available() acq.Range {
	return acq.Range{Start: q.AvailableStart, End: q.AvailableEnd}
}

// MinuteBars is how many 1-minute bars a half-open range holds. It is the
// expected count of a composite timeline, which is always a 1-minute one.
func MinuteBars(r acq.Range) int64 {
	if r.IsEmpty() {
		return 0
	}
	return int64(r.End.Sub(r.Start) / time.Minute)
}
