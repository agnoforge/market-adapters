---
status: accepted
---
# DuckDB (via cgo) as the historical bar store

The PRD chose DuckDB for a desktop app; the product became a headless Go service, which removed the "no local DB server" rationale but not the rest: embedded (no ops), columnar aggregation, and native Parquet export, which is how we hand millions of bars to other services (`COPY … TO` behind the query endpoint). We accept the cgo dependency of `github.com/duckdb/duckdb-go` and the cross-compilation friction it brings. Layout: one file, one `bars` table keyed `(provider, symbol, timeframe, open_time)`, plus `coverage` and `gaps` keyed the same way; no partitioning until a query is measurably slow.

## Considered options

- SQLite via pure-Go `modernc.org/sqlite` (rejected: row store, no Parquet export, weak for range aggregation)
- Parquet files only, no database (rejected: no cheap idempotent upsert or PK)
- PostgreSQL + TimescaleDB (deferred: only if this becomes shared/multi-writer)
