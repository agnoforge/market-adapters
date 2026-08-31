package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/composite/domain"
)

// Option configures a Service.
type Option func(*Service)

// WithClock replaces the clock the created and updated instants are read
// from.
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithLogger replaces the logger the Service reports on.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.log = l
		}
	}
}

// Service runs the Composite Market Dataset use cases over the Store port. It
// is safe for concurrent use.
type Service struct {
	store Store
	log   *slog.Logger
	now   func() time.Time
}

// New builds the use cases over a Store.
func New(store Store, opts ...Option) *Service {
	s := &Service{store: store, log: slog.Default(), now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create declares a new Composite Dataset. The configuration is normalised and
// validated before anything is written, so a refused declaration leaves no
// trace; a name that is already taken fails with domain.ErrDuplicateName.
//
// A new dataset is a draft: declaring one builds nothing.
func (s *Service) Create(ctx context.Context, name domain.Name, cfg domain.Config) (domain.Dataset, error) {
	ctx, span := tracer().Start(ctx, "composite.Create", trace.WithAttributes(datasetAttrs(name)...))
	defer span.End()

	if _, err := domain.ParseName(name.String()); err != nil {
		return domain.Dataset{}, fail(span, err)
	}
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return domain.Dataset{}, fail(span, err)
	}
	now := s.now().UTC()
	d := domain.Dataset{
		Name:      name,
		Config:    cfg,
		State:     domain.StateDraft,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateDataset(ctx, d); err != nil {
		return domain.Dataset{}, fail(span, err)
	}
	span.SetAttributes(stateKey.String(d.State.String()))
	s.log.Info("composite dataset created", "dataset", name.String(), "instrument", cfg.Instrument.String())
	return d, nil
}

// Dataset returns one Composite Dataset: its full configuration and the
// lifecycle state it is in.
func (s *Service) Dataset(ctx context.Context, name domain.Name) (domain.Dataset, error) {
	ctx, span := tracer().Start(ctx, "composite.Dataset", trace.WithAttributes(datasetAttrs(name)...))
	defer span.End()

	d, err := s.store.Dataset(ctx, name)
	if err != nil {
		return domain.Dataset{}, fail(span, err)
	}
	span.SetAttributes(stateKey.String(d.State.String()))
	return d, nil
}

// Datasets returns every declared Composite Dataset, ordered by name.
func (s *Service) Datasets(ctx context.Context) ([]domain.Dataset, error) {
	ctx, span := tracer().Start(ctx, "composite.Datasets")
	defer span.End()

	out, err := s.store.Datasets(ctx)
	if err != nil {
		return nil, fail(span, err)
	}
	return out, nil
}

// Edit replaces a Composite Dataset's configuration. A dataset that has been
// built becomes stale — what is served no longer matches what is declared,
// and only the next Build can reconcile them. A draft stays a draft: there is
// nothing built to diverge from.
func (s *Service) Edit(ctx context.Context, name domain.Name, cfg domain.Config) (domain.Dataset, error) {
	ctx, span := tracer().Start(ctx, "composite.Edit", trace.WithAttributes(datasetAttrs(name)...))
	defer span.End()

	existing, err := s.store.Dataset(ctx, name)
	if err != nil {
		return domain.Dataset{}, fail(span, err)
	}
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return domain.Dataset{}, fail(span, err)
	}
	edited := existing
	edited.Config = cfg
	edited.State = existing.State.AfterEdit()
	edited.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateDataset(ctx, edited); err != nil {
		return domain.Dataset{}, fail(span, err)
	}
	span.SetAttributes(stateKey.String(edited.State.String()))
	s.log.Info("composite dataset edited", "dataset", name.String(), "state", edited.State.String())
	return edited, nil
}

// Delete removes a Composite Dataset: its declaration and everything derived
// from it. Source data is never touched — this context only ever reads it.
func (s *Service) Delete(ctx context.Context, name domain.Name) (domain.Dataset, error) {
	ctx, span := tracer().Start(ctx, "composite.Delete", trace.WithAttributes(datasetAttrs(name)...))
	defer span.End()

	d, err := s.store.Dataset(ctx, name)
	if err != nil {
		return domain.Dataset{}, fail(span, err)
	}
	if err := s.store.DeleteDataset(ctx, name); err != nil {
		return domain.Dataset{}, fail(span, fmt.Errorf("deleting composite dataset %q: %w", name, err))
	}
	s.log.Info("composite dataset deleted", "dataset", name.String())
	return d, nil
}
