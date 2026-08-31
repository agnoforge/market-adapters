package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// Catch-up is the phase of a Build that makes the source data exist before
// anything is assembled over it: whatever part of the requested range
// acquisition has not acquired is requested from the base provider — head and
// tail alike — and waited for, and the Gaps inside what is then covered are
// repaired. Only after that is readiness judged (decision 24, 25).
//
// The researcher never talks to acquisition: this context orchestrates it
// through the control-plane port, and everything acquisition can answer that is
// not a failure — a provider floor, a backfill already running for the same
// source Dataset — is handled here rather than surfaced as a build failure.

const (
	// defaultRetryEvery and defaultRetryAttempts are how long a Build waits out
	// a backfill of the same source Dataset that acquisition is already
	// running. Five minutes of patience, then the Build gives up and says so.
	defaultRetryEvery    = 2 * time.Second
	defaultRetryAttempts = 150
)

// ensure extends the base source's coverage over the resolved range: every part
// of it acquisition has not acquired is asked of the base provider and waited
// for, and the coverage that results is returned.
//
// What the provider cannot supply — the head below its earliest-available
// floor, a tail it does not reach — is not a failure here: the coverage simply
// comes back short, and the mode decides what that means. An acquisition
// failure is, because a Build that could not ask has not judged anything.
func (s *Service) ensure(ctx context.Context, src domain.Source, resolved acq.Range) ([]acq.Range, error) {
	ctx, span := tracer().Start(ctx, "composite.ensure", trace.WithAttributes(
		layerKey.String(layer),
		rangeStartKey.String(instant(resolved.Start)),
		rangeEndKey.String(instant(resolved.End))))
	defer span.End()

	coverage, err := s.acquisition.Coverage(ctx, src)
	if err != nil {
		return nil, fail(span, fmt.Errorf("coverage of the base source %s: %w", src, err))
	}
	missing := acq.SubtractRanges([]acq.Range{resolved}, coverage)
	span.SetAttributes(missingCountKey.Int(len(missing)))
	if len(missing) == 0 {
		return coverage, nil
	}

	filled := 0
	for _, r := range missing {
		landed, err := s.acquire(ctx,
			fmt.Sprintf("backfilling %s over %s", src, r),
			func() (BackfillHandle, error) { return s.acquisition.StartBackfill(ctx, src, r) })
		if err != nil {
			return nil, fail(span, err)
		}
		if landed {
			filled++
			s.log.Info("base coverage extended", "source", src.String(), "range", r.String())
			continue
		}
		s.log.Info("the base provider has nothing to acquire",
			"source", src.String(), "range", r.String())
	}
	span.SetAttributes(filledCountKey.Int(filled))
	if filled == 0 {
		return coverage, nil
	}

	// The backfills have landed, so what the source covers is a new answer.
	coverage, err = s.acquisition.Coverage(ctx, src)
	if err != nil {
		return nil, fail(span, fmt.Errorf("coverage of the base source %s after its backfills: %w", src, err))
	}
	return coverage, nil
}

// repair closes what it can of the Gaps inside the range the base source now
// covers, and answers with the completeness that follows. The Gaps that survive
// it are the ones the mode judges: strict refuses them, research records them.
//
// A Gap the provider cannot fill is not a failure — the repair is asked for and
// the Gap stays open, which is exactly what a researcher needs to see.
func (s *Service) repair(ctx context.Context, src domain.Source, ensured, resolved acq.Range) (Completeness, error) {
	ctx, span := tracer().Start(ctx, "composite.repair", trace.WithAttributes(
		layerKey.String(layer),
		rangeStartKey.String(instant(ensured.Start)),
		rangeEndKey.String(instant(ensured.End))))
	defer span.End()

	gaps, err := s.acquisition.DetectGaps(ctx, src, ensured)
	if err != nil {
		return Completeness{}, fail(span, fmt.Errorf("detecting the gaps of %s in %s: %w", src, ensured, err))
	}
	span.SetAttributes(gapCountKey.Int(len(gaps)))

	repaired := 0
	for _, g := range gaps {
		gap := g
		landed, err := s.acquire(ctx,
			fmt.Sprintf("repairing the gap of %s over %s", src, gap.Range),
			func() (BackfillHandle, error) { return s.acquisition.RepairGap(ctx, gap) })
		if err != nil {
			return Completeness{}, fail(span, err)
		}
		if landed {
			repaired++
			continue
		}
		s.log.Info("a gap could not be repaired: the provider has nothing there",
			"source", src.String(), "gap", gap.Range.String())
	}
	span.SetAttributes(repairedCountKey.Int(repaired))

	// Detection alone can change which Gaps are open — one whose bars have
	// since landed closes — so readiness is judged on a fresh answer either way.
	complete, err := s.acquisition.Completeness(ctx, src, resolved)
	if err != nil {
		return Completeness{}, fail(span, fmt.Errorf("completeness of the base source %s after its repairs: %w", src, err))
	}
	return complete, nil
}

// acquire runs one backfill request through to its end, reporting whether it
// landed anything.
//
// It is where the two answers that are not failures are absorbed. A source
// Dataset that already has a backfill running is waited for and asked again —
// acquisition refuses the second one, and a researcher should never see that
// race. A provider with nothing in the range answers false: there was nothing
// to land. Anything else — a request that could not be made, a backfill that
// ended terminally failed — is returned with the acquisition error inside it,
// and fails the Build.
func (s *Service) acquire(ctx context.Context, what string, request func() (BackfillHandle, error)) (bool, error) {
	var handle BackfillHandle
	for attempt := 1; ; attempt++ {
		h, err := request()
		if err == nil {
			handle = h
			break
		}
		if errors.Is(err, ErrNothingToAcquire) {
			return false, nil
		}
		if !errors.Is(err, ErrBackfillBusy) {
			return false, fmt.Errorf("%s: %w", what, err)
		}
		if attempt >= s.retryAttempts {
			return false, fmt.Errorf("%s: gave up after %d attempts: %w", what, attempt, err)
		}
		s.log.Info("waiting for a backfill of the same source dataset to finish",
			"what", what, "attempt", attempt)
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("%s: %w", what, ctx.Err())
		case <-time.After(s.retryEvery):
		}
	}
	if err := s.acquisition.WaitBackfill(ctx, handle); err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	return true, nil
}
