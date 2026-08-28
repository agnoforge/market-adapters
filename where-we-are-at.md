# Where we are at

## Done
- Grilling on the Market Data Acquisition PRD complete. Outcome: `CONTEXT.md` glossary, ADR-0001 (no scheduler), ADR-0002 (DuckDB via cgo), Phase 1 spec `.scratch/market-acquisition/spec.md`, issues 01–08.
- **Ticket 01 — domain types and module skeleton.** Go module `github.com/agnos/agnoforge` with the hexagonal layout from the spec (`cmd/agnoforge`, `internal/domain`, `internal/app`, `internal/adapters/{binance,duckdb,httpapi}`; only `internal/domain` has real code). Domain covers Timeframe (13 fixed durations, rejects 1s/1w/1M), half-open Range with union/intersect/subtract and the slice helpers, DatasetID, Bar with exact `big.Rat` validation, Gap + GapStatus, the TradingCalendar port with a Continuous (24/7) implementation, and the four sentinel errors. Table-driven tests throughout; `internal/domain` imports stdlib only and `go.mod` still has zero dependencies.

## Possible next
- Ticket 02 `02-duckdb-store.md` — the Store adapter (this is where the first and only dependency, `github.com/duckdb/duckdb-go`, arrives).
- Reserved-for-later list lives at the bottom of the spec (Binance bulk-archive adapter, 1w/1M timeframes, persisted backfills).
