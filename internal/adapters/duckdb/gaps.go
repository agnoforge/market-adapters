package duckdb

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/domain"
)

// ReplaceOpenGaps deletes the Dataset's open Gaps intersecting r and inserts
// gaps in their place, in one transaction. Gaps in any other status survive,
// as do open Gaps that only touch r without sharing an instant with it.
//
// A Gap's ID is assigned by the database: whatever ID the caller put on a Gap
// in gaps is ignored, because these are new records.
func (s *Store) ReplaceOpenGaps(ctx context.Context, id domain.DatasetID, r domain.Range, gaps []domain.Gap) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if !r.IsEmpty() {
			startMS, endMS := msRange(r)
			args := append(key(id), endMS, startMS)
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM gaps
				WHERE provider = ? AND symbol = ? AND timeframe = ?
				  AND status = 'open'
				  AND start_ms < ? AND end_ms > ?`, args...); err != nil {
				return fmt.Errorf("duckdb: replace open gaps of %s: %w", id, err)
			}
		}
		for _, g := range gaps {
			startMS, endMS := msRange(g.Range)
			args := append(key(id), startMS, endMS, string(g.Status), g.Reason)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO gaps (provider, symbol, timeframe, start_ms, end_ms, status, reason)
				VALUES (?,?,?,?,?,?,?)`, args...); err != nil {
				return fmt.Errorf("duckdb: replace open gaps of %s: %w", id, err)
			}
		}
		return nil
	})
}

// Gaps returns the Dataset's Gaps matching f, sorted by start.
func (s *Store) Gaps(ctx context.Context, id domain.DatasetID, f app.GapFilter) ([]domain.Gap, error) {
	where := `provider = ? AND symbol = ? AND timeframe = ?`
	args := key(id)
	if f.Status != nil {
		where += ` AND status = ?`
		args = append(args, string(*f.Status))
	}
	if f.Range != nil {
		startMS, endMS := msRange(*f.Range)
		where += ` AND start_ms < ? AND end_ms > ?`
		args = append(args, endMS, startMS)
	}

	res, err := s.db.QueryContext(ctx, `
		SELECT id, provider, symbol, timeframe, start_ms, end_ms, status, reason
		FROM gaps WHERE `+where+` ORDER BY start_ms, end_ms, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("duckdb: gaps of %s: %w", id, err)
	}
	defer res.Close()

	var out []domain.Gap
	for res.Next() {
		g, err := scanGap(res)
		if err != nil {
			return nil, fmt.Errorf("duckdb: gaps of %s: %w", id, err)
		}
		out = append(out, g)
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("duckdb: gaps of %s: %w", id, err)
	}
	return out, nil
}

// Gap returns one Gap by id.
func (s *Store) Gap(ctx context.Context, gapID int64) (domain.Gap, error) {
	res, err := s.db.QueryContext(ctx, `
		SELECT id, provider, symbol, timeframe, start_ms, end_ms, status, reason
		FROM gaps WHERE id = ?`, gapID)
	if err != nil {
		return domain.Gap{}, fmt.Errorf("duckdb: gap %d: %w", gapID, err)
	}
	defer res.Close()

	if !res.Next() {
		if err := res.Err(); err != nil {
			return domain.Gap{}, fmt.Errorf("duckdb: gap %d: %w", gapID, err)
		}
		return domain.Gap{}, fmt.Errorf("duckdb: gap %d: %w", gapID, domain.ErrNotFound)
	}
	g, err := scanGap(res)
	if err != nil {
		return domain.Gap{}, fmt.Errorf("duckdb: gap %d: %w", gapID, err)
	}
	return g, nil
}

// SetGapStatus moves a Gap to s and records the operator's reason.
func (s *Store) SetGapStatus(ctx context.Context, gapID int64, status domain.GapStatus, reason string) error {
	if _, err := domain.ParseGapStatus(string(status)); err != nil {
		return fmt.Errorf("duckdb: set status of gap %d: %w", gapID, err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE gaps SET status = ?, reason = ? WHERE id = ?`, string(status), reason, gapID)
	if err != nil {
		return fmt.Errorf("duckdb: set status of gap %d: %w", gapID, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("duckdb: set status of gap %d: %w", gapID, err)
	}
	if changed == 0 {
		return fmt.Errorf("duckdb: set status of gap %d: %w", gapID, domain.ErrNotFound)
	}
	return nil
}

// scanGap reads one gaps record in the column order every gap query selects.
func scanGap(res *sql.Rows) (domain.Gap, error) {
	var g domain.Gap
	var symbol, timeframe, status string
	var startMS, endMS int64
	if err := res.Scan(&g.ID, &g.Dataset.Provider, &symbol, &timeframe,
		&startMS, &endMS, &status, &g.Reason); err != nil {
		return domain.Gap{}, err
	}
	g.Dataset.Symbol = domain.Symbol(symbol)
	g.Dataset.Timeframe = domain.Timeframe(timeframe)
	g.Range = asRange(startMS, endMS)
	g.Status = domain.GapStatus(status)
	return g, nil
}
