package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"time"

	"github.com/agnos/agnoforge/internal/domain"
)

// ExtendCoverage adds r to the Dataset's Coverage. It reads the ranges already
// recorded, union-merges them with r through the domain, and rewrites the
// Dataset's rows in one transaction, so touching and overlapping ranges always
// collapse into a single stored range.
func (s *Store) ExtendCoverage(ctx context.Context, id domain.DatasetID, r domain.Range) error {
	if r.IsEmpty() {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		existing, err := coverageIn(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("duckdb: extend coverage of %s: %w", id, err)
		}
		merged := domain.MergeRanges(append(existing, r))

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM coverage WHERE provider = ? AND symbol = ? AND timeframe = ?`,
			key(id)...); err != nil {
			return fmt.Errorf("duckdb: extend coverage of %s: %w", id, err)
		}
		for _, m := range merged {
			startMS, endMS := msRange(m)
			args := append(key(id), startMS, endMS)
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO coverage (provider, symbol, timeframe, start_ms, end_ms)
				 VALUES (?,?,?,?,?)`, args...); err != nil {
				return fmt.Errorf("duckdb: extend coverage of %s: %w", id, err)
			}
		}
		return nil
	})
}

// Coverage returns the Dataset's Coverage, sorted by start.
func (s *Store) Coverage(ctx context.Context, id domain.DatasetID) ([]domain.Range, error) {
	out, err := coverageIn(ctx, s.db, id)
	if err != nil {
		return nil, fmt.Errorf("duckdb: coverage of %s: %w", id, err)
	}
	return out, nil
}

// querier is the part of *sql.DB and *sql.Tx that reading needs.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// coverageIn reads a Dataset's stored Coverage ranges, sorted by start.
func coverageIn(ctx context.Context, q querier, id domain.DatasetID) ([]domain.Range, error) {
	res, err := q.QueryContext(ctx, `
		SELECT start_ms, end_ms FROM coverage
		WHERE provider = ? AND symbol = ? AND timeframe = ?
		ORDER BY start_ms, end_ms`, key(id)...)
	if err != nil {
		return nil, err
	}
	defer res.Close()

	var out []domain.Range
	for res.Next() {
		var startMS, endMS int64
		if err := res.Scan(&startMS, &endMS); err != nil {
			return nil, err
		}
		out = append(out, asRange(startMS, endMS))
	}
	return out, res.Err()
}

// OpenTimes yields the open_time of every Bar the Dataset holds inside r, in
// ascending order. A failure is yielded once with a zero time and ends the
// sequence.
func (s *Store) OpenTimes(ctx context.Context, id domain.DatasetID, r domain.Range) iter.Seq2[time.Time, error] {
	return func(yield func(time.Time, error) bool) {
		if r.IsEmpty() {
			return
		}
		startMS, endMS := msRange(r)
		args := append(key(id), startMS, endMS)
		res, err := s.db.QueryContext(ctx, `
			SELECT open_time FROM bars
			WHERE provider = ? AND symbol = ? AND timeframe = ?
			  AND open_time >= ? AND open_time < ?
			ORDER BY open_time`, args...)
		if err != nil {
			yield(time.Time{}, fmt.Errorf("duckdb: open times of %s: %w", id, err))
			return
		}
		defer res.Close()

		for res.Next() {
			var openTime int64
			if err := res.Scan(&openTime); err != nil {
				yield(time.Time{}, fmt.Errorf("duckdb: open times of %s: %w", id, err))
				return
			}
			if !yield(time.UnixMilli(openTime).UTC(), nil) {
				return
			}
		}
		if err := res.Err(); err != nil {
			yield(time.Time{}, fmt.Errorf("duckdb: open times of %s: %w", id, err))
		}
	}
}
