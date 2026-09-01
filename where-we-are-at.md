# Where we are at

## First real composite build against live data (2026-09-01)
Done: `btcusdt-backtest` — BTC/USD, base `binance:BTCUSDT`, 2024-01-01 → 2026-08-31T23:00Z, strict, timeframes 5m/15m/1h/4h/1d. Stored 1m coverage ran out at 2026-08-30, so 2,820 bars were backfilled first; the build is `ready` at 100% coverage (1,402,500 bars, 0 gaps, 0 transitions, 0 incomplete windows). All five frames exported to Parquet in the session scratchpad. First end-to-end exercise of declare → build → query on real Binance data.
Next: user runs a backtest off the Parquet files; re-run `composite build` to pick up new bars (dataset goes `stale` as sources advance).

## Playground sidebar grouped by module (2026-08-31)
Done: the playground sidebar now shows an "Acquisition" and a "Composite" group header, so the boundary between the two contexts' operations is visible (composite ops were already in the catalog since ticket 08). Tests green. Not committed.
Next: user opens `/playground/` and checks the grouping reads right in both themes.

## Composite inside-out explainer (2026-08-31)
Done: `docs/architectural-walkthrough/composite.html` — a progressive onion explainer for `internal/composite/` in the same style as the acquisition one (24 pages: problem → six domain concepts → use cases → ports → adapters → composition root → full picture → build + bars-query traces → testing → cheat sheet; one SVG growing from a fixed domain centre, dark/light, clickable). Honest notes cover the real asymmetries: the domain imports acquisition's domain words, the AcquisitionPort is control-plane-only with the data plane read in SQL (ADR-0005), no inbound port, in-memory build guard. Verified in a browser, both themes, console clean; commit header `526585f` / 2026-08-31. Cross-linked with acquisition.html. Not committed.
Next: user reads it end to end and flags anything that does not land; commit both walkthrough files together when happy.

## Acquisition walkthrough realigned with restructure (2026-08-31)
Done: `docs/architectural-walkthrough/acquisition.html` updated to the post-restructure code — every package path now `internal/acquisition/{domain,app,adapters}` (text, tables and SVG labels), source-commit line bumped to `526585f` / 2026-08-31, `BackfillRequest` fields corrected, companion link repointed to `docs/deprecated/architecture-walkthrough.html`, and a one-line mention of the sibling `internal/composite/` context added on the composition-root and full-picture pages. All quoted signatures, routes, error sentinels and file lists verified against the Go sources. Not committed.
Next: user opens the page in both themes and skims the changed pages (17, 18, cheat sheet); commit when happy.

## Bounded contexts made symmetric (2026-08-31)
Done: acquisition packages moved from `internal/{domain,app,adapters}` to `internal/acquisition/{domain,app,adapters}`, mirroring `internal/composite/`; its glossary moved to `internal/acquisition/CONTEXT.md` and root `CONTEXT-MAP.md` now points there. Pure move — no behavior change, build/tests green.
Next: nothing pending from this; historical docs (`.scratch/` tickets, ADRs, GOAL_RUN.md) intentionally keep the old paths.

## Composite Market Dataset — module complete (2026-08-31)
Done: tickets 01–08 in `.scratch/composite-dataset/issues/` all implemented. The context lives in `internal/composite/` (domain / app / adapters: duckdb, httpapi, acqport), wired beside acquisition in one process, one port and one DuckDB file by `cmd/agnoforge/serve.go`.

What it does now: declare a Composite Dataset (instrument, base source, optional cross-provider catch-up, requested range with `now`, materialized timeframes, strict/research) and edit or delete it; calendar-capable timeframes (`1w`, `1M`) alongside the fixed ones; a Build that resolves the end, backfills whatever the base source is missing through acquisition's control plane, asks a catch-up provider for the tail alone, assembles ordered Segments, validates the provider boundary, repairs gaps, prices every Transition, judges Quality against the mode and materializes the declared timeframes; a query surface that streams the composite timeline or any materialized frame as Parquet or JSON with state/mode/range headers. Bars never cross the port — the composite store reads acquisition's rows in SQL from the same file (ADR-0005).

Observability: `composite/app` records under its own scope with `agnoforge.layer=composite-app`; a Build's trace is `composite.Build` with one child span per phase (`resolve`, `ensure`, `assemble`, `validate`, `quality`, `materialize`), each carrying the dataset and the resolved range, each marking its own error. Composite operations are in the playground catalog and draw in the existing trace UI; `/composites/**` carries `X-Trace-ID` like every other route. README documents the composite REST surface and CLI beside acquisition's.

Next: user evaluates a real build against real Binance data (`composite create` → `build` → `quality` → `query`, then the trace in `/playground/`); a real second provider adapter (Coinbase) to exercise cross-provider catch-up outside the fake port; the two declaration routes are CLI-only in the playground until a form model can express a nested body.

## Composite Market Dataset — design grilled and settled (2026-08-31)
Done: 32-decision grilling of `docs/raw/AgnoForge_Composite_Market_Dataset_Requirements.md`, all accepted. Recorded in `.scratch/composite-dataset/design-decisions.md`; new bounded-context glossary `internal/composite/CONTEXT.md` + root `CONTEXT-MAP.md`; ADR-0004 (calendar-capable composite Timeframe), ADR-0005 (reference-not-copy). Headlines: no live continuation in MVP, segments reference source bars, materialized HTF persisted via DuckDB SQL, one merge policy (`reject_conflict`, no overlap), sync in-process build, `internal/composite/` in the same module/binary, Coinbase not in scope (fake provider in tests).
Spec written: `.scratch/composite-dataset/spec.md` (`ready-for-agent`) — 33 user stories, seams confirmed (one new seam: fake `AcquisitionPort`; control-plane port / SQL data-plane split added as decision 33 + ADR-0005 update).
Tickets published: `.scratch/composite-dataset/issues/01–08`, all `ready-for-agent`. Frontier: 01 (definitions CRUD) and 02 (calendar Timeframe) can start immediately in parallel; 03 (build) after 01; then 04→05 (catch-up, cross-provider) and 06 (materialization, also needs 02) in parallel; 07 (query) after 06; 08 (observability + docs) last.
Goal prompt written: `.scratch/composite-dataset/goal-prompt.md` (orchestrator + ticket-implementer subagents, 6 cycles/ticket, 10h cap, scope-guarded to `internal/composite/` + wiring; real `agnoforge.duckdb` protected).
All eight tickets have since been implemented — see the section above.

## Done
- Phase 1 of Market Data Acquisition complete: tickets 01–08 in `.scratch/market-acquisition/issues/` all `done`, one commit each on `main`. `go test -race ./...` green; only dependency duckdb-go; dep direction domain ← app ← adapters ← cmd.
- Developer Playground + OpenTelemetry tracing complete: tickets 01–06 in `.scratch/playground/issues/` all `done`, one commit per ticket on `main`. `agnoforge serve` records an always-on trace per request (`httpapi → app → provider/store`, errors marked, a Backfill's worker in its own linked trace), sinks it into a bounded in-process store, and serves `/playground/` — an embedded page that runs any of the ten domain operations and draws the trace behind it. Every response carries `X-Trace-ID`; the CLI prints `trace: <id>` on failure. No OTLP exporter, no backend to run (`docs/adr/0003-opentelemetry-in-process-trace-store.md`).
- Docs: `README.md` (now with a `## Playground` section), `ASSUMPTIONS.md` § Ticket 09 (trace JSON shape, header name, retention), `docs/architecture-walkthrough.html`.

- Default Binance endpoint is now `https://data-api.binance.vision` (market data only, no geo block). `api.binance.com` answers `{"error":403}` from restricted regions; `BINANCE_BASE_URL` still overrides.

- Playground verified in a real browser against live Binance: all ten operations execute, the request/execution traces and span detail render, required-field validation fires, no console errors. Three defects found and fixed: sidebar routes were clipped mid-word, only the *first* errored span was outlined red, and settling a gap produced a doubly-wrapped error message. `GET /backfills/{id}` and `DELETE /backfills/{id}` now report an unknown id identically (`not found: backfill "x"`, via `domain.ErrNotFound`).

## Possible next
- User hand-runs spec acceptance #1 against real Binance (see README).
- Deliberately not built, available as follow-ups: swapping in an OTLP exporter in `cmd` (one seam, no other code changes); a per-trace span cap (`// ponytail:` in `internal/acquisition/adapters/playground/store.go`); the backfill details page (report Phase 5); trace search / console (report Phase 6).
- `docs/gaps_overview.html` — plain-language explainer of what a Gap is (re-pitch of the count-vs-times question), checked in both themes.
- Gap semantics confirmed by reading: a Gap is expected-minus-present per open_time inside Coverage (one Gap per run of consecutive missing open_times), recorded by `DetectGaps` at the end of a Backfill — `GET .../gaps` and `.../complete` read that record, they do not recompute. Bars removed out of band are therefore invisible until the next Backfill over that range.
- Review follow-ups from `GOAL_RUN.md` § Code review (3d bar alignment ADR, non-1121 Binance 4xx → 500, unbounded X-Gaps header).

## Architecture walkthrough — inside-out edition (2026-08-29)
Done: `docs/architecture-inside-out.html` — a second, progressive-disclosure architecture walkthrough (problem → domain types one by one → use cases → why ports → Provider/Store → Binance/DuckDB/HTTP adapters → composition root → full hexagon → two step-by-step runtime traces → testing seams → cheat sheet). Three-pane layout (nav / explanation / one SVG that grows outward from a fixed Domain centre), clickable diagram nodes, ▸ details, dark/light, keyboard ←/→, mobile fallback. States honestly that there is no inbound port interface. Complements, does not replace, `docs/architecture-walkthrough.html`.
Next: user reads it end to end and flags anything that does not land; optionally cross-link it from README / the existing walkthrough; regenerate "source commit" line when the architecture changes.

## README.html (2026-08-30)
Done: `README.html` at repo root — one-page visual onboarding (problem, six vocabulary words on a coverage/gap timeline, run commands, backfill lifecycle, dependency layers, API, doc index) linking to `docs/architecture-inside-out.html`. README.md cross-links it.
Next: user opens it in both themes and flags anything that does not land; keep it in sync when the API or env vars change.

## agno-market-cli skill (2026-08-30, composite added 2026-08-31)
Done: `.claude/skills/agno-market-cli/SKILL.md` — lets an agent drive the `agnoforge` CLI: check/start `serve`, the `data` command table with the complete → backfill → gaps → repair loop and output-parsing gotchas, and (new) a `## Composite datasets` section covering declare → build → query, the five states, strict/research modes, `-catch-up` semantics, whole-declaration `edit`, and the four header lines `composite query` prints before the payload. One skill for both contexts, same binary — deliberately not split.
Next: user invokes `/agno-market-cli` for a composite task and flags anything missing; split the skill only if it outgrows a single read.
