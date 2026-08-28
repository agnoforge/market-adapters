Status: todo

# CLI

Depends on: 07. `agnoforge serve` + `agnoforge data …` client subcommands over HTTP; stdlib flag only.

## Done when
- [ ] `agnoforge serve` starts the service on `AGNOFORGE_LISTEN`
- [ ] `agnoforge data providers|backfill|status|cancel|gaps|repair|complete|query` each hit the corresponding route against `AGNOFORGE_URL`
- [ ] `query` writes Parquet to `-o file` and prints the `X-Complete` verdict
- [ ] Non-zero exit and `error: ` on stderr for any non-2xx
- [ ] Argument parsing is stdlib `flag`; no new dependency
- [ ] End-to-end: against a running server with an httptest-backed fake Binance, `backfill` then `complete` returns true
