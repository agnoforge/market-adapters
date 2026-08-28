package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/agnos/agnoforge/internal/domain"
)

// ErrGapStatusNotSettable reports a status an operator may not move a Gap to.
// Only open, ignored and unrecoverable are an operator's to set: repaired is
// the detector's word that the Bars are there, so it is never asserted by
// hand. It is permanent — the same request will always be refused.
var ErrGapStatusNotSettable = errors.New("gap status not settable")

// Repair acquires the Bars a Gap is missing: a Backfill over exactly that
// Gap's range, in that Gap's Dataset. It is idempotent — running it again
// yields the same Dataset — and it returns as soon as the Backfill is
// registered, like every other Backfill.
//
// Nothing here sets the Gap's status. The Backfill's terminal gap detection
// runs over the range that landed, which is the Gap's range, so a Gap whose
// Bars are now all present becomes repaired on its own — including one an
// operator had settled as ignored or unrecoverable.
//
// It reports domain.ErrNotFound for a Gap that does not exist, and
// domain.ErrBackfillRunning when the Gap's Dataset already has a Backfill
// running.
func (s *Service) Repair(ctx context.Context, gapID int64) (BackfillStatus, error) {
	g, err := s.store.Gap(ctx, gapID)
	if err != nil {
		return BackfillStatus{}, fmt.Errorf("repair gap %d: %w", gapID, err)
	}
	return s.StartBackfill(ctx, BackfillRequest{
		Provider:  g.Dataset.Provider,
		Symbol:    g.Dataset.Symbol,
		Timeframe: g.Dataset.Timeframe,
		Range:     g.Range,
	})
}

// SetGapStatus records an operator's judgement on a Gap: open when it is
// still to be dealt with, ignored when its absence is accepted, unrecoverable
// when the Provider will never serve those Bars. Ignored and unrecoverable
// Gaps stop blocking Complete; the reason is stored as given.
//
// It reports ErrGapStatusNotSettable for repaired — only detection sets that,
// by observing the Bars — and for anything outside the four statuses, and
// domain.ErrNotFound for a Gap that does not exist.
func (s *Service) SetGapStatus(ctx context.Context, gapID int64, status domain.GapStatus, reason string) error {
	switch status {
	case domain.GapOpen, domain.GapIgnored, domain.GapUnrecoverable:
	default:
		return fmt.Errorf("%w: %q", ErrGapStatusNotSettable, status)
	}
	if err := s.store.SetGapStatus(ctx, gapID, status, reason); err != nil {
		return fmt.Errorf("set status of gap %d: %w", gapID, err)
	}
	return nil
}
