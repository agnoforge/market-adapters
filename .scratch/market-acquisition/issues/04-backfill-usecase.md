Status: todo

# Backfill use case

Depends on: 02, 03. In-memory registry, one running per Dataset (conflict error), per-page upsert + ExtendCoverage, states running/completed/failed/cancelled via ctx, progress counters, triggers DetectGaps on terminal state. Tests with fake Provider + real DuckDB.
