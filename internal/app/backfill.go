package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/agnos/agnoforge/internal/domain"
)

// ErrUnknownProvider reports a Provider name this service was not built with.
// It is permanent: the set of Providers is fixed at construction.
var ErrUnknownProvider = errors.New("unknown provider")

// BackfillID identifies one Backfill for as long as this process lives.
type BackfillID string

// BackfillState is where a Backfill is in its lifecycle:
// running → completed | failed | cancelled.
type BackfillState string

const (
	// StateRunning means the Backfill is still acquiring Bars.
	StateRunning BackfillState = "running"
	// StateCompleted means the Provider yielded every page of the effective
	// range without failing.
	StateCompleted BackfillState = "completed"
	// StateFailed means a page or a write failed; LastError says why.
	StateFailed BackfillState = "failed"
	// StateCancelled means the Backfill was cancelled before the Provider ran
	// out of pages.
	StateCancelled BackfillState = "cancelled"
)

// BackfillRequest asks for every Bar of one Dataset over a half-open range.
type BackfillRequest struct {
	Provider  string
	Symbol    domain.Symbol
	Timeframe domain.Timeframe
	Range     domain.Range
}

// BackfillStatus is everything observable about one Backfill.
type BackfillStatus struct {
	ID      BackfillID
	Dataset domain.DatasetID
	// Range is the effective range: the requested one with its start clipped
	// up to the Provider's earliest available Bar.
	Range domain.Range
	State BackfillState
	// BarsDownloaded counts the Bars persisted so far.
	BarsDownloaded int64
	// Position is the open_time of the last Bar that landed.
	Position time.Time
	// LastError is the failure that ended the Backfill, empty otherwise.
	LastError string
}

// backfill is a registry entry: the status, the way to cancel the run, and
// the channel that closes once the run and its terminal work are finished.
type backfill struct {
	status BackfillStatus
	cancel context.CancelFunc
	done   chan struct{}
}

// ProviderInfo describes one Provider to a caller outside this package.
type ProviderInfo struct {
	Name       string
	Timeframes []domain.Timeframe
}

// Option configures a Service.
type Option func(*Service)

// WithLogger replaces the logger the Service reports Backfill progress on.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.log = l
		}
	}
}

// Service runs the use cases over the Provider and Store ports. It is safe
// for concurrent use.
type Service struct {
	store     Store
	providers map[string]Provider
	log       *slog.Logger

	// ponytail: in-memory registry, persist when a Backfill must survive restart
	mu        sync.Mutex
	backfills map[BackfillID]*backfill
}

// New builds a Service over one Store and the Providers it may acquire from,
// keyed by Provider name.
func New(store Store, providers []Provider, opts ...Option) *Service {
	s := &Service{
		store:     store,
		providers: make(map[string]Provider, len(providers)),
		log:       slog.Default(),
		backfills: make(map[BackfillID]*backfill),
	}
	for _, p := range providers {
		s.providers[p.Name()] = p
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Providers describes every Provider the Service was built with, by name.
func (s *Service) Providers() []ProviderInfo {
	out := make([]ProviderInfo, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, ProviderInfo{Name: p.Name(), Timeframes: p.SupportedTimeframes()})
	}
	slices.SortFunc(out, func(a, b ProviderInfo) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// StartBackfill validates the request, resolves the effective range, and runs
// the Backfill in the background. It returns as soon as the Backfill is
// registered, so the caller never waits on the Provider.
//
// It reports ErrUnknownProvider for a Provider it was not built with,
// domain.ErrUnsupportedTimeframe for a Timeframe that Provider does not
// offer, domain.ErrUnknownSymbol for a Symbol it does not know — all
// permanent, and none of them create a registry entry — and
// domain.ErrBackfillRunning when the Dataset already has one running.
func (s *Service) StartBackfill(ctx context.Context, req BackfillRequest) (BackfillStatus, error) {
	p, ok := s.providers[req.Provider]
	if !ok {
		return BackfillStatus{}, fmt.Errorf("%w: %q", ErrUnknownProvider, req.Provider)
	}
	if !slices.Contains(p.SupportedTimeframes(), req.Timeframe) {
		return BackfillStatus{}, fmt.Errorf("%w: %q on provider %q", domain.ErrUnsupportedTimeframe, req.Timeframe, p.Name())
	}

	// A permanent Provider failure here is the caller's answer, not a failed
	// record: nothing was started, so there is nothing to report on later.
	earliest, err := p.EarliestAvailable(ctx, req.Symbol)
	if err != nil {
		return BackfillStatus{}, fmt.Errorf("earliest available for %s: %w", req.Symbol, err)
	}

	// The effective range clips the start up to the Provider's first Bar.
	// Clipping the end down to the last closed Bar is the adapter's job, so
	// the end here is the one that was asked for.
	eff := domain.Range{Start: latest(req.Range.Start, earliest).UTC(), End: req.Range.End.UTC()}

	ds := domain.DatasetID{Provider: p.Name(), Symbol: req.Symbol, Timeframe: req.Timeframe}

	s.mu.Lock()
	for _, b := range s.backfills {
		if b.status.Dataset == ds && b.status.State == StateRunning {
			s.mu.Unlock()
			return BackfillStatus{}, fmt.Errorf("%w: %s", domain.ErrBackfillRunning, ds)
		}
	}
	// The HTTP request that started this Backfill ends immediately, so the
	// run gets a context of its own that only CancelBackfill ends.
	runCtx, cancel := context.WithCancel(context.Background())
	b := &backfill{
		status: BackfillStatus{ID: newBackfillID(), Dataset: ds, Range: eff, State: StateRunning},
		cancel: cancel,
		done:   make(chan struct{}),
	}
	s.backfills[b.status.ID] = b
	status := b.status
	s.mu.Unlock()

	s.log.Info("backfill started", "backfill", status.ID, "dataset", ds.String(), "range", eff.String())
	go s.run(runCtx, b, p)
	return status, nil
}

// Backfill returns the current status of one Backfill, or false when no such
// Backfill exists.
func (s *Service) Backfill(id BackfillID) (BackfillStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.backfills[id]
	if !ok {
		return BackfillStatus{}, false
	}
	return b.status, true
}

// CancelBackfill ends a running Backfill. The Bars and Coverage that already
// landed stay. Cancelling a Backfill that already reached a terminal state
// does nothing. It reports domain.ErrNotFound for an unknown id.
func (s *Service) CancelBackfill(id BackfillID) error {
	s.mu.Lock()
	b, ok := s.backfills[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: backfill %q", domain.ErrNotFound, id)
	}
	b.cancel()
	return nil
}

// Wait blocks until the Backfill has reached a terminal state and its
// terminal work — the final Coverage extension and gap detection — is done,
// then returns its final status. Polling Backfill cannot see that moment, so
// this is what a caller that must observe the finished Dataset uses.
func (s *Service) Wait(id BackfillID) (BackfillStatus, bool) {
	s.mu.Lock()
	b, ok := s.backfills[id]
	s.mu.Unlock()
	if !ok {
		return BackfillStatus{}, false
	}
	<-b.done
	return s.Backfill(id)
}

// run acquires every page of the effective range, persisting each one before
// asking for the next, and settles the Backfill in a terminal state.
func (s *Service) run(ctx context.Context, b *backfill, p Provider) {
	defer close(b.done)
	defer b.cancel()

	s.mu.Lock()
	ds, eff := b.status.Dataset, b.status.Range
	s.mu.Unlock()
	step := ds.Timeframe.Duration()

	var failure error
	for page, err := range p.Bars(ctx, ds.Symbol, ds.Timeframe, eff) {
		if err != nil {
			failure = err
			break
		}
		if len(page) == 0 {
			continue
		}
		if err := s.store.UpsertBars(ctx, ds, page); err != nil {
			failure = fmt.Errorf("upsert bars: %w", err)
			break
		}
		// Coverage is extended only for a page that is already persisted, so
		// it never claims Bars that are not there.
		last := page[len(page)-1].OpenTime.UTC()
		if err := s.store.ExtendCoverage(ctx, ds, domain.Range{Start: page[0].OpenTime.UTC(), End: last.Add(step)}); err != nil {
			failure = fmt.Errorf("extend coverage: %w", err)
			break
		}
		s.mu.Lock()
		b.status.BarsDownloaded += int64(len(page))
		b.status.Position = last
		s.mu.Unlock()
	}

	// Terminal work must happen even when the run was cancelled, so it runs
	// on a context of its own.
	term := context.Background()

	s.mu.Lock()
	position := b.status.Position
	s.mu.Unlock()

	state, lastErr := StateCompleted, ""
	switch {
	case ctx.Err() != nil:
		state = StateCancelled
	case failure != nil:
		state, lastErr = StateFailed, failure.Error()
	}
	if state == StateCompleted {
		// A completed Backfill covers everything that was asked for, even the
		// instants the Provider had no Bar for: those become Gaps.
		if err := s.store.ExtendCoverage(term, ds, eff); err != nil {
			state, lastErr = StateFailed, fmt.Errorf("extend coverage: %w", err).Error()
		}
	}
	landed := eff
	if state != StateCompleted {
		landed = domain.Range{Start: eff.Start, End: position.Add(step)}
	}

	s.mu.Lock()
	b.status.State, b.status.LastError = state, lastErr
	status := b.status
	s.mu.Unlock()

	s.log.Info("backfill finished",
		"backfill", status.ID, "dataset", ds.String(), "state", string(state),
		"bars_downloaded", status.BarsDownloaded, "last_error", lastErr)

	gaps, err := s.DetectGaps(term, ds, landed)
	if err != nil {
		s.log.Error("detect gaps", "backfill", status.ID, "dataset", ds.String(), "err", err)
		return
	}
	s.log.Info("gaps detected", "backfill", status.ID, "dataset", ds.String(), "range", landed.String(), "gaps", len(gaps))
}

// newBackfillID returns 16 random bytes in hex.
func newBackfillID() BackfillID {
	var b [16]byte
	// crypto/rand.Read never fails; it crashes the program instead.
	_, _ = rand.Read(b[:])
	return BackfillID(hex.EncodeToString(b[:]))
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
