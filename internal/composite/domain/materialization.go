package domain

import (
	"slices"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
)

// Materialization is deriving the higher-timeframe bars of a Composite Dataset
// from its 1-minute timeline. This file holds the rules that decide *which*
// windows a Build touches and which of them the source data does not fully
// back; the derivation itself is SQL in the store adapter, over the source
// bars it never copies (ADR-0005).

// MaterializationVersion is the version of the derivation this build of the
// service performs. Every materialized bar is stamped with it, and so is the
// dataset row the bars belong to: a dataset whose bars were derived by another
// version is stale until the next Build restamps them.
//
// Bump it whenever a change makes this service derive a different bar from the
// same source data — a new aggregation rule, a corrected boundary, a new
// column. Nothing else may bump it: it is not a schema version.
const MaterializationVersion = 1

// IncompleteWindow is one materialization window that emitted a bar the source
// data does not fully back: an open Gap or a stretch the sources never supplied
// falls inside it, so the bar summarises less than the window claims.
//
// It is recorded, never hidden and never silently dropped: strict mode refuses
// readiness while one exists, and research mode is ready with them listed.
type IncompleteWindow struct {
	Timeframe Timeframe
	// Range is the window itself, half-open: [open time, next open time).
	Range acq.Range
}

// String renders an incomplete window the way a log line names one.
func (w IncompleteWindow) String() string {
	return w.Timeframe.String() + " " + w.Range.String()
}

// IncompleteWindows are the materialization windows of every frame in frames
// that emit a bar the data does not fully back, ordered by frame and then by
// time.
//
// A window is judged against what the dataset was asked for: `resolved` is the
// range the Build judged everything by, `available` the part of it the sources
// supply, and `gaps` the open Gaps inside that part. Three rules follow from
// that, and they are the honesty rule of materialization (decision 19):
//
//   - A window with no source bar at all — one that lies outside the available
//     range, or entirely inside a Gap — emits nothing and is not incomplete.
//     It is simply not materialized, and the coverage figures report it.
//   - A window that emits a bar, but whose part inside the resolved range holds
//     a Gap or a stretch the sources never supplied, is incomplete.
//   - The part of a window outside the resolved range is not missing data: the
//     dataset was never asked for it, so a week that starts before the
//     requested range is complete as far as this dataset is concerned.
func IncompleteWindows(frames []Timeframe, resolved, available acq.Range, gaps []Gap) []IncompleteWindow {
	holes := clip(gapRanges(gaps), resolved)
	// What is missing inside the resolved range: the stretches no Segment
	// supplies, and the open Gaps inside the stretches that are supplied.
	missing := acq.MergeRanges(append(clip(resolved.Subtract(available), resolved), holes...))
	// What really holds bars: the supplied range, minus its Gaps.
	covered := subtract(available.Intersect(resolved), holes)
	if len(missing) == 0 || len(covered) == 0 {
		return nil
	}

	var out []IncompleteWindow
	for _, tf := range frames {
		if !tf.Valid() || tf == TF1m {
			continue
		}
		seen := make(map[int64]bool)
		for _, m := range missing {
			// Only the two windows at the edges of a missing stretch can hold a
			// bar: every window between them lies wholly inside it and emits
			// nothing at all.
			for _, edge := range []time.Time{m.Start, m.End.Add(-time.Nanosecond)} {
				w := tf.Window(edge)
				if w.IsEmpty() || seen[w.Start.UnixMilli()] {
					continue
				}
				seen[w.Start.UnixMilli()] = true
				if overlapsAny(w, covered) {
					out = append(out, IncompleteWindow{Timeframe: tf, Range: w})
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b IncompleteWindow) int {
		if a.Timeframe != b.Timeframe {
			return a.Timeframe.Order() - b.Timeframe.Order()
		}
		return a.Range.Start.Compare(b.Range.Start)
	})
	return out
}

// gapRanges is the ranges of a set of Gaps.
func gapRanges(gaps []Gap) []acq.Range {
	out := make([]acq.Range, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, g.Range)
	}
	return out
}

// clip intersects every range with r, dropping what is left empty.
func clip(ranges []acq.Range, r acq.Range) []acq.Range {
	out := make([]acq.Range, 0, len(ranges))
	for _, piece := range ranges {
		if clipped := piece.Intersect(r); !clipped.IsEmpty() {
			out = append(out, clipped)
		}
	}
	return out
}

// subtract removes every hole from r, leaving the parts of it that survive.
func subtract(r acq.Range, holes []acq.Range) []acq.Range {
	out := []acq.Range{r}
	if r.IsEmpty() {
		out = nil
	}
	for _, hole := range holes {
		var next []acq.Range
		for _, piece := range out {
			next = append(next, piece.Subtract(hole)...)
		}
		out = next
	}
	return out
}

// overlapsAny reports whether r shares an instant with any of the ranges.
func overlapsAny(r acq.Range, ranges []acq.Range) bool {
	for _, other := range ranges {
		if r.Overlaps(other) {
			return true
		}
	}
	return false
}
