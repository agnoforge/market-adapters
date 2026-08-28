# Developer Playground & Tracing — spec

Settled in a grilling session 2026-08-28. Source requirements report: the user's "AgnoForge Developer Playground" document (§1–37); this spec records only the decisions, which win over the report where they differ.

## Scope
Report phases 1–4 as one feature. Phases 5 (backfill details view) and 6 (console) are out.

## Not changing
- The eight domain routes and their wire format (`ASSUMPTIONS.md` §07).
- CLI syntax: positional, as it is (`agnoforge data backfill binance BTCUSDT 1m 2024-01-01 2024-02-01 -wait`). The report's `--flag` form is not adopted.
- `CONTEXT.md`: Trace/Span/Operation are tooling vocabulary, not domain terms.

## Tracing (ADR 0003)
- `go.opentelemetry.io/otel` API imported by `internal/app` and adapters; `sdk/trace` + `otelhttp` only in `cmd`. No OTLP exporter dependency.
- Always-on sampler. Inbound W3C `traceparent` honoured (otelhttp default).
- Every response carries `X-Trace-ID`. CLI prints `trace: <id>` on any non-2xx.
- Span names are package-prefixed: `app.StartBackfill`, `app.DetectGaps`, `app.IsComplete`, `app.Repair`, `app.Query`; `binance.EarliestAvailable`, `binance.Bars`, `binance.get` (one span per page fetch, retries and rate-limit waits are **events** on it); `duckdb.UpsertBars`, `duckdb.ExtendCoverage`, `duckdb.OpenTimes`, `duckdb.ReplaceOpenGaps`, `duckdb.ExportParquet`, `duckdb.Bars`, `duckdb.Coverage`, `duckdb.Gaps`.
- Every span carries `agnoforge.layer` ∈ `httpapi|app|provider|store`. Domain attributes: `agnoforge.provider|symbol|timeframe|range.start|range.end|backfill.id|page.bar_count|gap.count`; standard `http.*`/`server.address` from otelhttp/semconv.
- Error semantics: any error returned from an app/provider/store operation → `RecordError` + status Error on that span. HTTP server span: 5xx only (otelhttp default).
- Backfill worker: new trace, root `app.HistoricalBackfill`, span Link to the `app.StartBackfill` span, attribute `agnoforge.backfill.id`.
- Nothing sensitive exists to redact today (Binance public endpoints); never put request/response bodies or headers into attributes.

## Trace store
- One package `internal/adapters/playground`: a `sdktrace.SpanProcessor` (OnStart + OnEnd, so running spans are visible), the ring buffer, the HTTP handlers and the embedded UI.
- Retention: last 256 traces, evict oldest trace. `// ponytail: per-trace span cap if a multi-year backfill ever hurts`.
- Lookup by trace id and by `agnoforge.backfill.id`.

## Playground HTTP surface (always on, under `/playground/`)
- `GET /playground/` — embedded `index.html`, vanilla JS, no build step.
- `GET /playground/operations` — JSON from a hand-written `[]Operation{Name, Method, Path, Params[]{Name, In(path|query|body), Required, Example}, CLI template}`. Ten operations, one per domain route.
- `GET /playground/traces/{id}` — trace as `{trace_id, spans[]{span_id, parent_id, name, layer, start, end|null, status, attributes, events[], links[]}}`; 404 if unknown.
- `GET /playground/traces?backfill_id=…` — same shape, array.

## UI (single page)
Operation list → form (required/optional, path/query/body marked) → Execute → panels: HTTP request preview + `curl` + CLI string (copy buttons); response status/duration/headers/body (JSON pretty; Parquet shown as headers + "binary, N bytes"); trace id → waterfall tree coloured by layer, click span → detail panel (ids, times, status, attributes, events, error); first Error span highlighted; for `POST /backfills` a "execution trace" link that polls `?backfill_id=`.

## Verification
- Go test: fake provider + in-memory processor; `POST /backfills` yields a request trace with `httpapi → app.StartBackfill → binance.EarliestAvailable`; after `Wait`, `?backfill_id=` returns an execution trace containing `duckdb.UpsertBars` and a Link to the request span; an unknown-symbol request yields Error status on `binance.EarliestAvailable` and `X-Trace-ID` matches.
- Catalog test: every operation's `Method Path` (examples substituted) is routed (not 404/405).
- UI checked by hand in a browser.
