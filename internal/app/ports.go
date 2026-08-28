package app

import (
	"context"
	"io"
	"iter"
	"time"

	"github.com/agnos/agnoforge/internal/domain"
)

// GapFilter narrows a Gaps query. A nil field means "no constraint on this
// field"; the zero GapFilter matches every Gap of the Dataset.
type GapFilter struct {
	// Status, when set, keeps only Gaps in that status.
	Status *domain.GapStatus
	// Range, when set, keeps only Gaps whose range intersects it.
	Range *domain.Range
}

// Store persists the Bars a Provider returned, the Coverage that was
// requested, and the Gaps found inside it. Every range it takes or returns is
// half-open on open_time.
//
// Implementations live under internal/adapters; nothing here knows which
// database is behind the port.
type Store interface {
	// UpsertBars writes the Bars of one Dataset, replacing any Bar already
	// stored at the same open_time. It is idempotent.
	UpsertBars(ctx context.Context, id domain.DatasetID, bars []domain.Bar) error

	// ExtendCoverage adds r to the Dataset's Coverage, union-merging it with
	// the ranges already recorded so touching and overlapping ranges become
	// one.
	ExtendCoverage(ctx context.Context, id domain.DatasetID, r domain.Range) error

	// Coverage returns the Dataset's Coverage, sorted by start and coalesced.
	Coverage(ctx context.Context, id domain.DatasetID) ([]domain.Range, error)

	// OpenTimes yields, in ascending order, the open_time of every Bar the
	// Dataset holds inside r. A failure is yielded once, with a zero time,
	// and ends the sequence.
	OpenTimes(ctx context.Context, id domain.DatasetID, r domain.Range) iter.Seq2[time.Time, error]

	// ReplaceOpenGaps deletes the Dataset's open Gaps intersecting r and
	// inserts gaps in their place. Gaps in any other status are left alone,
	// as are open Gaps outside r.
	ReplaceOpenGaps(ctx context.Context, id domain.DatasetID, r domain.Range, gaps []domain.Gap) error

	// Gaps returns the Dataset's Gaps matching f, sorted by start.
	Gaps(ctx context.Context, id domain.DatasetID, f GapFilter) ([]domain.Gap, error)

	// Gap returns one Gap by id, or an error wrapping domain.ErrNotFound.
	Gap(ctx context.Context, gapID int64) (domain.Gap, error)

	// SetGapStatus moves a Gap to s and records the operator's reason. It
	// returns an error wrapping domain.ErrNotFound when no such Gap exists.
	SetGapStatus(ctx context.Context, gapID int64, s domain.GapStatus, reason string) error

	// ExportParquet streams the Dataset's Bars inside r to w as a Parquet
	// file, ordered by open_time.
	ExportParquet(ctx context.Context, id domain.DatasetID, r domain.Range, w io.Writer) error
}
