package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/domain"
	_ "github.com/duckdb/duckdb-go/v2" // registers the "duckdb" database/sql driver
)

// Store satisfies the port the use cases depend on.
var _ app.Store = (*Store)(nil)

// Store is the DuckDB implementation of the app.Store port. Open it once and
// share it: *sql.DB is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open connects to the DuckDB database at path, creating the file and the
// schema when they do not exist. Pass ":memory:" for an ephemeral database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("duckdb: open %q: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("duckdb: create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// key returns the three Dataset columns in the order every statement binds
// them.
func key(id domain.DatasetID) []any {
	return []any{id.Provider, string(id.Symbol), string(id.Timeframe)}
}

// msRange returns r as the pair of epoch-millisecond bounds the tables store.
func msRange(r domain.Range) (int64, int64) {
	return r.Start.UnixMilli(), r.End.UnixMilli()
}

// asRange rebuilds a half-open Range from stored epoch milliseconds.
func asRange(startMS, endMS int64) domain.Range {
	return domain.Range{
		Start: time.UnixMilli(startMS).UTC(),
		End:   time.UnixMilli(endMS).UTC(),
	}
}

// inTx runs fn inside a transaction, rolling back on any failure.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// upsertBatch is how many Bars one INSERT OR REPLACE statement carries. Nine
// bound values per Bar keeps a batch well inside DuckDB's statement limits
// while making the per-statement overhead negligible; the Appender is not
// worth its complexity at this size.
const upsertBatch = 500

// UpsertBars writes bars for one Dataset, replacing whatever was stored at the
// same open_time. Running it twice with the same input changes nothing.
func (s *Store) UpsertBars(ctx context.Context, id domain.DatasetID, bars []domain.Bar) error {
	ctx, span := tracer().Start(ctx, "duckdb.UpsertBars",
		trace.WithAttributes(append(datasetAttrs(id), pageBarCountKey.Int(len(bars)))...))
	defer span.End()

	if len(bars) == 0 {
		return nil
	}
	for i, b := range bars {
		if err := b.Validate(); err != nil {
			return fail(span, fmt.Errorf("duckdb: upsert bars for %s: bar %d: %w", id, i, err))
		}
	}
	return fail(span, s.inTx(ctx, func(tx *sql.Tx) error {
		for start := 0; start < len(bars); start += upsertBatch {
			end := min(start+upsertBatch, len(bars))
			batch := bars[start:end]

			values := make([]string, 0, len(batch))
			args := make([]any, 0, len(batch)*9)
			for _, b := range batch {
				values = append(values, "(?,?,?,?,?,?,?,?,?)")
				args = append(args, id.Provider, string(id.Symbol), string(id.Timeframe),
					b.OpenTime.UnixMilli(), b.Open, b.High, b.Low, b.Close, b.Volume)
			}
			stmt := `INSERT OR REPLACE INTO bars
				(provider, symbol, timeframe, open_time, "open", high, low, "close", volume)
				VALUES ` + strings.Join(values, ",")
			if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
				return fmt.Errorf("duckdb: upsert bars for %s: %w", id, err)
			}
		}
		return nil
	}))
}

// Bars returns the Dataset's Bars inside r, ordered by open_time.
// Prices come back cast to VARCHAR, which DuckDB renders at the column's full
// scale, so every value is a decimal string with exactly eight fractional
// digits.
func (s *Store) Bars(ctx context.Context, id domain.DatasetID, r domain.Range) ([]domain.Bar, error) {
	ctx, span := tracer().Start(ctx, "duckdb.Bars", trace.WithAttributes(datasetRangeAttrs(id, r)...))
	defer span.End()

	startMS, endMS := msRange(r)
	args := append(key(id), startMS, endMS)
	res, err := s.db.QueryContext(ctx, `
		SELECT open_time,
		       CAST("open" AS VARCHAR), CAST(high AS VARCHAR),
		       CAST(low AS VARCHAR), CAST("close" AS VARCHAR),
		       CAST(volume AS VARCHAR)
		FROM bars
		WHERE provider = ? AND symbol = ? AND timeframe = ?
		  AND open_time >= ? AND open_time < ?
		ORDER BY open_time`, args...)
	if err != nil {
		return nil, fail(span, fmt.Errorf("duckdb: bars for %s: %w", id, err))
	}
	defer res.Close()

	var out []domain.Bar
	for res.Next() {
		var openTime int64
		var b domain.Bar
		if err := res.Scan(&openTime, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			return nil, fail(span, fmt.Errorf("duckdb: bars for %s: %w", id, err))
		}
		b.OpenTime = time.UnixMilli(openTime).UTC()
		out = append(out, b)
	}
	if err := res.Err(); err != nil {
		return nil, fail(span, fmt.Errorf("duckdb: bars for %s: %w", id, err))
	}
	return out, nil
}
