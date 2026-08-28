package app

import (
	"context"
	"fmt"
	"time"

	"github.com/agnos/agnoforge/internal/domain"
)

// DetectGaps records where Bars are expected inside r but absent, and returns
// the Gaps it recorded.
//
// Only Coverage can be judged: a range that was never asked for is unknown,
// not missing. Inside the Coverage that r intersects, a Gap is what the
// Provider's TradingCalendar expects, minus the open_times the Dataset holds,
// minus the ranges an operator already settled as ignored or unrecoverable.
// The open Gaps in r are replaced by what this run found, so a Gap that has
// since been filled disappears.
func (s *Service) DetectGaps(ctx context.Context, id domain.DatasetID, r domain.Range) ([]domain.Gap, error) {
	if r.IsEmpty() {
		return nil, nil
	}
	p, ok := s.providers[id.Provider]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, id.Provider)
	}
	step := id.Timeframe.Duration()
	if step <= 0 {
		return nil, fmt.Errorf("%w: %q", domain.ErrUnsupportedTimeframe, id.Timeframe)
	}

	coverage, err := s.store.Coverage(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("coverage of %s: %w", id, err)
	}
	settled, err := s.settledRanges(ctx, id, r)
	if err != nil {
		return nil, err
	}

	calendar := p.Calendar(id.Symbol)
	var gaps []domain.Gap
	for _, covered := range coverage {
		piece := r.Intersect(covered)
		if piece.IsEmpty() {
			continue
		}
		present, err := s.openTimes(ctx, id, piece)
		if err != nil {
			return nil, err
		}
		// Missing open_times arrive in ascending order, so consecutive ones —
		// exactly one Timeframe apart — coalesce into a single Gap.
		var first, last time.Time
		flush := func() {
			if first.IsZero() {
				return
			}
			gaps = append(gaps, domain.Gap{
				Dataset: id,
				Range:   domain.Range{Start: first, End: last.Add(step)},
				Status:  domain.GapOpen,
			})
			first, last = time.Time{}, time.Time{}
		}
		for expected := range calendar.Expected(piece, id.Timeframe) {
			if present[expected.UnixMilli()] || covers(settled, expected) {
				flush()
				continue
			}
			if first.IsZero() {
				first = expected
			} else if !expected.Equal(last.Add(step)) {
				flush()
				first = expected
			}
			last = expected
		}
		flush()
	}

	if err := s.store.ReplaceOpenGaps(ctx, id, r, gaps); err != nil {
		return nil, fmt.Errorf("replace open gaps of %s: %w", id, err)
	}
	return gaps, nil
}

// settledRanges returns the ranges an operator has already settled — ignored
// or unrecoverable — that intersect r. Bars are not expected there.
func (s *Service) settledRanges(ctx context.Context, id domain.DatasetID, r domain.Range) ([]domain.Range, error) {
	all, err := s.store.Gaps(ctx, id, GapFilter{Range: &r})
	if err != nil {
		return nil, fmt.Errorf("gaps of %s: %w", id, err)
	}
	var settled []domain.Range
	for _, g := range all {
		if g.Status == domain.GapIgnored || g.Status == domain.GapUnrecoverable {
			settled = append(settled, g.Range)
		}
	}
	return domain.MergeRanges(settled), nil
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
