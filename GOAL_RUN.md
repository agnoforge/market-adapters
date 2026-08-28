# GOAL_RUN — Market Data Acquisition Phase 1

Goal: all eight tickets in `.scratch/market-acquisition/issues/`, in order, per `spec.md`.
Start: 1787909830 (2026-08-28 09:37 UTC). Deadline: 1787931430 (+6h).
Module: `github.com/agnos/agnoforge`.

## Outcomes
- [x] O1 01-domain-and-skeleton
- [x] O2 02-duckdb-store
- [x] O3 03-binance-adapter
- [x] O4 04-backfill-usecase
- [ ] O5 05-gaps-and-complete
- [ ] O6 06-repair-and-gap-status
- [ ] O7 07-http-api
- [ ] O8 08-cli

## After every ticket
- `go build ./... && go vet ./... && go test ./...` clean
- `go.mod`: only `github.com/duckdb/duckdb-go` + transitive
- deps direction: domain→stdlib; app→domain+ports; adapters→app/domain; cmd names concrete
- tests: no network, temp dir / `:memory:` only

## Budget
Per ticket: 1 sub-agent attempt + 1 correction → inline takeover. 6h total.

## Attempt log

### 01 — sub-agent pass, verified (elapsed ~7m)
Sub-agent: CB1–CB6 all PASS (build/vet/test clean; 13 timeframes + 1s/1w/1M rejected; range ops touching/disjoint; Bar.Validate; Continuous N==N; deps stdlib only; go.mod zero requires). Mutation checks done by agent.
Orchestrator: `go build/vet/test ./...` ok; `go list -deps ./internal/domain` → only itself + stdlib; go.mod no requires; 24 PASS, 0 FAIL on targeted run. Committed.

### 02 — sub-agent pass, verified (elapsed ~19m)
Sub-agent: CB1–CB6 PASS (PK via duckdb_constraints; double upsert 3→3, 44640→44640; coverage merge 7 cases; ReplaceOpenGaps keeps ignored/unrecoverable; Parquet round-trip 50 rows via second DuckDB; TestMain leak guard proven to fire). Judgement calls: decimal read-back rendered at scale 8 (`9.5`→`9.50000000`); `Bars()` reader on concrete store only; UpsertBars re-validates and fails whole batch.
Orchestrator: build/vet/test clean (24 duckdb tests PASS); app deps = domain only; go.mod direct require = duckdb-go/v2 only; no db/parquet files left. Committed.

### 03 — sub-agent pass, verified (elapsed ~28m)
Sub-agent: 8/8 PASS (13 tfs exact; 2500 fixture → pages [1000 1000 500], no dup/skip; in-progress candle clipped; 429/418 Retry-After honoured, 5×500 → error, -1121 → ErrUnknownSymbol 1 req; shared bucket 3001st waits 20ms; earliest = fixture first; invalid bar dropped + slog warn; TestMain blocks non-loopback transport). Assumptions: EarliestAvailable probes at 1m; empty pages not yielded; endTime=end-1ms.
Orchestrator: build/vet/test clean; 18 binance tests PASS under -race; app deps = domain only; go.mod unchanged; no binance/duckdb/kline in app; no Instrument. Committed.

### 04 — sub-agent pass, verified (elapsed ~40m)
Sub-agent: 8/8 PASS (id + clipped range; unknown symbol → error, no registry entry; ErrBackfillRunning + concurrent datasets; per-page persist+coverage checked from inside iterator; cancel keeps landed; failed keeps coverage honest; DetectGaps on completed/cancelled/failed over landed range; rerun identical; app deps no adapter). Mutation-checked. Includes minimal `DetectGaps` core in app/gaps.go (ticket 05 completes it). Assumptions: StartBackfill returns error (→400) for unknown symbol/provider/tf instead of a failed record; run ctx from Background; terminal work on fresh ctx; `Wait(id)` test seam.
Orchestrator: build/vet/test -race clean (15 app tests PASS); app deps = domain only; go.mod unchanged; ponytail comment present. Committed.
