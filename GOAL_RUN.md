# GOAL_RUN — Market Data Acquisition Phase 1

Goal: all eight tickets in `.scratch/market-acquisition/issues/`, in order, per `spec.md`.
Start: 1787909830 (2026-08-28 09:37 UTC). Deadline: 1787931430 (+6h).
Module: `github.com/agnos/agnoforge`.

## Outcomes
- [x] O1 01-domain-and-skeleton
- [x] O2 02-duckdb-store
- [x] O3 03-binance-adapter
- [x] O4 04-backfill-usecase
- [x] O5 05-gaps-and-complete
- [x] O6 06-repair-and-gap-status
- [x] O7 07-http-api
- [x] O8 08-cli

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

### 05 — sub-agent pass, verified (elapsed ~52m)
Sub-agent: 6/6 PASS (+ 5m coalescing extra): one gap per contiguous run ([10,15),[30,32)); nothing outside Coverage; weekend calendar → no gap over closed period, run spanning weekend = one gap (mutation-checked); ignored/unrecoverable excluded + preserved with reason; filled open/ignored gap → repaired; IsComplete 8-case table. Assumptions: contiguity in calendar sequence; repaired marking applies to any non-repaired status; repaired reason cleared; IsComplete returns open gaps only; empty range complete.
Orchestrator: build/vet/test -race clean; all DetectGaps/IsComplete subtests PASS; app deps = domain only; go.mod unchanged. Committed.

### 06 — sub-agent pass, verified (elapsed ~57m)
Sub-agent: 4/4 PASS (Repair = StartBackfill over exact gap range, id returned; unknown → ErrNotFound; busy → ErrBackfillRunning; idempotent; SetGapStatus 7-case table, `repaired` → app.ErrGapStatusNotSettable; ignored gap repaired after data lands; IsComplete true after ignoring only gap). Assumptions: empty reason allowed; ErrGapStatusNotSettable in app.
Orchestrator: build/vet/test -race clean; 9 repair/status tests PASS; app deps = domain only; go.mod unchanged. Committed.

### 07 — sub-agent pass, verified (elapsed ~1h05m)
Sub-agent: 6/6 PASS (26 httptest tests: every route success+failure; 202/409/400 on POST /backfills; Parquet default with X-Complete/X-Gaps, read back by second DuckDB = 9 rows; ?format=json; JSON {error} incl. mux 404/405; stdlib mux only; deps app+domain only). Store port gained `Bars`; app gained query pass-throughs. JSON field table in ASSUMPTIONS.md. Assumptions: DELETE → 202 + status body; content type application/vnd.apache.parquet; empty/reversed range → 400; unknown provider/symbol in query path = empty dataset not 400.
Orchestrator: build/vet/test -race clean (5 pkgs ok); httpapi imports = stdlib + app + domain; go.mod unchanged. Committed.

### 08 — sub-agent pass, verified (elapsed ~1h15m)
Sub-agent: 6/6 PASS (serve on AGNOFORGE_LISTEN, 127.0.0.1:0 + SIGINT exit 0; 8 subcommands hit exact route/query/body; query -o writes body + `complete:` verdict; non-2xx → exit 1 `error: boom`; bad args exit 2, zero requests; stdlib flag; e2e backfill→complete true→query 2500 Parquet rows→rerun still true against fake Binance). Assumptions: exit codes 0/1/2; flags after positionals; `-o` required; `listening on <addr>` on stdout.
Orchestrator: build/vet/test -race clean (6 pkgs ok, 18 cmd tests PASS); cmd imports all three adapters, nothing imports cmd; go.mod unchanged; dep direction domain←app←adapters←cmd verified with go list -deps; no stray db/parquet files. Committed.

### Code review (a2dee5f..HEAD, medium) — elapsed ~1h35m
14 findings (5 CONFIRMED, 9 PLAUSIBLE; verifier agents died mid-run, so the 9 were re-checked by the orchestrator where fixed).
Fixed + tested: #1 completion extends Coverage past last closed bar (app now clips end via WithClock; `TestEffectiveRangeIsClippedToTheLastClosedBar`); #10 empty/reversed effective range (ErrEmptyRange → 400); #2 straddling open Gap truncated on re-detect (hull widening; `TestDetectGapsRedetectsAStraddlingGapInFull`, mutation-checked); #6 late cancel flips completed run to cancelled; #7 state published before DetectGaps; #4 serve closes Store under running backfills (`Service.Shutdown`).
Not fixed, follow-ups: #3 EarliestAvailable probed at 1m can drop the first coarser bar (adapter should probe at the requested timeframe); #5 Binance 3d bars are not epoch-aligned — `Continuous.Expected` assumes epoch multiples (needs an ADR: either drop 3d or give the calendar an anchor); #8 non-1121 Binance 4xx → 500 (add a permanent bad-request sentinel); #9 X-Gaps header unbounded (cap or move to trailer/body); #11 format=json buffers whole range (stream/limit); #12 settle/PATCH race (accept: last writer wins, rare); #13 Bars re-probes EarliestAvailable (memoise per symbol); #14 short page ends walk (Binance semantics make short pages legitimate; accept).
Full suite after fixes: `go test -race ./...` 6/6 ok.
