# Where we are at

## Done
- Phase 1 of Market Data Acquisition complete: tickets 01–08 in `.scratch/market-acquisition/issues/` all `done`, one commit each on `main`.
  Domain types → DuckDB Store → Binance Provider → Backfill → gap detection + Complete → Repair/gap status → HTTP API → `agnoforge` CLI.
  `go test -race ./...` green (6 packages); only dependency `github.com/duckdb/duckdb-go/v2`; dep direction domain ← app ← adapters ← cmd.
- Run log in `GOAL_RUN.md`; every spec-silent choice (JSON field names, exit codes, output lines, Parquet columns) in `ASSUMPTIONS.md`.

## Possible next
- User hand-runs spec acceptance #1 against real Binance (first time anything touches `api.binance.com`):
  `go build ./cmd/agnoforge && AGNOFORGE_DB_PATH=/tmp/agnoforge.duckdb ./agnoforge serve`, then
  `./agnoforge data backfill binance BTCUSDT 1m 2024-01-01 2024-02-01 -wait` → expect 44,640 bars; then `data complete …`, `data query … -o /tmp/jan.parquet`.
- User reviews `ASSUMPTIONS.md` §07/§08 (wire format, exit codes, `query` verdict on stdout, flags after positionals) and objects where needed.
- Review follow-ups (not blocking, listed in `GOAL_RUN.md` § Code review): 3d bars not epoch-aligned vs Continuous calendar (needs ADR), EarliestAvailable probed at 1m, non-1121 Binance 4xx → 500, unbounded X-Gaps header / JSON bars buffering.
- Reserved for later (spec): bulk-archive adapter, 1s/1w/1M, persisted Backfill registry.
