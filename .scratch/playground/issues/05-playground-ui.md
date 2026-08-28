Status: done
Blocked by: 03, 04

# Playground UI

`go:embed index.html` served at `GET /playground/`. Vanilla JS: operation list, form (required/optional, path/query/body marked, client-side required check), Execute; panels for request preview, curl, CLI (copy buttons); response (status, duration, headers, pretty JSON or "binary, N bytes" for Parquet); trace id → waterfall tree coloured by `agnoforge.layer`, click → span detail (ids, times, status, attributes, events, error), first Error span highlighted; for Start Backfill an "execution trace" button polling `?backfill_id=` until all spans ended. Light/dark via `prefers-color-scheme`.

## Done when
- [x] `GET /playground/` returns the page; the page works from `agnoforge serve` with no other process
      — `TestThePageAndTheCatalogLoad` (200, `text/html; charset=utf-8`, body byte-identical to `index.html`);
      `go build -o $S/agnoforge ./cmd/agnoforge` then `AGNOFORGE_LISTEN=127.0.0.1:0 agnoforge serve` and
      `curl -D - http://127.0.0.1:49802/playground/` → `HTTP/1.1 200 OK`, `Content-Type: text/html; charset=utf-8`,
      25487 bytes, `diff` against `index.html` identical; `/playground/operations` → `200 application/json`;
      `/providers` → `X-Trace-Id` whose `/playground/traces/{id}` answers the trace. One process, no other.
- [x] Go test: page and `/playground/operations` load; a `POST /backfills` executed via the same endpoints produces a trace id the UI can fetch
      — `TestThePageAndTheCatalogLoad` (both loads) and `TestExecutingABackfillProducesATraceTheUICanFetch`
      (real `app.Service` + `duckdb.Open(":memory:")` + fake Provider + `httpapi.New` + `otelhttp` on one mux,
      mirroring `serve.go`: `POST /backfills` → 202 + `X-Trace-Id`; `GET /playground/traces/{id}` → 200 holding
      `app.StartBackfill`; after `Wait`, `?backfill_id=` holds a trace rooted at `app.HistoricalBackfill` with
      every span ended). `go test -race ./internal/adapters/playground/` ok.
- [x] Manual: start backfill BTCUSDT 1m one day, see request trace, open execution trace, watch it complete; run an unknown symbol, see the red span
- [x] No Node toolchain, no external asset
      — `TestNoNodeToolchainAndNoExternalAsset`: `os.Stat("package.json")` is not-exist, and `index.html` matches
      none of `src="http|href="http|@import|url\(http`; `TestThePageAndTheCatalogLoad` also asserts the page holds
      an inline `<script>` and no `<script src=` and no `<link`. One embedded file, no siblings, no build step.
