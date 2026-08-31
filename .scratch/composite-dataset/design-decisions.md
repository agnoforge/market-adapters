# Composite Market Dataset — Design Decisions

Grilling session against `docs/raw/AgnoForge_Composite_Market_Dataset_Requirements.md`, 2026-08-31. All 32 decisions accepted by Stephen. Glossary: `internal/composite/CONTEXT.md`; ADRs 0004, 0005.

## Scope

1. **Live continuation is out of MVP.** Domain reserves a `live` segment kind; no live code in v1.
2. **Composite 1m timeline references source bars, no copy** (ADR-0005). Freeze/snapshot (deferred) is what will copy.
3. **Materialized higher timeframes are persisted**, rebuildable, stamped with `mat_version`.
4. **Catch-up defaults to the base provider**; cross-provider only when explicitly configured; `catch_up: none` disables.
5. **One merge policy: `reject_conflict`.** No overlap allowed at all — segments abut exactly, half-open. Overlapping source data for the same range fails the build.
6. **Calendar boundaries: UTC everywhere; weeks Monday 00:00 UTC (ISO-8601); months 1st 00:00 UTC.** No timezone config in v1.
7. **Provenance is per-segment**, not per-bar. HTF bars' segment span is derivable, not stored.
8. **Strict vs Research = one boolean** (`quality.strict`) changing only the readiness rule; same API surface.
9. **Versioning and freeze deferred.** Config + segments persisted per build = reproducibility-of-explanation. ID model leaves room for `name@version`.
10. **Build is synchronous, in-process, no persisted jobs.** Lifecycle state lives on the dataset row.

## Placement & model

11. **New bounded context at `internal/composite/{domain,app,adapters}`**, same Go module (`github.com/agnos/agnoforge`), same binary, wired in `cmd/agnoforge/serve.go`.
12. **Composite-side calendar-capable Timeframe type** (ADR-0004); converts to `domain.Timeframe` only at the acquisition boundary (always `1m`).
13. **Instrument is minimal**: a validated string on the config; all sources must declare the same one; no registry/mapping table.
14. **Coinbase adapter is not part of this work.** Cross-provider catch-up proven with a fake provider in tests; Coinbase is a later acquisition-side ticket.
15. **Acquisition port is consumer-defined in composite**, mirroring existing `app.Service` methods (`Bars`, `Coverage`, `IsComplete`, `StartBackfill`+`Wait`, `DetectGaps`, `Repair`). `ensureRange` orchestration (incl. `ErrBackfillRunning` race) lives composite-side. Acquisition's surface unchanged.
16. **Lifecycle states: `draft → building → ready | failed`, plus `stale`.** Phase detail via OTel spans, not persisted states.
17. **Composite module owns its own DDL** (`CREATE IF NOT EXISTS` block in its own store adapter), same DuckDB file. No shared schema file.
18. **`materialized_bars(dataset_id, timeframe, open_time, o/h/l/c/v DECIMAL(20,8), mat_version, PK(dataset_id,timeframe,open_time))`**; rebuild = DELETE+INSERT per window; `mat_version` also on dataset row, mismatch ⇒ stale.
19. **Aggregation in DuckDB SQL** (FIRST/MAX/MIN/LAST/SUM over DECIMAL). Emit an HTF bar if ≥1 source bar in window; window overlapping an open gap ⇒ recorded incomplete materialization window (blocks strict readiness, flagged in research).
20. **Inbound surface: REST under `/composites/...` + CLI `agnoforge composite <cmd>`** (CLI as HTTP client, matching existing pattern); JSON + Parquet. No desktop UI.
21. **`end: now` supported in config; each build resolves it** to the last fully closed 1m bar and persists `resolved_end`; completeness/queries judged against it.

## Semantics

22. **Segments are build output**, recomputed and replaced wholesale on each successful build.
23. **`name` is the PK** (kebab-case slug). **Config editable; any edit ⇒ `stale`.**
24. **Missing head**: build first backfills base provider toward `requested_start`; if provider's earliest-available floor limits it — strict: not ready; research: starts where data starts, recorded in quality.  No head catch-up from another provider.
25. **Tail-filling: extend base provider first; catch-up provider takes only what base genuinely cannot supply.** No fallback chains beyond those two.
26. **Transition validation: no overlap, no hole, same instrument, same 1m timeframe. Price delta across transitions recorded only** — no threshold, no failure.
27. **v1 quality fields**: requested_start/end, resolved_end, available_start/end, expected/actual bar counts, coverage_percentage, open_gap_count + gap list, provider_transition_count + per-transition price delta, incomplete materialization windows per timeframe, last_build_time, mode, mat_version.
28. **No separate provenance endpoint** — the ordered segment list in `Get` is the provenance API.
29. **Concurrent builds of one dataset rejected** (in-memory guard). **Stale set only by config edits** — no source-drift watching; rebuild is refresh.
30. **Two-context docs structure**: root `CONTEXT-MAP.md`; acquisition glossary stays at root `CONTEXT.md`; composite glossary at `internal/composite/CONTEXT.md`.
31. **ADRs written**: 0004 (calendar-capable composite Timeframe), 0005 (reference-not-copy). DuckDB-SQL aggregation gets a `ponytail:` comment, not an ADR.
32. **Backtester contract v1 = the REST query endpoint** (`GET /composites/{name}/bars?timeframe=&start=&end=&format=parquet|json`) + quality endpoint. In-process Go API deferred until the backtesting context exists.

33. **Control plane / data plane split**: `AcquisitionPort` carries capabilities only (coverage, completeness, backfill start/wait, gaps); source 1m bars are read read-only in SQL from acquisition's `bars` table in the shared DuckDB file, for both 1m serving and aggregation. Documented in ADR-0005. Fake port in tests needs no `Bars` method; tests seed the real bars table.

## MVP use cases

Create, Get (incl. segments = provenance), List, Build, QueryBars (any materialized tf + 1m), GetQuality, Delete (removes definition + materialized bars, never source data).
