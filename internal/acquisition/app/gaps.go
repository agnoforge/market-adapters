package app

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// DetectGaps records where Bars are expected inside r but absent, and returns
// the open Gaps the Dataset now has in r.
//
// Only Coverage can be judged: a range that was never asked for is unknown,
// not missing. Inside the Coverage that r intersects, a Gap is what the
// Provider's TradingCalendar expects, minus the open_times the Dataset holds,
// minus the ranges an operator already settled as ignored or unrecoverable.
// One Gap is recorded per run of consecutive expected open_times that are
// absent — consecutive in the calendar's sequence, so a market-closed period
// neither becomes a Gap nor breaks a run across it.
//
// Any Gap that r touches whose range the Dataset now holds in full becomes
// repaired first, whatever status it was in, so a Repair that landed its Bars
// closes the record it was aimed at. The open Gaps in r are then replaced by
// what this run found, so a Gap that has since been filled disappears.
func (s *Service) DetectGaps(ctx context.Context, id domain.DatasetID, r domain.Range) ([]domain.Gap, error) {
	ctx, span := tracer().Start(ctx, "app.DetectGaps",
		trace.WithAttributes(append(datasetAttrs(id), rangeAttrs(r)...)...))
	defer span.End()

	if r.IsEmpty() {
		span.SetAttributes(gapCountKey.Int(0))
		return nil, nil
	}
	p, ok := s.providers[id.Provider]
	if !ok {
		return nil, fail(span, fmt.Errorf("%w: %q", ErrUnknownProvider, id.Provider))
	}
	step := id.Timeframe.Duration()
	if step <= 0 {
		return nil, fail(span, fmt.Errorf("%w: %q", domain.ErrUnsupportedTimeframe, id.Timeframe))
	}

	// ReplaceOpenGaps drops every open Gap r intersects, so a Gap straddling
	// r's edge must be re-detected in full: widen r to their hull.
	open := domain.GapOpen
	straddling, err := s.store.Gaps(ctx, id, GapFilter{Status: &open, Range: &r})
	if err != nil {
		return nil, fail(span, fmt.Errorf("gaps of %s: %w", id, err))
	}
	for _, g := range straddling {
		if g.Range.Start.Before(r.Start) {
			r.Start = g.Range.Start
		}
		if g.Range.End.After(r.End) {
			r.End = g.Range.End
		}
	}

	coverage, err := s.store.Coverage(ctx, id)
	if err != nil {
		return nil, fail(span, fmt.Errorf("coverage of %s: %w", id, err))
	}
	calendar := p.Calendar(id.Symbol)
	settled, err := s.settleGaps(ctx, id, r, calendar)
	if err != nil {
		return nil, fail(span, err)
	}

	var found []domain.Gap
	for _, covered := range coverage {
		piece := r.Intersect(covered)
		if piece.IsEmpty() {
			continue
		}
		present, err := s.openTimes(ctx, id, piece)
		if err != nil {
			return nil, fail(span, err)
		}
		// A run ends at the first expected open_time that is present or
		// settled; the Gap it becomes spans from its first missing open_time
		// to one Timeframe past its last.
		var first, last time.Time
		running := false
		flush := func() {
			if !running {
				return
			}
			found = append(found, domain.Gap{
				Dataset: id,
				Range:   domain.Range{Start: first, End: last.Add(step)},
				Status:  domain.GapOpen,
			})
			running = false
		}
		for expected := range calendar.Expected(piece, id.Timeframe) {
			if present[expected.UnixMilli()] || covers(settled, expected) {
				flush()
				continue
			}
			if !running {
				first, running = expected, true
			}
			last = expected
		}
		flush()
	}

	if err := s.store.ReplaceOpenGaps(ctx, id, r, found); err != nil {
		return nil, fail(span, fmt.Errorf("replace open gaps of %s: %w", id, err))
	}

	recorded, err := s.store.Gaps(ctx, id, GapFilter{Status: &open, Range: &r})
	if err != nil {
		return nil, fail(span, fmt.Errorf("gaps of %s: %w", id, err))
	}
	span.SetAttributes(gapCountKey.Int(len(recorded)))
	return recorded, nil
}

// settleGaps brings the Gaps that r touches up to date with the Bars the
// Dataset now holds: one whose range is present in full becomes repaired,
// whatever status it was in. It returns the ranges that are still settled —
// ignored or unrecoverable — where Bars are therefore not expected. A
// repaired Gap excludes nothing: its Bars are there.
func (s *Service) settleGaps(ctx context.Context, id domain.DatasetID, r domain.Range, calendar domain.TradingCalendar) ([]domain.Range, error) {
	existing, err := s.store.Gaps(ctx, id, GapFilter{Range: &r})
	if err != nil {
		return nil, fmt.Errorf("gaps of %s: %w", id, err)
	}
	var settled []domain.Range
	for _, g := range existing {
		if g.Status == domain.GapRepaired {
			continue
		}
		filled, err := s.isFilled(ctx, id, g.Range, calendar)
		if err != nil {
			return nil, err
		}
		if filled {
			if err := s.store.SetGapStatus(ctx, g.ID, domain.GapRepaired, ""); err != nil {
				return nil, fmt.Errorf("repair gap %d of %s: %w", g.ID, id, err)
			}
			continue
		}
		if g.Status == domain.GapIgnored || g.Status == domain.GapUnrecoverable {
			settled = append(settled, g.Range)
		}
	}
	return domain.MergeRanges(settled), nil
}

// isFilled reports whether the Dataset holds every open_time the calendar
// expects in r. A range the calendar expects nothing in is not filled: there
// was never anything to land there.
func (s *Service) isFilled(ctx context.Context, id domain.DatasetID, r domain.Range, calendar domain.TradingCalendar) (bool, error) {
	present, err := s.openTimes(ctx, id, r)
	if err != nil {
		return false, err
	}
	expected := 0
	for t := range calendar.Expected(r, id.Timeframe) {
		if !present[t.UnixMilli()] {
			return false, nil
		}
		expected++
	}
	return expected > 0, nil
}

// openTimes reads every open_time the Dataset holds inside r into a set keyed
// by epoch milliseconds.
func (s *Service) openTimes(ctx context.Context, id domain.DatasetID, r domain.Range) (map[int64]bool, error) {
	present := make(map[int64]bool)
	for t, err := range s.store.OpenTimes(ctx, id, r) {
		if err != nil {
			return nil, fmt.Errorf("open times of %s: %w", id, err)
		}
		present[t.UnixMilli()] = true
	}
	return present, nil
}

// covers reports whether t falls inside any of the ranges.
func covers(ranges []domain.Range, t time.Time) bool {
	for _, r := range ranges {
		if r.Contains(t) {
			return true
		}
	}
	return false
}
