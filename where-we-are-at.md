# Where we are at

## Done
- Phase 1 of Market Data Acquisition complete: tickets 01–08 in `.scratch/market-acquisition/issues/` all `done`, one commit each on `main`. `go test -race ./...` green; only dependency duckdb-go; dep direction domain ← app ← adapters ← cmd.
- Docs: `README.md` (committed bc585c8) and `docs/architecture-walkthrough.html` (uncommitted).
- 2026-08-28: Developer Playground + tracing grilled and specced — `docs/adr/0003-opentelemetry-in-process-trace-store.md`, `.scratch/playground/spec.md`, issues 01–06.
- Playground ticket 01 (`done`): OpenTelemetry foundation. `serve` installs an always-on TracerProvider and the W3C propagator, the API is wrapped in an otelhttp server span, every response carries `X-Trace-ID` (an inbound `traceparent` is continued), and the CLI prints `trace: <id>` after `error:` on any non-2xx. The SDK and otelhttp are confined to `cmd`; a `go list` test holds that line.
- Playground ticket 02 (`done`): use-case, Provider and Store spans. Every operation the spec names records an inline span — `app.StartBackfill`/`HistoricalBackfill`/`DetectGaps`/`IsComplete`/`Repair`/`Query`, `binance.EarliestAvailable`/`Bars`/`get`, the eight `duckdb.*` ops — carrying `agnoforge.layer` and the domain attributes, with `RecordError` + Error status on any failure. A page fetch is one span whatever it costs: retries and rate-limit waits are events on it. The Backfill worker opens a trace of its own, rooted at `app.HistoricalBackfill` and linked back to the request span.
- Playground ticket 03 (`done`): the in-process trace store and its query endpoints. `internal/adapters/playground` is a `sdktrace.SpanProcessor` — OnStart files a span as running with a null end, OnEnd finalises it — over a ring of the last 256 traces, indexed by trace id and by `agnoforge.backfill.id`. `serve` sinks every span into it and registers `GET /playground/traces/{id}` and `GET /playground/traces?backfill_id=` on the same mux as the domain routes; `/playground/` is itself excluded from tracing so reading the store does not fill it. The package sees spans and HTTP and nothing else — no Service, no Store port, no Provider — and is the one package under `internal/` allowed to name the OTel SDK.
- Playground ticket 04 (`done`): the operation catalog. A hand-written `playground.Operations` — ten entries, one per domain route in `httpapi`'s own order — carries a glossary name, the method, the mux's own `{param}` path, its path/query/body params with working examples (`binance BTCUSDT 1m`, 2024-01-01 → 2024-01-02), and the equivalent positional CLI command (`null` for `…/coverage` and `PATCH /gaps/{id}`, which the CLI has no command for). Served at `GET /playground/operations` as JSON, registered beside the trace routes. A test substitutes every example into the real API and proves each one reaches a handler rather than the mux's 404 or a 405; the catalog itself still imports nothing but `net/http`.

## Possible next
- Implement `.scratch/playground/issues/05-*.md` onward (`/implement`): the playground UI, which reads this catalog.
- User hand-checks the catalog: `agnoforge serve`, then `curl localhost:8080/playground/operations`.
- User hand-checks the playground: `agnoforge serve`, make a request, then `curl localhost:8080/playground/traces/<X-Trace-ID>`.
- User commits the walkthrough HTML and ADR 0003.
- User hand-runs spec acceptance #1 against real Binance (see README).
- Review follow-ups from `GOAL_RUN.md` § Code review (3d bar alignment ADR, non-1121 Binance 4xx → 500, unbounded X-Gaps header).
