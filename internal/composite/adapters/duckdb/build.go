package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
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

// replaceQuality writes the Quality of one dataset: the row itself, the open
// Gaps it lists and the Transitions it recorded.
func replaceQuality(ctx context.Context, tx *sql.Tx, name domain.Name, q domain.Quality) error {
	for _, table := range qualityTables {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE dataset = ?`, name.String()); err != nil {
			return fmt.Errorf("composite duckdb: replace quality of %q: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO composite_quality
			(dataset, requested_start_ms, requested_end_ms, resolved_end_ms,
			 available_start_ms, available_end_ms, expected_bars, actual_bars,
			 mode, mat_version, last_build_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		name.String(), q.RequestedStart.UnixMilli(), endValue(q.RequestedEnd),
		q.ResolvedEnd.UnixMilli(), nullableMS(q.AvailableStart), nullableMS(q.AvailableEnd),
		q.ExpectedBars, q.ActualBars, q.Mode.String(), q.MatVersion,
		q.LastBuildAt.UnixMilli()); err != nil {
		return fmt.Errorf("composite duckdb: replace quality of %q: %w", name, err)
	}
	for i, w := range q.IncompleteWindows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO composite_quality_windows (dataset, ordinal, timeframe, start_ms, end_ms)
			VALUES (?,?,?,?,?)`,
			name.String(), i, w.Timeframe.String(),
			w.Range.Start.UnixMilli(), w.Range.End.UnixMilli()); err != nil {
			return fmt.Errorf("composite duckdb: replace quality windows of %q: %w", name, err)
		}
	}
	for i, g := range q.OpenGaps {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO composite_quality_gaps (dataset, ordinal, gap_id, start_ms, end_ms)
			VALUES (?,?,?,?,?)`,
			name.String(), i, g.ID, g.Range.Start.UnixMilli(), g.Range.End.UnixMilli()); err != nil {
			return fmt.Errorf("composite duckdb: replace quality gaps of %q: %w", name, err)
		}
	}
	for i, t := range q.Transitions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO composite_transitions
				(dataset, ordinal, at_ms,
				 from_instrument, from_provider, from_symbol, from_timeframe,
				 to_instrument, to_provider, to_symbol, to_timeframe,
				 close_price, open_price, price_delta)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			name.String(), i, t.At.UnixMilli(),
			t.From.Instrument.String(), t.From.Provider, t.From.Symbol.String(), t.From.Timeframe.String(),
			t.To.Instrument.String(), t.To.Provider, t.To.Symbol.String(), t.To.Timeframe.String(),
			t.Close, t.Open, t.Delta); err != nil {
			return fmt.Errorf("composite duckdb: replace transitions of %q: %w", name, err)
		}
	}
	return nil
}

// qualityTables are the tables one Build's Quality is spread over, all replaced
// together: what is stored always describes the build the dataset row is in the
// state of.
var qualityTables = []string{
	"composite_quality", "composite_quality_gaps",
	"composite_quality_windows", "composite_transitions",
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
		matVersion                     sql.NullInt64
		lastBuildMS                    int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT requested_start_ms, requested_end_ms, resolved_end_ms,
		       available_start_ms, available_end_ms, expected_bars, actual_bars,
		       mode, mat_version, last_build_ms
		FROM composite_quality WHERE dataset = ?`, name.String()).
		Scan(&requestedStartMS, &requestedEndMS, &resolvedMS,
			&availableStartMS, &availableEnd, &q.ExpectedBars, &q.ActualBars,
			&mode, &matVersion, &lastBuildMS)
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
	q.MatVersion = int(matVersion.Int64)
	q.LastBuildAt = time.UnixMilli(lastBuildMS).UTC()

	gaps, err := s.qualityGaps(ctx, name)
	if err != nil {
		return domain.Quality{}, false, err
	}
	q.OpenGaps = gaps

	windows, err := s.incompleteWindows(ctx, name)
	if err != nil {
		return domain.Quality{}, false, err
	}
	q.IncompleteWindows = windows

	transitions, err := s.transitions(ctx, name)
	if err != nil {
		return domain.Quality{}, false, err
	}
	q.Transitions = transitions
	return q, true, nil
}

// transitions reads the provider boundaries one Quality recorded, in timeline
// order.
func (s *Store) transitions(ctx context.Context, name domain.Name) ([]domain.Transition, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT at_ms,
		       from_instrument, from_provider, from_symbol, from_timeframe,
		       to_instrument, to_provider, to_symbol, to_timeframe,
		       close_price, open_price, price_delta
		FROM composite_transitions WHERE dataset = ? ORDER BY ordinal`, name.String())
	if err != nil {
		return nil, fmt.Errorf("composite duckdb: transitions of %q: %w", name, err)
	}
	defer rows.Close()

	var out []domain.Transition
	for rows.Next() {
		var (
			atMS                              int64
			fromInstrument, fromProvider      string
			fromSymbol, fromTimeframe         string
			toInstrument, toProvider          string
			toSymbol, toTimeframe             string
			closePrice, openPrice, priceDelta string
		)
		if err := rows.Scan(&atMS,
			&fromInstrument, &fromProvider, &fromSymbol, &fromTimeframe,
			&toInstrument, &toProvider, &toSymbol, &toTimeframe,
			&closePrice, &openPrice, &priceDelta); err != nil {
			return nil, fmt.Errorf("composite duckdb: transitions of %q: %w", name, err)
		}
		out = append(out, domain.Transition{
			At:    time.UnixMilli(atMS).UTC(),
			From:  source(fromInstrument, fromProvider, fromSymbol, fromTimeframe),
			To:    source(toInstrument, toProvider, toSymbol, toTimeframe),
			Close: closePrice, Open: openPrice, Delta: priceDelta,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("composite duckdb: transitions of %q: %w", name, err)
	}
	return out, nil
}

// source rebuilds a stored Source out of its four columns.
func source(instrument, provider, symbol, timeframe string) domain.Source {
	return domain.Source{
		Instrument: domain.Instrument(instrument),
		Provider:   provider,
		Symbol:     acq.Symbol(symbol),
		Timeframe:  domain.Timeframe(timeframe),
	}
}

// incompleteWindows reads the materialization windows one Quality flags, in
// the order it listed them: by timeframe, then by time.
func (s *Store) incompleteWindows(ctx context.Context, name domain.Name) ([]domain.IncompleteWindow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT timeframe, start_ms, end_ms FROM composite_quality_windows
		 WHERE dataset = ? ORDER BY ordinal`, name.String())
	if err != nil {
		return nil, fmt.Errorf("composite duckdb: incomplete windows of %q: %w", name, err)
	}
	defer rows.Close()

	var out []domain.IncompleteWindow
	for rows.Next() {
		var (
			timeframe      string
			startMS, endMS int64
		)
		if err := rows.Scan(&timeframe, &startMS, &endMS); err != nil {
			return nil, fmt.Errorf("composite duckdb: incomplete windows of %q: %w", name, err)
		}
		out = append(out, domain.IncompleteWindow{
			Timeframe: domain.Timeframe(timeframe),
			Range: acq.Range{
				Start: time.UnixMilli(startMS).UTC(),
				End:   time.UnixMilli(endMS).UTC(),
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("composite duckdb: incomplete windows of %q: %w", name, err)
	}
	return out, nil
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

// TransitionDelta prices one Transition out of acquisition's bars: the close of
// the last bar the outgoing source has before at, the open of the bar the
// incoming source has at at, and the difference between them.
//
// The subtraction happens here, in SQL, over the DECIMAL(20,8) columns the
// prices are stored in, and only the result is cast to text. No float is
// involved anywhere on the way — not in DuckDB, which subtracts two decimals
// exactly, and not in Go, which only ever sees the strings. A delta of
// 0.00000001 is therefore exactly that, and never 1.0000000000000001e-08.
//
// A Transition that one of the two bars is missing for is not an error: the
// join yields no row, and the answer comes back unpriced.
func (s *Store) TransitionDelta(ctx context.Context, from, to domain.Source, at time.Time) (app.PriceDelta, error) {
	fromTF, ok := from.Timeframe.Acquisition()
	if !ok {
		return app.PriceDelta{}, fmt.Errorf("%w: acquisition has no timeframe %q",
			domain.ErrInvalidConfig, from.Timeframe)
	}
	toTF, ok := to.Timeframe.Acquisition()
	if !ok {
		return app.PriceDelta{}, fmt.Errorf("%w: acquisition has no timeframe %q",
			domain.ErrInvalidConfig, to.Timeframe)
	}
	ms := at.UTC().UnixMilli()

	var out app.PriceDelta
	err := s.db.QueryRowContext(ctx, `
		SELECT CAST(outgoing.price AS VARCHAR),
		       CAST(incoming.price AS VARCHAR),
		       CAST(incoming.price - outgoing.price AS VARCHAR)
		FROM (SELECT "close" AS price FROM bars
		      WHERE provider = ? AND symbol = ? AND timeframe = ? AND open_time < ?
		      ORDER BY open_time DESC LIMIT 1) AS outgoing,
		     (SELECT "open" AS price FROM bars
		      WHERE provider = ? AND symbol = ? AND timeframe = ? AND open_time = ?) AS incoming`,
		from.Provider, from.Symbol.String(), fromTF.String(), ms,
		to.Provider, to.Symbol.String(), toTF.String(), ms).
		Scan(&out.Close, &out.Open, &out.Delta)
	if errors.Is(err, sql.ErrNoRows) {
		return app.PriceDelta{}, nil
	}
	if err != nil {
		return app.PriceDelta{}, fmt.Errorf("composite duckdb: price delta from %s to %s at %s: %w",
			from, to, at.UTC().Format(time.RFC3339), err)
	}
	out.Priced = true
	return out, nil
}

// nullableMS is an instant as a stored value: NULL when it is the zero time.
func nullableMS(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}
