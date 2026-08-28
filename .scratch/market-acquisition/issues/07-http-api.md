Status: todo

# HTTP API adapter

Depends on: 04, 05, 06. Endpoints per spec, net/http stdlib mux, Parquet streaming with X-Complete/X-Gaps headers, JSON option. httptest coverage of each route.

## Done when
- [ ] Every route in spec §HTTP API exists and is exercised by an httptest test, success and failure
- [ ] `POST /backfills` → 202 with id + effective_range; 409 when already running; 400 unknown symbol/timeframe
- [ ] `GET .../bars` streams Parquet by default with `X-Complete` and `X-Gaps` headers; `?format=json` returns JSON
- [ ] Errors are JSON `{error}` with correct status codes
- [ ] Only stdlib `net/http`; no router/framework in `go.mod`
- [ ] `httpapi` imports `internal/app` ports only, never `duckdb` or `binance`
