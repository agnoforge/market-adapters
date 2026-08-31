# GOAL_RUN — composite-dataset tickets 01–08

Goal: build Composite Market Dataset bounded context per `.scratch/composite-dataset/spec.md`, 33 decisions, ADRs 0004/0005. One ticket-implementer subagent per ticket, sequential 01→08. Orchestrator verifies everything.

## Budget
- Start: 1788157603 (2026-08-31 06:26:43 UTC)
- Deadline: 1788193603 (+10h)
- Max 6 verify cycles per ticket. Blocked blocker stops run.

## Remaining tickets (at start)
All 8 present, all `Status: ready-for-agent`, all checkboxes unticked:
- 01-definitions-crud.md
- 02-composite-timeframe.md
- 03-build-single-segment.md
- 04-catchup-base-provider.md
- 05-cross-provider-catchup.md
- 06-materialization.md
- 07-query-bars.md
- 08-observability-onboarding.md

## Outcome checklist
- [x] O1 remaining tickets identified (this file)
- [x] O2 ticket 01: definitions CRUD (REST+CLI, validation, uniqueness, edit⇒stale, DDL) — verified 06:50 UTC
- [x] O3 ticket 02: calendar Timeframe (1w/1M, boundaries, no acquisition conversion for calendar) — verified 06:55 UTC
- [x] O4 ticket 03: single-segment Build (AcquisitionPort control-plane only, lifecycle, quality) — verified 07:18 UTC
- [x] O5 ticket 04: base-provider catch-up (head/tail, ErrBackfillRunning, modes, failure) — verified 07:34 UTC
- [x] O6 ticket 05: cross-provider catch-up (transitions, overlap/hole rejection, delta) — verified 07:53 UTC
- [ ] O7 ticket 06: materialization (DuckDB SQL, decimal, 1w/1M, mat_version, convergence)
- [ ] O8 ticket 07: query surface (bars JSON+Parquet, quality, CLI, errors)
- [ ] O9 ticket 08: observability (OTel API only, spans, playground, README)
- [ ] O10 per-ticket commits + ticket files updated
- [ ] O11 scope guard (only allowed paths touched; acquisition internals + go.mod unchanged)

## Interpretations
- Ticket 01 assumptions (subagent, accepted): `1m` rejected as materialized tf (glossary: materialization = higher frames); omitted `mode` defaults `strict`; catch-up wire shape `{"kind": base|none|source}`, omitted = base; edit is PUT whole-config; composite domain imports acquisition `Symbol`/`Range` value types read-only; timeframe.go minimal (ticket 02 owns arithmetic); only `composite_datasets` table (03/06 add theirs).

## Deviations
- `.gitignore` line 8 `agnoforge` → `/agnoforge`: bare pattern matched at every depth, silently ignoring ALL new files under `cmd/agnoforge/`. Fix required for O10 (CLI files must be committable); root binary still ignored (verified `git check-ignore`). Outside O11 list — investigated, accepted as necessary.

## Attempt log
- [05] cycle 1: subagent added domain.ValidateTransitions (hole/overlap/instrument/timeframe, ErrTransitionInvalid→409), catchUp phase in assemble (base extended first, catch-up asked only the true tail), Quality.Transitions w/ per-transition close/open/delta, delta computed in DuckDB SQL over DECIMAL (CAST result to VARCHAR — no float; verified by grep: only float is coverage percentage over bar counts). Orchestrator verified: 5 named scenario tests PASS -v (e2e shape, overlap, hole, delta, base-only unchanged); full suite green; scope clean. Accepted assumptions: invalid Transition fails both modes; catch-up segment not silently clipped; empty base = NotReady; identical catch-up source = base catch-up; unpriced transition allowed. PASS.
- [04] cycle 1: subagent added Service.ensure/repair/acquire (catchup.go), ErrBackfillBusy sentinel + bounded retry (WithBackfillRetry, default 2s×150), acqport maps acquisition ErrBackfillRunning→busy + ErrEmptyRange→ErrNothingToAcquire. Orchestrator verified: all 7 scenario tests PASS by name (-v run: no-backfill, tail, head, shortfall strict+research, terminal failure, concurrent race); full `go test -race -count=1 ./...` green; acquisition paths diff empty. Accepted assumptions: nothing-to-acquire absorbed (floor semantics); bounded busy-retry; repair only when incomplete; sequential backfills. Subagent ran 2 mutation checks. PASS.
- [03] cycle 1: subagent built Segment/Quality domain, Build use case, AcquisitionPort (6 control-plane methods: Coverage, Completeness, StartBackfill, WaitBackfill, DetectGaps, RepairGap — verified by interface inspection, no bar reading; bars via Store.SourceBars read-only SQL), acqport in-process adapter, DDL (segments/quality tables + ALTER ADD COLUMN IF NOT EXISTS migration), POST /composites/{name}/build, CLI build. Orchestrator verified: full `go test -race -count=1 ./...` exit 0; both-mode harness tests present (TestStrictBuildFailsWithTheGapsReported, TestResearchBuildIsReadyWithItsGapsListedAndVisiblyResearch, TestBuildAssemblesOneBaseSegmentAndTheDatasetBecomesReady); scope clean. Accepted assumptions: failed state rebuildable; not-ready build = 409 w/ gaps in msg; failed strict build persists Quality but no segments; empty-supply range fails both modes. PASS.
- [02] cycle 1: subagent added calendar.go (WindowStart/WindowStarts/Window over iter.Seq), Acquisition() conversion (map w/o calendar entries — impossible by construction), Duration()=0 for calendar frames, Fixed(). Boundary tables: Monday weeks (2024-12-30, epoch Thursday, leap days 2024/2000), months 28/29/30/31 incl. 1900+2100 century rule, tiling test (no hole/overlap over 15 months). Subagent ran mutation check (3 deliberate breaks each caught). Orchestrator verified: `go test -race -count=1 ./internal/composite/...` all ok; tables inspected in calendar_test.go; scope clean. Interpretations accepted: WindowStarts aligns range start DOWN (overlap not containment — materialization needs partial windows flagged, story 17); Duration()=0 for calendar. PASS.
- [01] cycle 1: subagent built domain/app/duckdb/httpapi + CLI + wiring. Evidence: harness tests TestCreateRejects (13 subtests), TestCreateRefusesANameAlreadyTaken, TestEditingABuiltDatasetMarksItStale, TestDeleteRemovesTheDefinition, TestTheSchemaIsIdempotentOverTheAcquisitionFile; e2e serveOn test. Orchestrator verified: `go test -race ./...` exit 0 all pkgs ok; test names inspected in composites_test.go; scope clean except .gitignore (accepted, see Deviations). PASS.
