Status: done

# HTTP API adapter

Depends on: 04, 05, 06. Endpoints per spec, net/http stdlib mux, Parquet streaming with X-Complete/X-Gaps headers, JSON option. httptest coverage of each route.

## Done when
- [x] Every route in spec §HTTP API exists and is exercised by an httptest test, success and failure
- [x] `POST /backfills` → 202 with id + effective_range; 409 when already running; 400 unknown symbol/timeframe
- [x] `GET .../bars` streams Parquet by default with `X-Complete` and `X-Gaps` headers; `?format=json` returns JSON
- [x] Errors are JSON `{error}` with correct status codes
- [x] Only stdlib `net/http`; no router/framework in `go.mod`
- [x] `httpapi` imports `internal/app` ports only, never `duckdb` or `binance`
