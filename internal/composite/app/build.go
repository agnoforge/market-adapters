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
// bar, a fixed end passes through — and judges everything that follows against
// that Resolved End: which range the base source supplies, how many of the
// expected bars are really there, and which open Gaps intersect the range. The
// base Segment it assembles replaces whatever the previous build left.
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

	result, err := s.assemble(ctx, d)
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
		resolvedEndKey.String(instant(result.Quality.ResolvedEnd)))

	if result.NotReady != nil {
		s.log.Info("composite build failed", "dataset", name.String(), "err", result.NotReady)
		return View{}, fail(span, result.NotReady)
	}
	s.log.Info("composite dataset built", "dataset", name.String(),
		"resolved_end", instant(result.Quality.ResolvedEnd),
		"coverage", result.Quality.Coverage(), "open_gaps", result.Quality.OpenGapCount())
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
}

// assemble is the Build itself: resolve the end, ask the port what the base
// source covers and whether the resolved range is complete, count the bars
// that are really there, and assemble the base Segment over what the base
// source supplies. An error here is a failure to run the Build at all — the
// port or the store broke — never a judgement about the data.
func (s *Service) assemble(ctx context.Context, d domain.Dataset) (buildResult, error) {
	cfg := d.Config
	resolvedEnd := cfg.RequestedEnd.Resolve(s.now())
	resolved := acq.Range{Start: cfg.RequestedStart.UTC(), End: resolvedEnd}
	quality := domain.NewQuality(cfg, resolvedEnd, s.now())

	if resolved.IsEmpty() {
		return buildResult{Quality: quality, NotReady: fmt.Errorf(
			"%w: the resolved range %s holds no closed 1-minute bar",
			domain.ErrNotReady, resolved)}, nil
	}

	coverage, err := s.acquisition.Coverage(ctx, cfg.Base)
	if err != nil {
		return buildResult{}, fmt.Errorf("coverage of the base source %s: %w", cfg.Base, err)
	}
	supplied := intersectRanges(coverage, resolved)
	if len(supplied) == 0 {
		return buildResult{Quality: quality, NotReady: fmt.Errorf(
			"%w: the base source %s supplies nothing of %s",
			domain.ErrNotReady, cfg.Base, resolved)}, nil
	}

	// The base Segment spans everything the base source supplies inside the
	// resolved range. Anything missing inside that span is missing data, and
	// the completeness answer below is what says so.
	available := acq.Range{Start: supplied[0].Start, End: supplied[len(supplied)-1].End}
	quality.AvailableStart, quality.AvailableEnd = available.Start, available.End

	bars, err := s.store.SourceBars(ctx, cfg.Base, resolved)
	if err != nil {
		return buildResult{}, fmt.Errorf("counting the bars of %s: %w", cfg.Base, err)
	}
	quality.ActualBars = bars.Count

	complete, err := s.acquisition.Completeness(ctx, cfg.Base, resolved)
	if err != nil {
		return buildResult{}, fmt.Errorf("completeness of the base source %s: %w", cfg.Base, err)
	}
	quality.OpenGaps = complete.Gaps

	result := buildResult{
		Segments: []domain.Segment{{Kind: domain.SegmentBase, Source: cfg.Base, Range: available}},
		Quality:  quality,
	}
	if quality.Strict() && !complete.Complete {
		result.NotReady = notReady(resolved, available, quality.OpenGaps)
	}
	return result, nil
}

// notReady spells why a strict dataset refuses to be ready: the gaps that
// intersect its resolved range, and the parts of that range its sources do not
// supply at all.
func notReady(resolved, available acq.Range, gaps []domain.Gap) error {
	var reasons []string
	if len(gaps) > 0 {
		ranges := make([]string, 0, len(gaps))
		for _, g := range gaps {
			ranges = append(ranges, g.Range.String())
		}
		reasons = append(reasons, fmt.Sprintf("%d open gap(s) intersect it: %s",
			len(gaps), strings.Join(ranges, ", ")))
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
