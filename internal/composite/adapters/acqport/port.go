// Package acqport implements the composite AcquisitionPort over the Market
// Data Acquisition application service, in-process.
//
// It is the production adapter for the control plane the composite context
// depends on: coverage, completeness, backfill start and wait, gap detection
// and repair. Acquisition knows nothing about it — the port is defined by its
// consumer — and acquisition's own surface is unchanged by it.
//
// Two things happen here and nowhere else: a composite Source becomes an
// acquisition DatasetID (the one place a composite Timeframe is converted,
// ADR-0004), and acquisition's vocabulary — Gap rows, backfill statuses — is
// translated into the composite one. Bars are deliberately absent: they are the
// data plane, read in SQL from the shared database by the composite store
// (ADR-0005).
package acqport

import (
	"context"
	"fmt"

	acqapp "github.com/agnos/agnoforge/internal/app"
	compositeapp "github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// Port satisfies the port the composite use cases depend on.
var _ compositeapp.AcquisitionPort = (*Port)(nil)

// Port is the acquisition application service seen through composite eyes.
type Port struct {
	svc *acqapp.Service
}

// New wraps the acquisition service. The call is in-process: there is no HTTP
// hop and no second database between the two contexts.
func New(svc *acqapp.Service) *Port { return &Port{svc: svc} }

// Coverage returns the ranges the source Dataset has been acquired over.
func (p *Port) Coverage(ctx context.Context, src domain.Source) ([]acq.Range, error) {
	id, err := datasetID(src)
	if err != nil {
		return nil, err
	}
	return p.svc.Coverage(ctx, id)
}

// Completeness reports whether r can be trusted for the source Dataset, and
// which open Gaps stand in the way.
func (p *Port) Completeness(ctx context.Context, src domain.Source, r acq.Range) (compositeapp.Completeness, error) {
	id, err := datasetID(src)
	if err != nil {
		return compositeapp.Completeness{}, err
	}
	answer, err := p.svc.IsComplete(ctx, id, r)
	if err != nil {
		return compositeapp.Completeness{}, err
	}
	return compositeapp.Completeness{Complete: answer.Complete, Gaps: gaps(answer.Gaps)}, nil
}

// StartBackfill asks acquisition to acquire r for the source Dataset.
func (p *Port) StartBackfill(ctx context.Context, src domain.Source, r acq.Range) (compositeapp.BackfillHandle, error) {
	timeframe, err := timeframe(src)
	if err != nil {
		return "", err
	}
	status, err := p.svc.StartBackfill(ctx, acqapp.BackfillRequest{
		Provider: src.Provider, Symbol: src.Symbol, Timeframe: timeframe, Range: r,
	})
	if err != nil {
		return "", err
	}
	return compositeapp.BackfillHandle(status.ID), nil
}

// WaitBackfill blocks until the backfill finishes, reporting the acquisition
// failure when it did not complete.
func (p *Port) WaitBackfill(_ context.Context, h compositeapp.BackfillHandle) error {
	status, ok := p.svc.Wait(acqapp.BackfillID(h))
	if !ok {
		return fmt.Errorf("acquisition knows no backfill %q", h)
	}
	if status.State == acqapp.StateCompleted {
		return nil
	}
	if status.LastError != "" {
		return fmt.Errorf("backfill of %s %s: %s", status.Dataset, status.State, status.LastError)
	}
	return fmt.Errorf("backfill of %s %s", status.Dataset, status.State)
}

// DetectGaps records where bars are expected inside r and absent, and returns
// the open Gaps the source Dataset now has there.
func (p *Port) DetectGaps(ctx context.Context, src domain.Source, r acq.Range) ([]domain.Gap, error) {
	id, err := datasetID(src)
	if err != nil {
		return nil, err
	}
	found, err := p.svc.DetectGaps(ctx, id, r)
	if err != nil {
		return nil, err
	}
	return gaps(found), nil
}

// RepairGap asks acquisition to re-acquire one Gap's range.
func (p *Port) RepairGap(ctx context.Context, g domain.Gap) (compositeapp.BackfillHandle, error) {
	status, err := p.svc.Repair(ctx, g.ID)
	if err != nil {
		return "", err
	}
	return compositeapp.BackfillHandle(status.ID), nil
}

// datasetID is the one translation of a composite Source into the Dataset
// acquisition knows.
func datasetID(src domain.Source) (acq.DatasetID, error) {
	tf, err := timeframe(src)
	if err != nil {
		return acq.DatasetID{}, err
	}
	return acq.DatasetID{Provider: src.Provider, Symbol: src.Symbol, Timeframe: tf}, nil
}

// timeframe converts a composite Timeframe at the port boundary. The calendar
// frames cannot cross it — and never need to: every Source this context asks
// acquisition about is a 1-minute one (ADR-0004).
func timeframe(src domain.Source) (acq.Timeframe, error) {
	tf, ok := src.Timeframe.Acquisition()
	if !ok {
		return "", fmt.Errorf("%w: acquisition cannot express the timeframe %q of %s",
			domain.ErrInvalidConfig, src.Timeframe, src)
	}
	return tf, nil
}

// gaps translates acquisition's Gap rows into the ranges this context records.
func gaps(found []acq.Gap) []domain.Gap {
	if len(found) == 0 {
		return nil
	}
	out := make([]domain.Gap, 0, len(found))
	for _, g := range found {
		out = append(out, domain.Gap{ID: g.ID, Range: g.Range})
	}
	return out
}
