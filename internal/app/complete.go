package app

import (
	"context"
	"fmt"

	"github.com/agnos/agnoforge/internal/domain"
)

// Completeness is the answer to the only question another service should ask
// before trusting a range: is it Complete, and if not, what stands in the way.
type Completeness struct {
	// Complete is true when the range lies entirely within the Dataset's
	// Coverage and no open Gap intersects it.
	Complete bool
	// Gaps are the open Gaps intersecting the range, sorted by start. A Gap
	// that only touches the range shares no instant with it and is not
	// listed.
	Gaps []domain.Gap
}

// IsComplete reports whether r can be trusted for the Dataset: r ⊆ Coverage
// and no open Gap intersects it. A range outside Coverage was never asked
// for, so nothing is known about it and it is not Complete. Gaps an operator
// settled — ignored, unrecoverable — and Gaps a Repair closed do not block
// Complete.
//
// An empty range is trivially Complete: there is no instant in it to be
// missing.
func (s *Service) IsComplete(ctx context.Context, id domain.DatasetID, r domain.Range) (Completeness, error) {
	if r.IsEmpty() {
		return Completeness{Complete: true}, nil
	}
	coverage, err := s.store.Coverage(ctx, id)
	if err != nil {
		return Completeness{}, fmt.Errorf("coverage of %s: %w", id, err)
	}
	covered := len(domain.SubtractRanges([]domain.Range{r}, coverage)) == 0

	open := domain.GapOpen
	gaps, err := s.store.Gaps(ctx, id, GapFilter{Status: &open, Range: &r})
	if err != nil {
		return Completeness{}, fmt.Errorf("gaps of %s: %w", id, err)
	}
	return Completeness{Complete: covered && len(gaps) == 0, Gaps: gaps}, nil
}
