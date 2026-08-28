package duckdb

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agnos/agnoforge/internal/domain"
)

// ExportParquet streams the Dataset's Bars inside r to w as a Parquet file,
// ordered by open_time. The file is written by DuckDB's own COPY … TO, so the
// encoding is DuckDB's, not ours.
//
// Columns: open_time BIGINT (UTC epoch milliseconds), then open, high, low,
// close and volume as DECIMAL(20,8). The Dataset is not repeated in the file:
// every Bar in it belongs to the Dataset that was exported.
func (s *Store) ExportParquet(ctx context.Context, id domain.DatasetID, r domain.Range, w io.Writer) error {
	dir, err := os.MkdirTemp("", "agnoforge-parquet-")
	if err != nil {
		return fmt.Errorf("duckdb: export %s: %w", id, err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "bars.parquet")

	startMS, endMS := msRange(r)
	stmt := fmt.Sprintf(`
		COPY (
			SELECT open_time, "open", high, low, "close", volume
			FROM bars
			WHERE provider = %s AND symbol = %s AND timeframe = %s
			  AND open_time >= %d AND open_time < %d
			ORDER BY open_time
		) TO %s (FORMAT PARQUET)`,
		quote(id.Provider), quote(string(id.Symbol)), quote(string(id.Timeframe)),
		startMS, endMS, quote(path))

	if _, err := s.db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("duckdb: export %s: %w", id, err)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("duckdb: export %s: %w", id, err)
	}
	defer file.Close()

	if _, err := io.Copy(w, file); err != nil {
		return fmt.Errorf("duckdb: export %s: %w", id, err)
	}
	return nil
}

// quote renders s as a SQL string literal. COPY … TO takes neither its
// destination nor its predicates as bound parameters, so the few values that
// reach it are escaped here instead.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
