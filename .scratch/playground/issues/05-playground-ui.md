Status: todo
Blocked by: 03, 04

# Playground UI

`go:embed index.html` served at `GET /playground/`. Vanilla JS: operation list, form (required/optional, path/query/body marked, client-side required check), Execute; panels for request preview, curl, CLI (copy buttons); response (status, duration, headers, pretty JSON or "binary, N bytes" for Parquet); trace id → waterfall tree coloured by `agnoforge.layer`, click → span detail (ids, times, status, attributes, events, error), first Error span highlighted; for Start Backfill an "execution trace" button polling `?backfill_id=` until all spans ended. Light/dark via `prefers-color-scheme`.

## Done when
- [ ] `GET /playground/` returns the page; the page works from `agnoforge serve` with no other process
- [ ] Go test: page and `/playground/operations` load; a `POST /backfills` executed via the same endpoints produces a trace id the UI can fetch
- [ ] Manual: start backfill BTCUSDT 1m one day, see request trace, open execution trace, watch it complete; run an unknown symbol, see the red span
- [ ] No Node toolchain, no external asset
