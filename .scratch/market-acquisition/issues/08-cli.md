Status: done

# CLI

Depends on: 07. `agnoforge serve` + `agnoforge data …` client subcommands over HTTP; stdlib flag only.

## Done when
- [x] `agnoforge serve` starts the service on `AGNOFORGE_LISTEN`
- [x] `agnoforge data providers|backfill|status|cancel|gaps|repair|complete|query` each hit the corresponding route against `AGNOFORGE_URL`
- [x] `query` writes Parquet to `-o file` and prints the `X-Complete` verdict
- [x] Non-zero exit and `error: ` on stderr for any non-2xx
- [x] Argument parsing is stdlib `flag`; no new dependency
- [x] End-to-end: against a running server with an httptest-backed fake Binance, `backfill` then `complete` returns true
