package app

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// View is everything observable about one Composite Dataset: the declaration,
// the ordered Segments the last Build assembled — the provenance answer — and
// the Quality it computed. Quality is nil until a Build has run.
type View struct {
	Dataset  domain.Dataset
	Segments []domain.Segment
	Quality  *domain.Quality
}

// View returns one Composite Dataset with its Segments and Quality. The
// ordered Segment list is the whole provenance API: "where did this bar come
// from?" is answered by locating its open time in it.
func (s *Service) View(ctx context.Context, name domain.Name) (View, error) {
	ctx, span := tracer().Start(ctx, "composite.View", trace.WithAttributes(datasetAttrs(name)...))
	defer span.End()

	d, err := s.store.Dataset(ctx, name)
	if err != nil {
		return View{}, fail(span, err)
	}
	// A dataset whose bars were derived by another materialization version reads
	// as stale, whatever the row says: what is served is not what this service
	// would derive, and only the next Build reconciles them.
	d = d.Observed()
	span.SetAttributes(stateKey.String(d.State.String()))
	return s.viewOf(ctx, d)
}

// viewOf reads the Segments and Quality belonging to a declaration already in
// hand.
func (s *Service) viewOf(ctx context.Context, d domain.Dataset) (View, error) {
	segments, err := s.store.Segments(ctx, d.Name)
	if err != nil {
		return View{}, err
	}
	quality, built, err := s.store.Quality(ctx, d.Name)
	if err != nil {
		return View{}, err
	}
	out := View{Dataset: d, Segments: segments}
	if built {
		out.Quality = &quality
	}
	return out, nil
}

// Build reconciles a Composite Dataset's declaration against the source data
// that actually exists, and leaves the dataset ready or failed.
//
// It resolves the requested end — `now` becomes the last fully closed 1-minute
// bar, a fixed end passes through — then makes the source data exist before
// judging it: the parts of the resolved range the base source has not acquired
// are backfilled from the base provider and waited for, and the Gaps inside
// what it then covers are repaired. Everything that follows is judged against
// that Resolved End: which range the base source supplies, how many of the
// expected bars are really there, and which open Gaps survived. The base
// Segment it assembles replaces whatever the previous build left.
//
// A dataset that named a catch-up provider then continues past where the base
// one stops: only the tail the base genuinely could not supply is asked of it,
// it becomes a second Segment abutting the first, and the boundary between them
// is validated — no hole, no overlap, same Instrument, same canonical timeframe
// — before anything is judged over it. A boundary that cannot be safely
// resolved fails the Build and says why; the price movement across a valid one
// is recorded in Quality and enforced by nothing.
//
// What the providers cannot supply — a head below the base provider's
// earliest-available floor, a tail neither reaches — is a shortfall the mode
// judges, not a failure; a backfill that fails terminally is a failure, and the
// acquisition error is preserved on the dataset.
//
// The mode decides the ending. Strict refuses readiness while the base source
// does not supply the whole resolved range or an open Gap intersects it: the
// build ends failed, with the gaps named in the error and listed in Quality.
// Research becomes ready with those imperfections recorded and the dataset
// visibly a research dataset. Either way the Quality is persisted, so a failure
// is inspectable afterwards.
//
// A second concurrent Build of the same dataset is rejected with an error
// wrapping domain.ErrBuildRunning.
func (s *Service) Build(ctx context.Context, name domain.Name) (View, error) {
	ctx, span := tracer().Start(ctx, "composite.Build", trace.WithAttributes(datasetAttrs(name)...))
	defer span.End()

	release, err := s.claim(name)
	if err != nil {
		return View{}, fail(span, err)
	}
	defer release()

	d, err := s.store.Dataset(ctx, name)
	if err != nil {
		return View{}, fail(span, err)
	}
	state, err := d.State.BeginBuild()
	if err != nil {
		return View{}, fail(span, fmt.Errorf("%w: %q", err, name))
	}
	d.State, d.LastError, d.UpdatedAt = state, "", s.now().UTC()
	if err := s.store.UpdateDataset(ctx, d); err != nil {
		return View{}, fail(span, err)
	}
	span.SetAttributes(stateKey.String(d.State.String()))

	result, err := s.runBuild(ctx, d)
	if err != nil {
		// The Build could not run at all: the dataset is failed with the
		// failure preserved, and no Quality describes a build that never
		// happened.
		d.State, d.LastError, d.UpdatedAt = domain.StateFailed, err.Error(), s.now().UTC()
		if saveErr := s.store.UpdateDataset(ctx, d); saveErr != nil {
			s.log.Error("recording a failed composite build", "dataset", name.String(), "err", saveErr)
		}
		span.SetAttributes(stateKey.String(d.State.String()))
		return View{}, fail(span, err)
	}

	d.ResolvedEnd = result.Quality.ResolvedEnd
	d.State = domain.Settled(result.NotReady == nil)
	// The dataset row carries the version its bars were derived by, so a later
	// build of this service that derives them differently can tell that what is
	// stored is no longer what it would produce.
	d.MatVersion = domain.MaterializationVersion
	d.LastError = ""
	segments := result.Segments
	if result.NotReady != nil {
		d.LastError = result.NotReady.Error()
		segments = nil
	}
	d.UpdatedAt = s.now().UTC()
	if err := s.store.SaveBuild(ctx, d, segments, result.Quality); err != nil {
		return View{}, fail(span, err)
	}
	span.SetAttributes(stateKey.String(d.State.String()),
		gapCountKey.Int(result.Quality.OpenGapCount()),
		segmentCountKey.Int(len(result.Segments)),
		transitionCountKey.Int(result.Quality.TransitionCount()),
		materializedBarsKey.Int64(result.Materialized),
		incompleteWindowsKey.Int(result.Quality.IncompleteWindowCount()),
		resolvedEndKey.String(instant(result.Quality.ResolvedEnd)))

	if result.NotReady != nil {
		s.log.Info("composite build failed", "dataset", name.String(), "err", result.NotReady)
		return View{}, fail(span, result.NotReady)
	}
	s.log.Info("composite dataset built", "dataset", name.String(),
		"resolved_end", instant(result.Quality.ResolvedEnd),
		"coverage", result.Quality.Coverage(), "open_gaps", result.Quality.OpenGapCount(),
		"segments", len(result.Segments), "transitions", result.Quality.TransitionCount(),
		"materialized_bars", result.Materialized,
		"incomplete_windows", result.Quality.IncompleteWindowCount())
	quality := result.Quality
	return View{Dataset: d, Segments: segments, Quality: &quality}, nil
}

// buildResult is what one run of assemble produced: the Segments and Quality
// it computed, and — when the dataset cannot be ready under its mode — the
// reason. NotReady is an outcome of the Build, not a failure to run one.
type buildResult struct {
	Segments []domain.Segment
	Quality  domain.Quality
	NotReady error
	// Materialized is how many higher-timeframe bars the build derived. A build
	// that could not be ready derives none.
	Materialized int64
}

// runBuild is the Build itself, in the order the phases have to happen in:
// resolve the end, extend the base source over whatever part of the resolved
// range it does not have yet, assemble the ordered Segments over what the
// sources supply — asking a configured catch-up source for the tail the base
// could not — validate the boundary between them, judge what is really there,
// and materialize the configured timeframes.
//
// Every one of those phases is a span of its own under the Build's, carrying
// the dataset and the range it is about, so a slow phase is visible as a bar in
// the waterfall and a broken one is the span the error is recorded on
// (ADR 0003).
//
// An error here is a failure to run the Build at all — the port, a backfill or
// the store broke, or the Transitions could not be validated — never a
// judgement about how complete the data is, which is the mode's to make.
func (s *Service) runBuild(ctx context.Context, d domain.Dataset) (buildResult, error) {
	cfg := d.Config
	resolved, quality := s.resolve(ctx, d)

	if resolved.IsEmpty() {
		return buildResult{Quality: quality, NotReady: fmt.Errorf(
			"%w: the resolved range %s holds no closed 1-minute bar",
			domain.ErrNotReady, resolved)}, nil
	}

	// Catch-up first: what the base source does not have yet is asked of the
	// base provider and waited for, so everything below judges data that has
	// finished arriving (decision 25).
	coverage, err := s.ensure(ctx, d.Name, cfg.Base, resolved)
	if err != nil {
		return buildResult{}, err
	}
	supplied := intersectRanges(coverage, resolved)
	if len(supplied) == 0 {
		return buildResult{Quality: quality, NotReady: fmt.Errorf(
			"%w: the base source %s supplies nothing of %s",
			domain.ErrNotReady, cfg.Base, resolved)}, nil
	}

	segments, err := s.assemble(ctx, d, supplied, resolved)
	if err != nil {
		return buildResult{}, err
	}

	// The seam between two providers is validated before anything is judged
	// or recorded over it: a hole, an overlap, a different Instrument or a
	// different canonical timeframe fails the Build and says which. Nothing
	// is silently accepted (decision 5, 26).
	if err := s.validate(ctx, d, segments, resolved); err != nil {
		return buildResult{}, err
	}

	if err := s.judge(ctx, d, segments, resolved, &quality); err != nil {
		return buildResult{}, err
	}
	available := acq.Range{Start: quality.AvailableStart, End: quality.AvailableEnd}

	result := buildResult{Segments: segments, Quality: quality}
	if quality.Strict() && (len(quality.OpenGaps) > 0 ||
		len(resolved.Subtract(available)) > 0 || len(quality.IncompleteWindows) > 0) {
		result.NotReady = notReady(resolved, available, quality)
	}

	// Materializing is the last phase, and it happens either way: a ready
	// dataset gets the bars its Timeframes derive from the timeline, and one
	// that could not be ready gets none — the same rule the Segments follow, so
	// nothing derived outlives the build that derived it.
	written, err := s.materialize(ctx, d, result, resolved)
	if err != nil {
		return buildResult{}, err
	}
	result.Materialized = written
	return result, nil
}

// resolve is the first phase: the requested end becomes the instant everything
// after it is judged against — `now` is the last fully closed 1-minute bar, a
// fixed end is itself — and the Quality the Build fills in is opened over it.
func (s *Service) resolve(ctx context.Context, d domain.Dataset) (acq.Range, domain.Quality) {
	_, span := phase(ctx, "composite.resolve", datasetAttrs(d.Name))
	defer span.End()

	resolvedEnd := d.Config.RequestedEnd.Resolve(s.now())
	resolved := acq.Range{Start: d.Config.RequestedStart.UTC(), End: resolvedEnd}
	span.SetAttributes(append(rangeAttrs(resolved),
		resolvedEndKey.String(instant(resolvedEnd)))...)
	return resolved, domain.NewQuality(d.Config, resolvedEnd, s.now())
}

// assemble is the phase that turns what the sources supply into the ordered
// Segments of the composite timeline.
//
// The base Segment reaches over everything the base source supplies inside the
// resolved range. Anything missing inside it is missing data, and the
// completeness answer the next phase asks for is what says so.
//
// Only now, with the base provider extended as far as it goes, is a configured
// catch-up provider asked for anything — and only for the tail the base
// genuinely could not supply. There is no third source: the chain is the base
// and the catch-up, and nothing else (decision 25).
func (s *Service) assemble(ctx context.Context, d domain.Dataset, supplied []acq.Range, resolved acq.Range) ([]domain.Segment, error) {
	ctx, span := phase(ctx, "composite.assemble", phaseAttrs(d.Name, resolved))
	defer span.End()

	segments := []domain.Segment{{Kind: domain.SegmentBase, Source: d.Config.Base, Range: covering(supplied)}}
	tail, err := s.catchUp(ctx, d.Name, d.Config, segments[0].Range, resolved)
	if err != nil {
		return nil, fail(span, err)
	}
	if tail != nil {
		segments = append(segments, *tail)
	}
	span.SetAttributes(segmentCountKey.Int(len(segments)))
	return segments, nil
}

// validate is the phase that refuses a timeline whose provider boundaries do
// not line up: a hole, an overlap, a different Instrument or a different
// canonical timeframe fails the Build here, and the span says which.
func (s *Service) validate(ctx context.Context, d domain.Dataset, segments []domain.Segment, resolved acq.Range) error {
	_, span := phase(ctx, "composite.validate", phaseAttrs(d.Name, resolved))
	defer span.End()

	span.SetAttributes(segmentCountKey.Int(len(segments)),
		transitionCountKey.Int(len(domain.TransitionsOf(segments))))
	return fail(span, domain.ValidateTransitions(segments))
}

// judge is the phase that decides what is really there. Each Segment's own
// source answers for its own slice of the timeline: the Gaps inside it — with
// what a repair could not close left open — and the bars really there, counted
// last so a repair that landed some is counted. The price movement across every
// Transition is recorded, never enforced, and which higher-timeframe windows
// the data does not fully back follows from the timeline, the Gaps and the
// calendar.
//
// It fills the Quality in; the readiness rule that reads it is the mode's, and
// a dataset the mode refuses is not a failure of this phase.
func (s *Service) judge(ctx context.Context, d domain.Dataset, segments []domain.Segment, resolved acq.Range, quality *domain.Quality) error {
	ctx, span := phase(ctx, "composite.quality", phaseAttrs(d.Name, resolved))
	defer span.End()

	available := acq.Range{Start: segments[0].Range.Start, End: segments[len(segments)-1].Range.End}
	quality.AvailableStart, quality.AvailableEnd = available.Start, available.End

	for _, seg := range segments {
		complete, err := s.acquisition.Completeness(ctx, seg.Source, seg.Range)
		if err != nil {
			return fail(span, fmt.Errorf("completeness of %s: %w", seg.Source, err))
		}
		if !complete.Complete {
			// Something is missing inside what the source covers: the Gaps
			// there are repaired before readiness is judged, and what survives
			// the repair is what the mode judges.
			if complete, err = s.repair(ctx, d.Name, seg.Source, seg.Range); err != nil {
				return fail(span, err)
			}
		}
		quality.OpenGaps = append(quality.OpenGaps, complete.Gaps...)

		bars, err := s.store.SourceBars(ctx, seg.Source, seg.Range)
		if err != nil {
			return fail(span, fmt.Errorf("counting the bars of %s: %w", seg.Source, err))
		}
		quality.ActualBars += bars.Count
	}

	transitions, err := s.priceTransitions(ctx, segments)
	if err != nil {
		return fail(span, err)
	}
	quality.Transitions = transitions
	quality.IncompleteWindows = domain.IncompleteWindows(d.Config.Timeframes, resolved, available, quality.OpenGaps)

	span.SetAttributes(gapCountKey.Int(quality.OpenGapCount()),
		transitionCountKey.Int(quality.TransitionCount()),
		incompleteWindowsKey.Int(quality.IncompleteWindowCount()))
	return nil
}

// materialize is the last phase: it derives the configured higher timeframes
// from the composite timeline and reports how many bars it wrote.
//
// A Build that could not be ready materializes nothing: a dataset whose
// readiness rule refused it must not serve derived bars, and the ones an
// earlier build left are removed with the Segments they came from.
func (s *Service) materialize(ctx context.Context, d domain.Dataset, result buildResult, resolved acq.Range) (int64, error) {
	ctx, span := phase(ctx, "composite.materialize", phaseAttrs(d.Name, resolved))
	defer span.End()

	m := Materialization{Version: domain.MaterializationVersion}
	if result.NotReady == nil {
		m.Segments, m.Timeframes = result.Segments, d.Config.Timeframes
	}
	written, err := s.store.Materialize(ctx, d.Name, m)
	if err != nil {
		return 0, fail(span, fmt.Errorf("materializing the timeframes of %q: %w", d.Name, err))
	}
	span.SetAttributes(materializedBarsKey.Int64(written))
	return written, nil
}

// catchUp is the cross-provider half of the assemble phase: the Segment a
// configured catch-up source contributes past where the base one stops, or nil
// when there is nothing to ask for or nothing came back.
//
// Only the tail is ever requested — what the base provider genuinely could not
// supply — so a catch-up provider is never asked to re-acquire history the base
// already has (decision 25). What it then supplies of the resolved range is
// what the Segment reaches over: if that reaches back into the base's own
// range, two providers hold data for the same instants, and the Transition
// validation refuses it rather than quietly preferring one of them (decision 5).
func (s *Service) catchUp(ctx context.Context, name domain.Name, cfg domain.Config, base, resolved acq.Range) (*domain.Segment, error) {
	src, ok := catchUpSource(cfg)
	if !ok {
		return nil, nil
	}
	tail := acq.Range{Start: base.End, End: resolved.End}
	if tail.IsEmpty() {
		// The base provider reached the resolved end on its own: a dataset is
		// as single-source as it can be.
		return nil, nil
	}
	coverage, err := s.ensure(ctx, name, src, tail)
	if err != nil {
		return nil, err
	}
	supplied := intersectRanges(coverage, resolved)
	if len(supplied) == 0 {
		// The catch-up provider has nothing there either: the shortfall is
		// the mode's to judge, not a failure.
		s.log.Info("the catch-up provider supplies nothing of the tail",
			"source", src.String(), "tail", tail.String())
		return nil, nil
	}
	return &domain.Segment{Kind: domain.SegmentCatchUp, Source: src, Range: covering(supplied)}, nil
}

// catchUpSource is the catch-up source a Build should ask, and whether there is
// one. Cross-provider assembly happens only when it was explicitly configured
// (decision 4), and a catch-up source that is the base source is the base
// provider filling its own tail, which the base phase has already done.
func catchUpSource(cfg domain.Config) (domain.Source, bool) {
	if cfg.CatchUp.Kind != domain.CatchUpSource || cfg.CatchUp.Source == cfg.Base {
		return domain.Source{}, false
	}
	return cfg.CatchUp.Source, true
}

// priceTransitions records what the timeline's provider boundaries cost: the
// close→open movement across each of them, read as exact decimals from the
// bars themselves. It is evidence for a researcher, never a rule — no
// threshold makes a delta a failure (decision 26).
func (s *Service) priceTransitions(ctx context.Context, segments []domain.Segment) ([]domain.Transition, error) {
	transitions := domain.TransitionsOf(segments)
	for i := range transitions {
		t := &transitions[i]
		delta, err := s.store.TransitionDelta(ctx, t.From, t.To, t.At)
		if err != nil {
			return nil, fmt.Errorf("the price delta across the transition %s: %w", t, err)
		}
		if !delta.Priced {
			s.log.Info("a transition could not be priced: one of its two bars is not there",
				"transition", t.String())
			continue
		}
		t.Close, t.Open, t.Delta = delta.Close, delta.Open, delta.Delta
	}
	return transitions, nil
}

// covering is the one range a set of supplied ranges reaches over: from the
// first start to the last end. What is missing inside it is missing data, which
// the completeness answer is what reports.
func covering(ranges []acq.Range) acq.Range {
	return acq.Range{Start: ranges[0].Start, End: ranges[len(ranges)-1].End}
}

// notReady spells why a strict dataset refuses to be ready: the gaps that
// intersect its resolved range, the parts of that range its sources do not
// supply at all, and the materialization windows the data does not fully back.
func notReady(resolved, available acq.Range, q domain.Quality) error {
	var reasons []string
	if gaps := q.OpenGaps; len(gaps) > 0 {
		ranges := make([]string, 0, len(gaps))
		for _, g := range gaps {
			ranges = append(ranges, g.Range.String())
		}
		reasons = append(reasons, fmt.Sprintf("%d open gap(s) intersect it: %s",
			len(gaps), strings.Join(ranges, ", ")))
	}
	if windows := q.IncompleteWindows; len(windows) > 0 {
		named := make([]string, 0, len(windows))
		for _, w := range windows {
			named = append(named, w.String())
		}
		reasons = append(reasons, fmt.Sprintf("%d materialization window(s) are incomplete: %s",
			len(windows), strings.Join(named, ", ")))
	}
	if missing := resolved.Subtract(available); len(missing) > 0 {
		spans := make([]string, 0, len(missing))
		for _, r := range missing {
			spans = append(spans, r.String())
		}
		reasons = append(reasons, "the sources supply nothing of "+strings.Join(spans, ", "))
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "the sources do not supply it in full")
	}
	return fmt.Errorf("%w: strict mode refuses %s — %s",
		domain.ErrNotReady, resolved, strings.Join(reasons, "; "))
}

// claim takes the one build slot of a dataset, returning the release. A second
// concurrent Build of the same dataset is refused rather than queued: builds
// that interleave corrupt what they both write (decision 29).
func (s *Service) claim(name domain.Name) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.building[name] {
		return nil, fmt.Errorf("%w: %q", domain.ErrBuildRunning, name)
	}
	s.building[name] = true
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.building, name)
	}, nil
}

// intersectRanges clips every range to r and drops what is left empty, keeping
// the order it was given.
func intersectRanges(ranges []acq.Range, r acq.Range) []acq.Range {
	out := make([]acq.Range, 0, len(ranges))
	for _, piece := range acq.MergeRanges(ranges) {
		if clipped := piece.Intersect(r); !clipped.IsEmpty() {
			out = append(out, clipped)
		}
	}
	return out
}
