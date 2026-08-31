# 06 — Higher-timeframe materialization

**What to build:** A successful Build materializes the configured higher Timeframes from the composite 1-minute timeline into persisted, rebuildable bars — including calendar-correct weekly and monthly bars — with honest handling of windows that overlap missing data. The first bar-deriving code in the codebase.

**Blocked by:** 02 — Calendar-capable composite Timeframe; 03 — Build a single-segment dataset.

**Status:** ready-for-agent

- [ ] Aggregation runs as DuckDB SQL reading the source bars read-only (decision 33 / ADR-0005): first open, max high, min low, last close, summed volume per window, decimal-exact — no float arithmetic anywhere
- [ ] Fixed frames aggregate on epoch-aligned windows; `1w` and `1M` aggregate on the calendar boundaries defined by ticket 02 (Monday 00:00 UTC weeks, month starts), in SQL
- [ ] A window containing at least one source bar emits a bar; a window overlapping an open Gap is recorded as an incomplete materialization window in Quality — incomplete windows block strict readiness and are flagged (not hidden, not silently emitted) in research mode
- [ ] Materialized bars are keyed by dataset, timeframe, and open time, stamped with the materialization version; the version also lives on the dataset row, and a mismatch marks the dataset `stale`
- [ ] Rebuild is idempotent: re-running a build replaces the affected windows and converges to identical bars
- [ ] Store-level tests against real temp DuckDB cover calendar grouping, decimal exactness, gap-overlapping windows, and rebuild convergence; harness tests prove build → quality reports per-timeframe incomplete windows; `go test -race ./...` green
