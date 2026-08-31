package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// This file is the storage side of a Build: the Segments and the Quality one
// produced, and the one read of another context's table this package makes.

// SaveBuild records the outcome of one Build in a single transaction: the
// dataset row, its Segments and its Quality. Segments and Quality are replaced
// wholesale, so what is stored always describes the build the row is in the
// state of.
func (s *Store) SaveBuild(ctx context.Context, d domain.Dataset, segments []domain.Segment, q domain.Quality) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, updateStatement, append(values(d)[1:], d.Name.String())...)
		if err != nil {
			return fmt.Errorf("composite duckdb: save build of %q: %w", d.Name, err)
		}
		if err := notFoundIfUntouched(res, d.Name); err != nil {
			return err
		}
		if err := replaceSegments(ctx, tx, d.Name, segments); err != nil {
			return err
		}
		return replaceQuality(ctx, tx, d.Name, q)
	})
}

// replaceSegments writes the ordered Segments of one dataset, dropping
// whatever an earlier build left.
func replaceSegments(ctx context.Context, tx *sql.Tx, name domain.Name, segments []domain.Segment) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM composite_segments WHERE dataset = ?`, name.String()); err != nil {
		return fmt.Errorf("composite duckdb: replace segments of %q: %w", name, err)
	}
	for i, seg := range segments {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO composite_segments
				(dataset, ordinal, kind, instrument, provider, symbol, timeframe, start_ms, end_ms)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			name.String(), i, seg.Kind.String(),
			seg.Source.Instrument.String(), seg.Source.Provider,
			seg.Source.Symbol.String(), seg.Source.Timeframe.String(),
			seg.Range.Start.UnixMilli(), seg.Range.End.UnixMilli()); err != nil {
			return fmt.Errorf("composite duckdb: replace segments of %q: %w", name, err)
		}
	}
	return nil
}

// replaceQuality writes the Quality of one dataset and the open Gaps it lists.
func replaceQuality(ctx context.Context, tx *sql.Tx, name domain.Name, q domain.Quality) error {
	for _, table := range []string{"composite_quality", "composite_quality_gaps"} {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE dataset = ?`, name.String()); err != nil {
			return fmt.Errorf("composite duckdb: replace quality of %q: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO composite_quality
			(dataset, requested_start_ms, requested_end_ms, resolved_end_ms,
			 available_start_ms, available_end_ms, expected_bars, actual_bars,
			 mode, last_build_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		name.String(), q.RequestedStart.UnixMilli(), endValue(q.RequestedEnd),
		q.ResolvedEnd.UnixMilli(), nullableMS(q.AvailableStart), nullableMS(q.AvailableEnd),
		q.ExpectedBars, q.ActualBars, q.Mode.String(), q.LastBuildAt.UnixMilli()); err != nil {
		return fmt.Errorf("composite duckdb: replace quality of %q: %w", name, err)
	}
	for i, g := range q.OpenGaps {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO composite_quality_gaps (dataset, ordinal, gap_id, start_ms, end_ms)
			VALUES (?,?,?,?,?)`,
			name.String(), i, g.ID, g.Range.Start.UnixMilli(), g.Range.End.UnixMilli()); err != nil {
			return fmt.Errorf("composite duckdb: replace quality gaps of %q: %w", name, err)
		}
	}
	return nil
}

// Segments returns the ordered Segments of a Composite Dataset. A dataset no
// Build has assembled anything for has none, which is not an error.
func (s *Store) Segments(ctx context.Context, name domain.Name) ([]domain.Segment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, instrument, provider, symbol, timeframe, start_ms, end_ms
		FROM composite_segments WHERE dataset = ? ORDER BY ordinal`, name.String())
	if err != nil {
		return nil, fmt.Errorf("composite duckdb: segments of %q: %w", name, err)
	}
	defer rows.Close()

	out := make([]domain.Segment, 0)
	for rows.Next() {
		var (
			kind, instrument, provider, symbol, timeframe string
			startMS, endMS                                int64
		)
		if err := rows.Scan(&kind, &instrument, &provider, &symbol, &timeframe, &startMS, &endMS); err != nil {
			return nil, fmt.Errorf("composite duckdb: segments of %q: %w", name, err)
		}
		out = append(out, domain.Segment{
			Kind: domain.SegmentKind(kind),
			Source: domain.Source{
				Instrument: domain.Instrument(instrument),
				Provider:   provider,
				Symbol:     acq.Symbol(symbol),
				Timeframe:  domain.Timeframe(timeframe),
			},
			Range: acq.Range{
				Start: time.UnixMilli(startMS).UTC(),
				End:   time.UnixMilli(endMS).UTC(),
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("composite duckdb: segments of %q: %w", name, err)
	}
	return out, nil
}

// Quality returns what the last Build computed and whether there was one.
func (s *Store) Quality(ctx context.Context, name domain.Name) (domain.Quality, bool, error) {
	var (
		q                              domain.Quality
		requestedStartMS, resolvedMS   int64
		requestedEndMS                 sql.NullInt64
		availableStartMS, availableEnd sql.NullInt64
		mode                           string
		lastBuildMS                    int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT requested_start_ms, requested_end_ms, resolved_end_ms,
		       available_start_ms, available_end_ms, expected_bars, actual_bars,
		       mode, last_build_ms
		FROM composite_quality WHERE dataset = ?`, name.String()).
		Scan(&requestedStartMS, &requestedEndMS, &resolvedMS,
			&availableStartMS, &availableEnd, &q.ExpectedBars, &q.ActualBars,
			&mode, &lastBuildMS)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Quality{}, false, nil
	}
	if err != nil {
		return domain.Quality{}, false, fmt.Errorf("composite duckdb: quality of %q: %w", name, err)
	}
	q.RequestedStart = time.UnixMilli(requestedStartMS).UTC()
	q.RequestedEnd = domain.NowEnd()
	if requestedEndMS.Valid {
		q.RequestedEnd = domain.FixedEnd(time.UnixMilli(requestedEndMS.Int64))
	}
	q.ResolvedEnd = time.UnixMilli(resolvedMS).UTC()
	if availableStartMS.Valid {
		q.AvailableStart = time.UnixMilli(availableStartMS.Int64).UTC()
	}
	if availableEnd.Valid {
		q.AvailableEnd = time.UnixMilli(availableEnd.Int64).UTC()
	}
	q.Mode = domain.Mode(mode)
	q.LastBuildAt = time.UnixMilli(lastBuildMS).UTC()

	gaps, err := s.qualityGaps(ctx, name)
	if err != nil {
		return domain.Quality{}, false, err
	}
	q.OpenGaps = gaps
	return q, true, nil
}

// qualityGaps reads the open Gaps one Quality lists, ascending.
func (s *Store) qualityGaps(ctx context.Context, name domain.Name) ([]domain.Gap, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT gap_id, start_ms, end_ms FROM composite_quality_gaps
		 WHERE dataset = ? ORDER BY ordinal`, name.String())
	if err != nil {
		return nil, fmt.Errorf("composite duckdb: quality gaps of %q: %w", name, err)
	}
	defer rows.Close()

	var out []domain.Gap
	for rows.Next() {
		var (
			id             int64
			startMS, endMS int64
		)
		if err := rows.Scan(&id, &startMS, &endMS); err != nil {
			return nil, fmt.Errorf("composite duckdb: quality gaps of %q: %w", name, err)
		}
		out = append(out, domain.Gap{ID: id, Range: acq.Range{
			Start: time.UnixMilli(startMS).UTC(),
			End:   time.UnixMilli(endMS).UTC(),
		}})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("composite duckdb: quality gaps of %q: %w", name, err)
	}
	return out, nil
}

// SourceBars reports what acquisition's bars table holds for one source
// Dataset inside r.
//
// This is the one statement in this context that reads a table another context
// owns, and it only ever reads: the composite 1-minute timeline references
// source bars instead of copying them (ADR-0005), so counting them is a query
// against the shared file rather than a transfer through Go values. A future
// service split replaces this read, and nothing else.
func (s *Store) SourceBars(ctx context.Context, src domain.Source, r acq.Range) (app.SourceBars, error) {
	timeframe, ok := src.Timeframe.Acquisition()
	if !ok {
		return app.SourceBars{}, fmt.Errorf("%w: acquisition has no timeframe %q",
			domain.ErrInvalidConfig, src.Timeframe)
	}
	var (
		count           int64
		firstMS, lastMS sql.NullInt64
		out             app.SourceBars
		start, end      int64 = r.Start.UTC().UnixMilli(), r.End.UTC().UnixMilli()
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*), min(open_time), max(open_time) FROM bars
		WHERE provider = ? AND symbol = ? AND timeframe = ?
		  AND open_time >= ? AND open_time < ?`,
		src.Provider, src.Symbol.String(), timeframe.String(), start, end).
		Scan(&count, &firstMS, &lastMS); err != nil {
		return app.SourceBars{}, fmt.Errorf("composite duckdb: source bars of %s: %w", src, err)
	}
	out.Count = count
	if firstMS.Valid {
		out.First = time.UnixMilli(firstMS.Int64).UTC()
	}
	if lastMS.Valid {
		out.Last = time.UnixMilli(lastMS.Int64).UTC()
	}
	return out, nil
}

// nullableMS is an instant as a stored value: NULL when it is the zero time.
func nullableMS(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}
