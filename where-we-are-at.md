# Where we are at

## Done
- Phase 1 of Market Data Acquisition complete: tickets 01–08 in `.scratch/market-acquisition/issues/` all `done`, one commit each on `main`. `go test -race ./...` green; only dependency duckdb-go; dep direction domain ← app ← adapters ← cmd.
- Developer Playground + OpenTelemetry tracing complete: tickets 01–06 in `.scratch/playground/issues/` all `done`, one commit per ticket on `main`. `agnoforge serve` records an always-on trace per request (`httpapi → app → provider/store`, errors marked, a Backfill's worker in its own linked trace), sinks it into a bounded in-process store, and serves `/playground/` — an embedded page that runs any of the ten domain operations and draws the trace behind it. Every response carries `X-Trace-ID`; the CLI prints `trace: <id>` on failure. No OTLP exporter, no backend to run (`docs/adr/0003-opentelemetry-in-process-trace-store.md`).
- Docs: `README.md` (now with a `## Playground` section), `ASSUMPTIONS.md` § Ticket 09 (trace JSON shape, header name, retention), `docs/architecture-walkthrough.html`.

- Default Binance endpoint is now `https://data-api.binance.vision` (market data only, no geo block). `api.binance.com` answers `{"error":403}` from restricted regions; `BINANCE_BASE_URL` still overrides.

## Possible next
- User opens `http://localhost:8080/playground/` and evaluates by eye against real Binance: a BTCUSDT 1m backfill over one day, watch the request trace then the execution trace to `complete`, then an unknown symbol and look for the red span.
- User hand-runs spec acceptance #1 against real Binance (see README).
- Deliberately not built, available as follow-ups: swapping in an OTLP exporter in `cmd` (one seam, no other code changes); a per-trace span cap (`// ponytail:` in `internal/adapters/playground/store.go`); the backfill details page (report Phase 5); trace search / console (report Phase 6).
- Review follow-ups from `GOAL_RUN.md` § Code review (3d bar alignment ADR, non-1121 Binance 4xx → 500, unbounded X-Gaps header).
