Status: done
Blocked by: 01

# Use-case, provider and store spans

Inline spans per spec §Tracing naming: `app.*` use cases, `binance.*` (one `binance.get` span per page fetch with `retry` / `rate_limit_wait` events), `duckdb.*` store ops. `agnoforge.layer` on every span plus the domain attributes. Any returned error → `RecordError` + Error status. Backfill worker starts a new trace rooted at `app.HistoricalBackfill` with a Link to the `app.StartBackfill` span and `agnoforge.backfill.id`.

## Done when
- [x] Test with fake provider + `sdktrace` in-memory exporter: `POST /backfills` trace contains `httpapi → app.StartBackfill → binance.EarliestAvailable` (parent ids checked)
- [x] After `Wait`, a separate trace exists whose root is `app.HistoricalBackfill`, has `agnoforge.backfill.id`, a Link to the request span, and children `binance.Bars`/`binance.get`, `duckdb.UpsertBars`, `duckdb.ExtendCoverage`, `app.DetectGaps`
- [x] Unknown symbol → `binance.EarliestAvailable` span has status Error with the error recorded; the HTTP span is not Error (400)
- [x] `binance.get` records one event per retry attempt and per rate-limit wait
- [x] No span attribute contains a request/response body or header value
