package domain

import (
	"fmt"
	"slices"
	"time"
)

// Range is a half-open span [Start, End) over Bar open_time, in UTC.
// The zero Range is empty. Operations that return a Range normalise both
// bounds to UTC so results are directly comparable.
type Range struct {
	Start time.Time
	End   time.Time
}

// utc returns r with both bounds in UTC.
func (r Range) utc() Range {
	return Range{Start: r.Start.UTC(), End: r.End.UTC()}
}

// IsEmpty reports whether the Range covers no instant at all, which is the
// case when End is not after Start.
func (r Range) IsEmpty() bool { return !r.End.After(r.Start) }

// Contains reports whether t lies in [Start, End). An empty Range contains
// nothing.
func (r Range) Contains(t time.Time) bool {
	return !t.Before(r.Start) && t.Before(r.End)
}

// Overlaps reports whether the two ranges share at least one instant.
// Touching ranges do not overlap: [0,2) and [2,4) are adjacent, not shared.
func (r Range) Overlaps(other Range) bool {
	if r.IsEmpty() || other.IsEmpty() {
		return false
	}
	return r.Start.Before(other.End) && other.Start.Before(r.End)
}

// Touches reports whether the two ranges are adjacent with no instant between
// them: one ends exactly where the other starts. Empty ranges touch nothing.
func (r Range) Touches(other Range) bool {
	if r.IsEmpty() || other.IsEmpty() {
		return false
	}
	return r.End.Equal(other.Start) || other.End.Equal(r.Start)
}

// Union merges two ranges into one. It succeeds when the ranges overlap or
// touch, or when either is empty; disjoint ranges cannot be expressed as a
// single Range and report false.
func (r Range) Union(other Range) (Range, bool) {
	switch {
	case r.IsEmpty() && other.IsEmpty():
		return Range{}, true
	case r.IsEmpty():
		return other.utc(), true
	case other.IsEmpty():
		return r.utc(), true
	case !r.Overlaps(other) && !r.Touches(other):
		return Range{}, false
	}
	return Range{Start: earliest(r.Start, other.Start), End: latest(r.End, other.End)}.utc(), true
}

// Intersect returns the range covered by both. It returns the zero Range when
// they share no instant.
func (r Range) Intersect(other Range) Range {
	if !r.Overlaps(other) {
		return Range{}
	}
	return Range{Start: latest(r.Start, other.Start), End: earliest(r.End, other.End)}.utc()
}

// Subtract removes other from r, returning the remaining pieces in ascending
// order: zero pieces when other covers r, one piece when it trims an edge or
// misses entirely, two pieces when it punches a hole in the middle.
func (r Range) Subtract(other Range) []Range {
	if r.IsEmpty() {
		return nil
	}
	if !r.Overlaps(other) {
		return []Range{r.utc()}
	}
	var out []Range
	if r.Start.Before(other.Start) {
		out = append(out, Range{Start: r.Start, End: other.Start}.utc())
	}
	if other.End.Before(r.End) {
		out = append(out, Range{Start: other.End, End: r.End}.utc())
	}
	return out
}

// String renders the range in its half-open form.
func (r Range) String() string {
	return fmt.Sprintf("[%s,%s)", r.Start.UTC().Format(time.RFC3339), r.End.UTC().Format(time.RFC3339))
}

// MergeRanges returns the ranges sorted by start and coalesced: overlapping
// and touching ranges become one, empty ranges are dropped. The input is not
// modified.
func MergeRanges(ranges []Range) []Range {
	sorted := make([]Range, 0, len(ranges))
	for _, r := range ranges {
		if !r.IsEmpty() {
			sorted = append(sorted, r.utc())
		}
	}
	if len(sorted) == 0 {
		return nil
	}
	slices.SortFunc(sorted, func(a, b Range) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		return a.End.Compare(b.End)
	})

	out := []Range{sorted[0]}
	for _, r := range sorted[1:] {
		last := &out[len(out)-1]
		if merged, ok := last.Union(r); ok {
			*last = merged
			continue
		}
		out = append(out, r)
	}
	return out
}

// SubtractRanges removes every range in minus from base, returning the
// remainder sorted and coalesced.
func SubtractRanges(base, minus []Range) []Range {
	remaining := MergeRanges(base)
	for _, m := range MergeRanges(minus) {
		var next []Range
		for _, r := range remaining {
			next = append(next, r.Subtract(m)...)
		}
		remaining = next
		if len(remaining) == 0 {
			return nil
		}
	}
	if len(remaining) == 0 {
		return nil
	}
	return remaining
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
