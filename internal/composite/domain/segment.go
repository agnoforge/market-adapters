package domain

import (
	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
)

// SegmentKind says where a Segment's bars come from. It is a closed set: a
// composite timeline is base data, the catch-up that continues it, and — one
// day — the live continuation the model already reserves room for (decision 1).
type SegmentKind string

const (
	// SegmentBase is the slice the base source supplies.
	SegmentBase SegmentKind = "base"
	// SegmentCatchUp is the tail a catch-up source supplies past what the base
	// one can.
	SegmentCatchUp SegmentKind = "catch_up"
	// SegmentLive is reserved for the live continuation and is never assembled
	// in v1. It exists so adding live data later extends this model instead of
	// reworking it.
	SegmentLive SegmentKind = "live"
)

// segmentKinds is the closed set, in the order a timeline uses them.
var segmentKinds = []SegmentKind{SegmentBase, SegmentCatchUp, SegmentLive}

// Valid reports whether k is one of the reserved kinds.
func (k SegmentKind) Valid() bool {
	for _, known := range segmentKinds {
		if k == known {
			return true
		}
	}
	return false
}

// String returns the kind as the API spells it.
func (k SegmentKind) String() string { return string(k) }

// Segment is a contiguous, provider-attributed slice of a composite timeline,
// computed by a Build. It is metadata over source bars, never a copy of them
// (ADR-0005): the Range says which instants of the composite timeline this
// Source answers for.
//
// The ordered Segment list is this context's provenance record — "where did
// this bar come from?" is answered by locating its open time in the list.
type Segment struct {
	Kind   SegmentKind
	Source Source
	// Range is the half-open range of composite open times this Segment
	// covers.
	Range acq.Range
}

// String renders a Segment the way a log line names one.
func (s Segment) String() string {
	return s.Kind.String() + " " + s.Source.String() + " " + s.Range.String()
}
