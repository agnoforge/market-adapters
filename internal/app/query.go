package app

import (
	"context"
	"fmt"
	"io"

	"github.com/agnos/agnoforge/internal/domain"
)

// Query is the read side of the service: what a Dataset covers, where its
// Gaps are, and the Bars themselves. None of it ever refuses an incomplete
// range — IsComplete is how a caller learns the range is incomplete, and the
// Bars come back either way.
//
// The methods below are pass-throughs to the Store port. They exist so an
// inbound adapter depends on the use cases alone and never on the Store.

// Coverage returns the Dataset's Coverage, sorted by start and coalesced.
func (s *Service) Coverage(ctx context.Context, id domain.DatasetID) ([]domain.Range, error) {
	coverage, err := s.store.Coverage(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("coverage of %s: %w", id, err)
	}
	return coverage, nil
}

// Gaps returns the Dataset's Gaps matching f, sorted by start. The zero
// GapFilter returns every Gap of the Dataset.
func (s *Service) Gaps(ctx context.Context, id domain.DatasetID, f GapFilter) ([]domain.Gap, error) {
	gaps, err := s.store.Gaps(ctx, id, f)
	if err != nil {
		return nil, fmt.Errorf("gaps of %s: %w", id, err)
	}
	return gaps, nil
}

// Gap returns one Gap by id. It reports domain.ErrNotFound when no such Gap
// exists.
func (s *Service) Gap(ctx context.Context, gapID int64) (domain.Gap, error) {
	g, err := s.store.Gap(ctx, gapID)
	if err != nil {
		return domain.Gap{}, fmt.Errorf("gap %d: %w", gapID, err)
	}
	return g, nil
}

// Bars returns the Dataset's Bars inside r, ordered by open_time. An
// incomplete range is served, not refused: ask IsComplete about it.
func (s *Service) Bars(ctx context.Context, id domain.DatasetID, r domain.Range) ([]domain.Bar, error) {
	bars, err := s.store.Bars(ctx, id, r)
	if err != nil {
		return nil, fmt.Errorf("bars of %s: %w", id, err)
	}
	return bars, nil
}

// ExportParquet streams the Dataset's Bars inside r to w as a Parquet file,
// ordered by open_time. Like Bars, it serves an incomplete range.
func (s *Service) ExportParquet(ctx context.Context, id domain.DatasetID, r domain.Range, w io.Writer) error {
	if err := s.store.ExportParquet(ctx, id, r, w); err != nil {
		return fmt.Errorf("export %s: %w", id, err)
	}
	return nil
}
