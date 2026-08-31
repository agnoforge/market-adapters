package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
	_ "github.com/duckdb/duckdb-go/v2" // registers the "duckdb" database/sql driver
)

// Store satisfies the port the composite use cases depend on.
var _ app.Store = (*Store)(nil)

// Store is the DuckDB implementation of the composite app.Store port. Open it
// once and share it: *sql.DB is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open connects to the DuckDB database at path and applies this context's own
// schema, creating whatever is not there yet. It is safe to run against the
// database acquisition already opened — the statements are idempotent and
// touch none of acquisition's tables. Pass ":memory:" for an ephemeral
// database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("composite duckdb: open %q: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("composite duckdb: create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// columns is the row every read of a declaration selects, in the order scan
// reads them.
const columns = `name, instrument,
	base_provider, base_symbol, base_timeframe,
	catch_up_kind, catch_up_provider, catch_up_symbol, catch_up_timeframe,
	requested_start_ms, requested_end_ms, timeframes, mode, state,
	resolved_end_ms, last_error, mat_version, created_at_ms, updated_at_ms`

// CreateDataset writes a new declaration. The name is checked and inserted in
// one transaction, so the identity rule is decided against the same snapshot
// the row lands in.
func (s *Store) CreateDataset(ctx context.Context, d domain.Dataset) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM composite_datasets WHERE name = ?`, d.Name.String()).Scan(&count); err != nil {
			return fmt.Errorf("composite duckdb: create %q: %w", d.Name, err)
		}
		if count > 0 {
			return fmt.Errorf("%w: %q", domain.ErrDuplicateName, d.Name)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO composite_datasets (`+columns+`)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, values(d)...); err != nil {
			return fmt.Errorf("composite duckdb: create %q: %w", d.Name, err)
		}
		return nil
	})
}

// Dataset returns one declaration by name.
func (s *Store) Dataset(ctx context.Context, name domain.Name) (domain.Dataset, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM composite_datasets WHERE name = ?`, name.String())
	d, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Dataset{}, fmt.Errorf("%w: %q", domain.ErrNotFound, name)
	}
	if err != nil {
		return domain.Dataset{}, fmt.Errorf("composite duckdb: dataset %q: %w", name, err)
	}
	return d, nil
}

// Datasets returns every declaration, ordered by name.
func (s *Store) Datasets(ctx context.Context) ([]domain.Dataset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM composite_datasets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("composite duckdb: datasets: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Dataset, 0)
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("composite duckdb: datasets: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("composite duckdb: datasets: %w", err)
	}
	return out, nil
}

// UpdateDataset replaces the configuration and state of an existing
// declaration. The name it is keyed by never changes: it is the identity.
func (s *Store) UpdateDataset(ctx context.Context, d domain.Dataset) error {
	res, err := s.db.ExecContext(ctx, updateStatement, append(values(d)[1:], d.Name.String())...)
	if err != nil {
		return fmt.Errorf("composite duckdb: update %q: %w", d.Name, err)
	}
	return notFoundIfUntouched(res, d.Name)
}

// updateStatement replaces every column of a declaration but its name, which
// is the identity and never changes. The bound values are values(d) without
// the name, then the name to key by — the order CreateDataset writes in.
const updateStatement = `
	UPDATE composite_datasets SET
		instrument = ?,
		base_provider = ?, base_symbol = ?, base_timeframe = ?,
		catch_up_kind = ?, catch_up_provider = ?, catch_up_symbol = ?, catch_up_timeframe = ?,
		requested_start_ms = ?, requested_end_ms = ?, timeframes = ?, mode = ?, state = ?,
		resolved_end_ms = ?, last_error = ?, mat_version = ?, created_at_ms = ?, updated_at_ms = ?
	WHERE name = ?`

// DeleteDataset removes a declaration and everything a Build derived from it.
// Only rows this context owns are touched: no source Dataset can be reached
// from here.
func (s *Store) DeleteDataset(ctx context.Context, name domain.Name) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, table := range append([]string{"composite_segments", "composite_materialized_bars"}, qualityTables...) {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM `+table+` WHERE dataset = ?`, name.String()); err != nil {
				return fmt.Errorf("composite duckdb: delete %q: %w", name, err)
			}
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM composite_datasets WHERE name = ?`, name.String())
		if err != nil {
			return fmt.Errorf("composite duckdb: delete %q: %w", name, err)
		}
		return notFoundIfUntouched(res, name)
	})
}

// notFoundIfUntouched turns a statement that matched no row into
// domain.ErrNotFound.
func notFoundIfUntouched(res sql.Result, name domain.Name) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("composite duckdb: %q: %w", name, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %q", domain.ErrNotFound, name)
	}
	return nil
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

// values flattens a declaration into the bound values of one row, in the
// order columns names them.
func values(d domain.Dataset) []any {
	cfg := d.Config
	return []any{
		d.Name.String(), cfg.Instrument.String(),
		cfg.Base.Provider, cfg.Base.Symbol.String(), cfg.Base.Timeframe.String(),
		string(cfg.CatchUp.Kind), cfg.CatchUp.Source.Provider,
		cfg.CatchUp.Source.Symbol.String(), cfg.CatchUp.Source.Timeframe.String(),
		cfg.RequestedStart.UnixMilli(), endValue(cfg.RequestedEnd),
		joinTimeframes(cfg.Timeframes), cfg.Mode.String(), d.State.String(),
		nullableMS(d.ResolvedEnd), d.LastError, d.MatVersion,
		d.CreatedAt.UnixMilli(), d.UpdatedAt.UnixMilli(),
	}
}

// endValue is the stored requested end: NULL for `now`, epoch milliseconds
// for a fixed instant.
func endValue(e domain.RequestedEnd) any {
	if e.Now {
		return nil
	}
	return e.At.UnixMilli()
}

// row is what both QueryRow and Rows offer scan.
type row interface{ Scan(dest ...any) error }

// scan rebuilds one declaration from its row.
func scan(r row) (domain.Dataset, error) {
	var (
		d          domain.Dataset
		name       string
		instrument string
		baseProv   string
		baseSym    string
		baseTF     string
		cuKind     string
		cuProv     string
		cuSym      string
		cuTF       string
		startMS    int64
		endMS      sql.NullInt64
		frames     string
		mode       string
		state      string
		resolvedMS sql.NullInt64
		lastError  sql.NullString
		matVersion sql.NullInt64
		createdMS  int64
		updatedMS  int64
	)
	if err := r.Scan(&name, &instrument,
		&baseProv, &baseSym, &baseTF,
		&cuKind, &cuProv, &cuSym, &cuTF,
		&startMS, &endMS, &frames, &mode, &state,
		&resolvedMS, &lastError, &matVersion, &createdMS, &updatedMS); err != nil {
		return domain.Dataset{}, err
	}
	d.Name = domain.Name(name)
	d.State = domain.State(state)
	if resolvedMS.Valid {
		d.ResolvedEnd = time.UnixMilli(resolvedMS.Int64).UTC()
	}
	d.LastError = lastError.String
	d.MatVersion = int(matVersion.Int64)
	d.CreatedAt = time.UnixMilli(createdMS).UTC()
	d.UpdatedAt = time.UnixMilli(updatedMS).UTC()
	d.Config = domain.Config{
		Instrument: domain.Instrument(instrument),
		Base: domain.Source{
			Instrument: domain.Instrument(instrument),
			Provider:   baseProv,
			Symbol:     acq.Symbol(baseSym),
			Timeframe:  domain.Timeframe(baseTF),
		},
		CatchUp:        catchUp(cuKind, cuProv, cuSym, cuTF, instrument),
		RequestedStart: time.UnixMilli(startMS).UTC(),
		RequestedEnd:   domain.NowEnd(),
		Timeframes:     splitTimeframes(frames),
		Mode:           domain.Mode(mode),
	}
	if endMS.Valid {
		d.Config.RequestedEnd = domain.FixedEnd(time.UnixMilli(endMS.Int64))
	}
	return d, nil
}

// catchUp rebuilds the catch-up declaration. Only an explicitly configured
// catch-up has a Source at all — the base and none kinds carry the empty one
// they were stored as, so what comes back is what was declared.
//
// A stored Source needs no Instrument column: every Source in a configuration
// declares the dataset's own Instrument, or the configuration was refused.
func catchUp(kind, provider, symbol, timeframe, instrument string) domain.CatchUp {
	out := domain.CatchUp{Kind: domain.CatchUpKind(kind)}
	if out.Kind == domain.CatchUpSource {
		out.Source = domain.Source{
			Instrument: domain.Instrument(instrument),
			Provider:   provider,
			Symbol:     acq.Symbol(symbol),
			Timeframe:  domain.Timeframe(timeframe),
		}
	}
	return out
}

// joinTimeframes spells a materialized set as one column value.
func joinTimeframes(frames []domain.Timeframe) string {
	out := make([]string, 0, len(frames))
	for _, tf := range frames {
		out = append(out, tf.String())
	}
	return strings.Join(out, ",")
}

// splitTimeframes reads one back. An empty column is an empty set, not a set
// holding the empty timeframe.
func splitTimeframes(v string) []domain.Timeframe {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]domain.Timeframe, 0, len(parts))
	for _, p := range parts {
		out = append(out, domain.Timeframe(p))
	}
	return out
}
