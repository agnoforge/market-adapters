# Where we are at

## Done
- Grilling on the Market Data Acquisition PRD complete. Outcome: `CONTEXT.md` glossary, ADR-0001 (no scheduler), ADR-0002 (DuckDB via cgo), Phase 1 spec `.scratch/market-acquisition/spec.md`, issues 01–08.
- **Ticket 01 — domain types and module skeleton.** Go module `github.com/agnos/agnoforge` with the hexagonal layout from the spec (`cmd/agnoforge`, `internal/domain`, `internal/app`, `internal/adapters/{binance,duckdb,httpapi}`; only `internal/domain` has real code). Domain covers Timeframe (13 fixed durations, rejects 1s/1w/1M), half-open Range with union/intersect/subtract and the slice helpers, DatasetID, Bar with exact `big.Rat` validation, Gap + GapStatus, the TradingCalendar port with a Continuous (24/7) implementation, and the four sentinel errors. Table-driven tests throughout; `internal/domain` imports stdlib only and `go.mod` still has zero dependencies.
- **Ticket 02 — Store port and DuckDB adapter.** `internal/app/ports.go` declares the `Store` port (plus `GapFilter`) in domain terms only; `go list -deps ./internal/app` is still stdlib + domain, and nothing there names a database. `internal/adapters/duckdb` implements it against DuckDB 1.5.5 (`github.com/duckdb/duckdb-go/v2`, the only direct dependency): one file, three tables (`bars` PK on the Dataset + open_time with `DECIMAL(20,8)` prices, `coverage`, `gaps` on a sequence), idempotent `INSERT OR REPLACE` upserts, coverage union-merged through `domain.MergeRanges`, half-open gap intersection, and Parquet export via DuckDB's own `COPY … TO`. 24 tests, all on `:memory:`, with a `TestMain` guard that fails the package if a run leaves a database or Parquet file on disk.

## Possible next
- Ticket 03 — the Binance provider adapter (REST `/api/v3/klines`, paging, token bucket, retry/`Retry-After`).
- User may want to eyeball `ASSUMPTIONS.md` for the two judgement calls worth a second opinion: the store re-runs `Bar.Validate` and fails a whole batch on one bad Bar, and prices read back at full `DECIMAL(20,8)` scale (`9.5` in, `9.50000000` out).
- Reserved-for-later list lives at the bottom of the spec (Binance bulk-archive adapter, 1w/1M timeframes, persisted backfills).
