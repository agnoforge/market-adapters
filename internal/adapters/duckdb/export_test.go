package duckdb

import (
	"context"
	"fmt"
)

// This file is compiled only for tests. It lets the external duckdb_test
// package reach statements the Store port deliberately does not expose, so
// the schema itself can be probed without widening the adapter's API.

// ReapplySchemaForTest runs the schema statements again on an open Store, which
// is what Open does when it meets a database that already holds them.
func (s *Store) ReapplySchemaForTest(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// ExecForTest runs one statement against the store's database.
func (s *Store) ExecForTest(ctx context.Context, stmt string) error {
	_, err := s.db.ExecContext(ctx, stmt)
	return err
}

// PrimaryKeyForTest returns the columns of the primary key on table, in order,
// or nil when the table has none.
func (s *Store) PrimaryKeyForTest(ctx context.Context, table string) ([]string, error) {
	res, err := s.db.QueryContext(ctx, `
		SELECT constraint_column_names FROM duckdb_constraints()
		WHERE table_name = ? AND constraint_type = 'PRIMARY KEY'`, table)
	if err != nil {
		return nil, err
	}
	defer res.Close()

	if !res.Next() {
		return nil, res.Err()
	}
	var columns []any
	if err := res.Scan(&columns); err != nil {
		return nil, err
	}
	out := make([]string, len(columns))
	for i, c := range columns {
		out[i] = fmt.Sprint(c)
	}
	return out, res.Err()
}

// ColumnTypeForTest returns the declared SQL type of one column.
func (s *Store) ColumnTypeForTest(ctx context.Context, table, column string) (string, error) {
	var declared string
	err := s.db.QueryRowContext(ctx, `
		SELECT data_type FROM information_schema.columns
		WHERE table_name = ? AND column_name = ?`, table, column).Scan(&declared)
	return declared, err
}
