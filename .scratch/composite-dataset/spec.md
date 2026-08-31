# Composite Market Dataset — MVP spec

Status: ready-for-agent

Vocabulary: `internal/composite/CONTEXT.md` (composite context) and `CONTEXT.md` (acquisition context), mapped in `CONTEXT-MAP.md`. Decisions: `.scratch/composite-dataset/design-decisions.md` (33 accepted decisions), `docs/adr/0004`, `docs/adr/0005`. Source requirements: `docs/raw/AgnoForge_Composite_Market_Dataset_Requirements.md`.

## Problem Statement

A researcher who wants to backtest BTC/USD has 1-minute Binance source data in acquisition, but a backtest needs more than acquisition offers: the range extended to the present (possibly finishing with another provider when the base one stops), gaps accounted for honestly, higher timeframes (5m…1M) derived consistently, and a single dataset name to point the backtester at — without the backtester knowing which provider supplied which period or how bars were derived. Today every consumer would have to stitch coverage, backfills, provider switches, and aggregation together itself, differently each time.

## Solution

A new Composite Market Dataset bounded context in the same repository, module, and binary. The user declares a Composite Dataset by name: an Instrument, a base 1-minute source Dataset, a requested range (fixed end or `now`), an optional catch-up provider, the Timeframes to materialize, and strict or research mode. A synchronous Build reconciles that declaration against reality: it asks acquisition (through a port) to extend coverage, assembles ordered provider-attributed Segments over referenced source bars, validates every Transition, materializes higher-timeframe bars — including calendar-correct 1w/1M — into persisted, rebuildable tables, computes Quality metadata, and marks the dataset `ready` (or `failed`, per mode). Consumers query bars by dataset name and timeframe over REST (JSON or Parquet) or CLI; the ordered Segment list is the Provenance answer.

## User Stories

1. As a researcher, I want to create a named Composite Dataset from an explicit configuration (instrument, base source, range, catch-up, timeframes, mode), so that dataset assembly is declared once instead of improvised per experiment.
2. As a researcher, I want the base to be an existing 1-minute source Dataset, so that already-acquired history is reused instead of re-downloaded.
3. As a researcher, I want to request a range ending at `now`, so that the dataset tracks the present without me editing the config before every build.
4. As a researcher, I want each Build to record the concrete Resolved End it used, so that I always know exactly which timeline a build produced.
5. As a researcher, I want the Build to extend the base provider's coverage toward my requested start and end before anything else, so that the dataset is as single-source as possible.
6. As a researcher, I want catch-up to default to the base provider, so that the common case needs no extra configuration.
7. As a researcher, I want to explicitly configure a different catch-up provider, so that the timeline can continue past the point where the base provider's data ends.
8. As a researcher, I want cross-provider assembly to be impossible unless I configured it, so that no foreign data ever enters a dataset silently.
9. As a researcher, I want the Build to fail loudly when source segments would overlap, so that conflicting provider data is surfaced rather than merged by guesswork.
10. As a researcher, I want Segments to abut exactly with no holes and no overlaps, so that the 1-minute composite timeline is a single continuous sequence I can trust.
11. As a researcher, I want every Transition validated (same Instrument, same timeframe, no hole, no overlap) and its price delta recorded, so that provider boundaries are visible and measurable, never hidden.
12. As a researcher, I want a strict-mode dataset to refuse readiness while any open Gap, invalid Transition, or incomplete materialization exists, so that a backtest never runs on data I believed was complete but wasn't.
13. As a researcher, I want a research-mode dataset to become ready with its imperfections listed and flagged, so that I can explore imperfect history without pretending it is pristine.
14. As a researcher, I want a research-mode dataset to be visibly distinguishable from a strict one, so that imperfect data can never masquerade as complete.
15. As a researcher, I want higher timeframes (5m, 15m, 30m, 1h, 4h, 1d, 1w, 1M) materialized from the composite 1-minute timeline, so that every timeframe agrees with the same underlying bars.
16. As a researcher, I want weekly bars to start Monday 00:00 UTC and monthly bars on the 1st at 00:00 UTC, so that calendar timeframes match charting-industry convention and are reproducible.
17. As a researcher, I want a materialized window that overlaps an open Gap flagged as incomplete rather than silently emitted or silently skipped, so that derived bars never hide missing source data.
18. As a researcher, I want materialization to be deterministic and rebuildable from the 1-minute timeline, so that derived data is never an irreversible source of truth.
19. As a researcher, I want the dataset's Quality metadata (coverage, bar counts, gaps, transitions, incomplete windows, build time, mode), so that I can judge fitness before running anything against it.
20. As a researcher, I want the ordered Segment list with every dataset, so that "where did this bar come from?" is answerable by timestamp for any bar, including derived ones.
21. As a researcher, I want editing a built dataset's configuration to mark it stale until the next Build, so that what's served never silently diverges from what's declared.
22. As a researcher, I want to rebuild a dataset on demand, so that refreshing toward `now` is an explicit, observable act.
23. As a researcher, I want a second concurrent Build of the same dataset rejected with a clear error, so that builds can't interleave and corrupt state.
24. As a researcher, I want to list my Composite Datasets and get any one by name with its state, config, segments, and quality, so that dataset construction is queryable, not an implicit operation.
25. As a researcher, I want to delete a Composite Dataset (its definition and materialized bars, never source data), so that abandoned experiments don't accumulate.
26. As a backtesting engine, I want to query bars by dataset name, timeframe, and range — JSON or Parquet — so that I consume one abstraction with zero knowledge of providers, gaps, or storage.
27. As a backtesting engine, I want the 1-minute timeline queryable through the same endpoint as materialized timeframes, so that all resolutions share one contract.
28. As a charting client, I want the same query surface, so that charts and backtests can never disagree about what the data is.
29. As an operator or agent, I want CLI commands mirroring the REST surface (create, build, list, get, query, quality), so that datasets are scriptable the same way acquisition is.
30. As an operator, I want each Build traced with the repo's OpenTelemetry conventions and phase-level spans, so that a slow or failed build is diagnosable in the playground like any other operation.
31. As an operator, I want a failed Build to leave the dataset in a `failed` state with the error preserved, so that failures are inspectable after the fact.
32. As a researcher, I want the persisted config plus per-build segments and quality to explain exactly what any build used, so that historical results remain explainable even before freeze/versioning exists.
33. As a future live-trading system, I want the segment model to already reserve a live continuation kind, so that adding live data later extends the model instead of reworking it.

## Implementation Decisions

All 33 decisions in `design-decisions.md` govern; the load-bearing ones:

- New bounded context `internal/composite/{domain,app,adapters}` in the existing Go module and binary, wired in the existing composition root, following the acquisition context's hexagonal conventions (domain ← app ← adapters ← cmd, stdlib-only domain).
- **Composite-side Timeframe** (ADR-0004): fixed frames plus calendar `1w`/`1M`; UTC; Monday weeks; month starts on the 1st. Converts to acquisition's Timeframe only at the port boundary (always `1m`).
- **Control plane / data plane split** (decision 33, ADR-0005): a consumer-defined `AcquisitionPort` for capabilities only — coverage, completeness, backfill start/wait, gap detection/repair. Source 1-minute bars are read **read-only in SQL from acquisition's bars table** in the shared DuckDB file, both for 1m serving and aggregation. A future service split swaps this store adapter, not the domain.
- **Reference, not copy** (ADR-0005): the 1m composite timeline is segment metadata over source bars. Materialized higher-timeframe bars ARE persisted, keyed `(dataset, timeframe, open_time)` with a materialization version; rebuild is delete+insert per window; aggregation is DuckDB SQL (first open / max high / min low / last close / sum volume over `DECIMAL(20,8)`).
- **Build semantics**: synchronous, in-process, no job table; one in-memory guard per dataset. Order: resolve end (`now` → last closed 1m bar, persisted as Resolved End) → extend base provider coverage toward requested start and end → configured catch-up provider fills only the tail the base genuinely cannot supply → assemble segments (replaced wholesale) → validate transitions (`reject_conflict`; no overlap, no hole, same Instrument, price delta recorded not enforced) → materialize → compute quality → `ready`/`failed` per mode.
- **Missing head**: base provider is backfilled toward the requested start; if its earliest-available floor limits it — strict: not ready; research: dataset starts where data starts, recorded in Quality.
- **Identity & lifecycle**: `name` (kebab-case slug) is the primary key, leaving room for `name@version` later. States: `draft → building → ready | failed`, plus `stale` (set only by config edits; no source-drift watching). Config is editable; any edit ⇒ stale.
- **Instrument** is a validated string every source in the config must match — no registry, no symbol mapping.
- **Storage**: composite context owns its own DDL (`CREATE IF NOT EXISTS`) in its own store adapter, same DuckDB file: dataset definitions (config, state, resolved end, mat version), segments, materialized bars, quality.
- **Inbound surface**: REST `/composites` (create, list, get incl. segments, build, delete, `bars?timeframe=&start=&end=&format=json|parquet`, quality) + CLI `agnoforge composite <cmd>` as an HTTP client, mirroring the acquisition adapter pattern. Get-with-segments is the entire provenance API.
- **Quality fields**: requested start/end, resolved end, available start/end, expected/actual bar counts, coverage percentage, open gap count + list, transition count + per-transition price delta, incomplete materialization windows per timeframe, last build time, mode, materialization version.
- **Observability**: OTel API only, own tracer scope, own `agnoforge.layer` value, phase-level spans per Build, per ADR-0003 conventions.
- **Cross-provider catch-up** ships architecturally (providers are opaque names) and is proven with a fake second provider in tests; no Coinbase adapter in this work.

## Testing Decisions

A good test exercises external behavior at the highest seam and asserts on observable outcomes (HTTP responses, persisted rows, returned errors) — never on internals like call order, private state, or SQL text. One new seam total: the `AcquisitionPort` interface.

- **Primary seam — composite HTTP harness**: real composite REST handlers + real composite app service + real temp-file DuckDB (acquisition schema seeded with source bars + composite schema) + fake `AcquisitionPort` scripting coverage/backfill/gap behavior, including a fake second provider for cross-provider catch-up. Most acceptance flows live here: create→build→query, `now` resolution, catch-up tail-splitting, head shortfall in both modes, strict vs research readiness, stale-on-edit, concurrent-build rejection, delete. Prior art: the acquisition `httpapi` harness tests (real service + real DuckDB + fake provider over `httptest`).
- **Composite DuckDB store tests**: SQL aggregation correctness against real temp DuckDB — calendar 1w/1M grouping, incomplete windows over gaps, decimal exactness, rebuild idempotence (delete+insert converges), read-only reads of the acquisition bars table. Prior art: the acquisition `duckdb` adapter tests.
- **Domain unit tests**: composite Timeframe boundary arithmetic (Monday-UTC week starts around year boundaries, month lengths, alignment of window starts), segment abutment/validation rules. Prior art: acquisition domain and app tests with fakes.
- Everything runs under `go test -race ./...` with no network.

## Out of Scope

Live continuation (WebSockets, forming bars, historical-to-live handover) — model reserves the `live` segment kind only. Dataset versioning, freeze/snapshot. Coinbase (or any new) acquisition adapter. Merge policies beyond `reject_conflict`; provider ranking, price-quality scoring, anomaly correction. Price-discontinuity *enforcement* (delta is recorded only). Head catch-up from a non-base provider. Automatic staleness from source-data drift. Instrument registry / symbol mapping. Desktop UI. Persistent build jobs, message brokers, separate repositories or deployment.

## Further Notes

- Acquisition's surface is unchanged by this work; `ensureRange` orchestration (including the concurrent-backfill race, `ErrBackfillRunning`) lives composite-side behind the port.
- A source repair changes composite query results without composite-side action — accepted for v1, documented in ADR-0005; freeze is the future fix.
- The DuckDB-SQL aggregation choice carries a `ponytail:` comment naming the upgrade path (Go-side aggregation) instead of an ADR.
- After implementation, update `where-we-are-at.md` and cross-link the new REST/CLI surface from the README, per repo convention.
