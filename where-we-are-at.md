# Where we are at

## Done
- Phase 1 of Market Data Acquisition complete: tickets 01–08 in `.scratch/market-acquisition/issues/` all `done`, one commit each on `main`. `go test -race ./...` green; only dependency duckdb-go; dep direction domain ← app ← adapters ← cmd.
- Developer Playground + OpenTelemetry tracing complete: tickets 01–06 in `.scratch/playground/issues/` all `done`, one commit per ticket on `main`. `agnoforge serve` records an always-on trace per request (`httpapi → app → provider/store`, errors marked, a Backfill's worker in its own linked trace), sinks it into a bounded in-process store, and serves `/playground/` — an embedded page that runs any of the ten domain operations and draws the trace behind it. Every response carries `X-Trace-ID`; the CLI prints `trace: <id>` on failure. No OTLP exporter, no backend to run (`docs/adr/0003-opentelemetry-in-process-trace-store.md`).
- Docs: `README.md` (now with a `## Playground` section), `ASSUMPTIONS.md` § Ticket 09 (trace JSON shape, header name, retention), `docs/architecture-walkthrough.html`.

- Default Binance endpoint is now `https://data-api.binance.vision` (market data only, no geo block). `api.binance.com` answers `{"error":403}` from restricted regions; `BINANCE_BASE_URL` still overrides.

- Playground verified in a real browser against live Binance: all ten operations execute, the request/execution traces and span detail render, required-field validation fires, no console errors. Three defects found and fixed: sidebar routes were clipped mid-word, only the *first* errored span was outlined red, and settling a gap produced a doubly-wrapped error message. `GET /backfills/{id}` and `DELETE /backfills/{id}` now report an unknown id identically (`not found: backfill "x"`, via `domain.ErrNotFound`).

## Possible next
- User hand-runs spec acceptance #1 against real Binance (see README).
- Deliberately not built, available as follow-ups: swapping in an OTLP exporter in `cmd` (one seam, no other code changes); a per-trace span cap (`// ponytail:` in `internal/adapters/playground/store.go`); the backfill details page (report Phase 5); trace search / console (report Phase 6).
- `docs/gaps_overview.html` — plain-language explainer of what a Gap is (re-pitch of the count-vs-times question), checked in both themes.
- Gap semantics confirmed by reading: a Gap is expected-minus-present per open_time inside Coverage (one Gap per run of consecutive missing open_times), recorded by `DetectGaps` at the end of a Backfill — `GET .../gaps` and `.../complete` read that record, they do not recompute. Bars removed out of band are therefore invisible until the next Backfill over that range.
- Review follow-ups from `GOAL_RUN.md` § Code review (3d bar alignment ADR, non-1121 Binance 4xx → 500, unbounded X-Gaps header).

## Architecture walkthrough — inside-out edition (2026-08-29)
Done: `docs/architecture-inside-out.html` — a second, progressive-disclosure architecture walkthrough (problem → domain types one by one → use cases → why ports → Provider/Store → Binance/DuckDB/HTTP adapters → composition root → full hexagon → two step-by-step runtime traces → testing seams → cheat sheet). Three-pane layout (nav / explanation / one SVG that grows outward from a fixed Domain centre), clickable diagram nodes, ▸ details, dark/light, keyboard ←/→, mobile fallback. States honestly that there is no inbound port interface. Complements, does not replace, `docs/architecture-walkthrough.html`.
Next: user reads it end to end and flags anything that does not land; optionally cross-link it from README / the existing walkthrough; regenerate "source commit" line when the architecture changes.
