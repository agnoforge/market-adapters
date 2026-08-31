package app

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel/trace"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// The consumer contract this whole context exists for (user stories 26–28): a
// backtesting engine — or a chart, or a curious human — asks for the bars of a
// Composite Dataset by name, Timeframe and range, and gets them without
// knowing which provider supplied which period, which gaps were repaired on the
// way, or where any of it is stored.
//
// One query surface answers every resolution. The 1-minute timeline is the
// dataset's Segments read over acquisition's own bars, and the higher frames
// are the bars a Build materialized; which of the two a request lands on is
// decided here and nowhere else, so a caller never has to ask.

// BarsRequest is a bars query as a consumer states it: a Timeframe, and the
// two bounds of a half-open range. A zero bound is one the caller omitted, and
// the dataset's own resolved range supplies it.
type BarsRequest struct {
	Timeframe  domain.Timeframe
	Start, End time.Time
}

// BarQuery is one resolved bars query, and the only thing the Store is ever
// asked to answer: which dataset, which Timeframe, over which concrete range,
// and the ordered Segments the 1-minute timeline is made of.
//
// Segments are carried for every query, because they are what says which
// source bars belong to this dataset at all; a query for a materialized frame
// reads the derived bars and ignores them.
type BarQuery struct {
	Dataset   domain.Dataset
	Timeframe domain.Timeframe
	Range     acq.Range
	Segments  []domain.Segment
}

// Name is the Composite Dataset this query is against.
func (q BarQuery) Name() domain.Name { return q.Dataset.Name }

// Derived reports whether the query is answered from materialized bars rather
// than from the referenced source bars of the 1-minute timeline.
func (q BarQuery) Derived() bool { return q.Timeframe != domain.TF1m }

// PlanBars resolves a bars query against a Composite Dataset: it decides that
// the dataset exists, that it has a timeline at all, that it serves the
// Timeframe asked for, and what range an omitted bound means.
//
// It is the whole error contract of the query surface, and every one of its
// refusals names what is wrong:
//
//   - an unknown dataset is domain.ErrNotFound;
//   - a dataset no Build ever assembled a timeline for — a draft, or one whose
//     last Build could not be ready — is domain.ErrNotBuilt;
//   - a Timeframe the dataset does not materialize is
//     domain.ErrTimeframeNotMaterialized, naming the frames it does serve;
//   - a malformed Timeframe or an empty range is domain.ErrInvalidConfig.
//
// A `stale` or research-mode dataset is none of those: it answers, and the
// state and mode it answers in are on the dataset it returns. Nothing here
// pretends staleness away — it is reported, not enforced.
func (s *Service) PlanBars(ctx context.Context, name domain.Name, req BarsRequest) (BarQuery, error) {
	ctx, span := tracer().Start(ctx, "composite.PlanBars", trace.WithAttributes(datasetAttrs(name)...))
	defer span.End()

	d, err := s.store.Dataset(ctx, name)
	if err != nil {
		return BarQuery{}, fail(span, err)
	}
	// A dataset whose bars were derived by another materialization version reads
	// as stale, and is served anyway: what it is, is discoverable.
	d = d.Observed()

	segments, err := s.store.Segments(ctx, name)
	if err != nil {
		return BarQuery{}, fail(span, err)
	}
	if len(segments) == 0 {
		return BarQuery{}, fail(span, fmt.Errorf(
			"%w: %q is %s and has no timeline to serve", domain.ErrNotBuilt, name, d.State))
	}
	if err := d.Serves(req.Timeframe); err != nil {
		return BarQuery{}, fail(span, err)
	}
	r, err := d.QueryRange(req.Start, req.End)
	if err != nil {
		return BarQuery{}, fail(span, err)
	}
	span.SetAttributes(stateKey.String(d.State.String()),
		timeframeKey.String(req.Timeframe.String()),
		rangeStartKey.String(instant(r.Start)), rangeEndKey.String(instant(r.End)))
	return BarQuery{Dataset: d, Timeframe: req.Timeframe, Range: r, Segments: segments}, nil
}

// Bars answers a resolved query with the composite bars themselves, ascending
// by open time. The prices are the decimal strings the database holds, never
// floats: a bar is exact from acquisition's DECIMAL(20,8) column to the
// consumer, or it is not the same bar the source data says it is.
func (s *Service) Bars(ctx context.Context, q BarQuery) ([]acq.Bar, error) {
	ctx, span := tracer().Start(ctx, "composite.Bars",
		trace.WithAttributes(queryAttrs(q)...))
	defer span.End()

	bars, err := s.store.Bars(ctx, q)
	if err != nil {
		return nil, fail(span, err)
	}
	span.SetAttributes(barCountKey.Int(len(bars)))
	return bars, nil
}

// ExportBars streams a resolved query to w as a Parquet file, which is the
// default path for a payload a backtester actually wants: a year of bars is
// megabytes, and it never has to become Go values on the way out.
func (s *Service) ExportBars(ctx context.Context, q BarQuery, w io.Writer) error {
	ctx, span := tracer().Start(ctx, "composite.ExportBars",
		trace.WithAttributes(queryAttrs(q)...))
	defer span.End()

	if err := s.store.ExportBars(ctx, q, w); err != nil {
		return fail(span, err)
	}
	return nil
}
