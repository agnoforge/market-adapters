# Where we are at

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

## agno-market-cli skill (2026-08-30)
Done: `.claude/skills/agno-market-cli/SKILL.md` — lets an agent drive the `agnoforge data` CLI: check/start `serve`, command table with real timeframes and exit codes, the complete → backfill → gaps → repair loop, and the output-parsing gotcha that `complete`/`query` print a human first line before the payload (`tail -n +2 | jq`). Commands and the jq recipe verified against the running service.
Next: user invokes `/agno-market-cli` (or just asks for bars) and flags anything missing.
