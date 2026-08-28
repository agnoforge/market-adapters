Status: todo

# Backfill use case

Depends on: 02, 03. In-memory registry, one running per Dataset (conflict error), per-page upsert + ExtendCoverage, states running/completed/failed/cancelled via ctx, progress counters, triggers DetectGaps on terminal state. Tests with fake Provider + real DuckDB.

## Done when
- [ ] `POST`-equivalent Start returns an id and effective (clipped) range; unknown symbol → immediate `failed`
- [ ] Second Start on a running Dataset → `ErrBackfillRunning`; different Datasets run concurrently
- [ ] Each provider page is persisted and Coverage extended before the next page is requested
- [ ] Cancelling via ctx leaves landed pages and their Coverage in place; state = `cancelled`
- [ ] Provider error mid-run → `failed` with `last_error`; Coverage reflects only landed pages
- [ ] DetectGaps runs over the landed range on every terminal state
- [ ] Re-running the same Backfill yields identical bar count and Coverage
- [ ] Tests use a fake Provider and real in-memory DuckDB; `internal/app` imports no adapter
