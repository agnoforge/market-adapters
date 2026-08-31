// Package duckdb implements the Composite Market Dataset Store port on the
// same embedded DuckDB database acquisition uses.
//
// This context owns its own schema: its tables are created here, idempotently,
// on every Open, and no statement in this package writes to a table
// acquisition owns. Sharing one file is the accepted data plane (ADR-0005) —
// it is what lets a composite query read source bars in SQL — not shared
// ownership of the schema.
package duckdb
