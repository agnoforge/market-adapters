# Where we are at

## Done
- Phase 1 of Market Data Acquisition complete: tickets 01–08 in `.scratch/market-acquisition/issues/` all `done`, one commit each on `main`. `go test -race ./...` green; only dependency duckdb-go; dep direction domain ← app ← adapters ← cmd.
- Docs: `README.md` (committed bc585c8) and `docs/architecture-walkthrough.html` (uncommitted).
- 2026-08-28: Developer Playground + tracing grilled and specced — `docs/adr/0003-opentelemetry-in-process-trace-store.md`, `.scratch/playground/spec.md`, issues 01–06.
- Playground ticket 01 (`done`): OpenTelemetry foundation. `serve` installs an always-on TracerProvider and the W3C propagator, the API is wrapped in an otelhttp server span, every response carries `X-Trace-ID` (an inbound `traceparent` is continued), and the CLI prints `trace: <id>` after `error:` on any non-2xx. The SDK and otelhttp are confined to `cmd`; a `go list` test holds that line.
- Playground ticket 02 (`done`): use-case, Provider and Store spans. Every operation the spec names records an inline span — `app.StartBackfill`/`HistoricalBackfill`/`DetectGaps`/`IsComplete`/`Repair`/`Query`, `binance.EarliestAvailable`/`Bars`/`get`, the eight `duckdb.*` ops — carrying `agnoforge.layer` and the domain attributes, with `RecordError` + Error status on any failure. A page fetch is one span whatever it costs: retries and rate-limit waits are events on it. The Backfill worker opens a trace of its own, rooted at `app.HistoricalBackfill` and linked back to the request span.

## Possible next
- Implement `.scratch/playground/issues/03-*.md` onward (`/implement`): the in-process trace store (ring buffer + `SpanProcessor`, lookup by trace id and `agnoforge.backfill.id`), then the playground HTTP surface and UI.
- User commits the walkthrough HTML and ADR 0003.
- User hand-runs spec acceptance #1 against real Binance (see README).
- Review follow-ups from `GOAL_RUN.md` § Code review (3d bar alignment ADR, non-1121 Binance 4xx → 500, unbounded X-Gaps header).
