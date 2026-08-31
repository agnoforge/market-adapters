# Composite Market Dataset

Assembles one or more source Datasets into a derived, consumer-ready dataset — extended toward the present, validated, and materialized into higher timeframes — without ever mutating source data. Downstream consumers (backtesting, charting, live trading) depend on this abstraction, not on providers.

## Language

**Composite Dataset**:
A derived dataset assembled from source Datasets according to a declared configuration, identified by a unique name. Always visibly derived — never presented as original exchange data.
_Avoid_: merged dataset, virtual dataset, view

**Instrument**:
The logical market a Composite Dataset represents (`BTC/USD`), declared in its configuration. Every source in the configuration must declare the same Instrument; the user asserts the provider symbols really are that market.
_Avoid_: symbol, pair, ticker

**Timeframe**:
The duration of one bar, including calendar frames: fixed frames (`1m` … `1d`) plus `1w` (Monday 00:00 UTC, ISO-8601) and `1M` (1st of month, 00:00 UTC). Broader than Acquisition's fixed-duration Timeframe.
_Avoid_: interval, resolution, period

**Segment**:
A contiguous, provider-attributed slice of a composite timeline, computed by a Build. Segments are ordered, abut exactly (half-open, no overlap, no hole), and are the provenance record. Kinds: `base`, `catch_up` (`live` reserved).
_Avoid_: chunk, slice, partition

**Base**:
The one existing 1-minute source Dataset a Composite Dataset is built on. The Build extends the base provider's coverage as far as it can before any other provider contributes.

**Catch-up**:
Filling the tail between what the base provider can supply and the resolved end. Defaults to the base provider; a different provider only when explicitly configured.
_Avoid_: fallback, continuation, backfill (that word is Acquisition's)

**Transition**:
The boundary between two Segments from different providers. Validated at Build time (no overlap, no hole, same Instrument, same canonical timeframe); the price delta across it is recorded, not enforced.

**Build**:
Reconciling a Composite Dataset's configuration against actual source coverage: ensure coverage via Acquisition, assemble Segments, validate, materialize. Synchronous and repeatable — rebuild is refresh.
_Avoid_: sync, refresh, compile

**Resolved End**:
The concrete end timestamp a Build derived from the requested end (`now` resolves to the last fully closed 1m bar at build time). Completeness and queries are judged against it.

**Materialization**:
Deriving higher-timeframe bars from the composite 1-minute timeline. Deterministic, persisted, rebuildable, stamped with a materialization version. A window overlapping an open Gap yields a bar flagged as an incomplete window, never a silent omission.
_Avoid_: aggregation, rollup, resampling

**Strict / Research** (Mode):
The readiness rule. Strict: any open Gap, invalid Transition, or incomplete materialization prevents `ready`. Research: the dataset may become `ready` with acknowledged imperfections, visibly flagged — never presented as equivalent to strict.

**Stale**:
A previously built Composite Dataset whose configuration has since been edited. Only edits cause staleness; source-data drift does not. A Build clears it.
_Avoid_: dirty, outdated, expired

**Quality**:
The metadata a Build computes about a Composite Dataset: requested vs available range, bar counts, coverage percentage, open gaps, transitions and their price deltas, incomplete materialization windows, mode, build time.

**Provenance**:
The answer to "where did this bar come from": locate the bar's open time in the ordered Segment list. Per-segment, not per-bar.

**Bars Query**:
Reading a Composite Dataset's bars by name, Timeframe and half-open range — JSON or streamed Parquet. `1m` is served by reading the referenced source bars across the Segments; every other Timeframe is served from Materialization. One surface, and the consumer never learns which of the two answered. Omitted bounds mean the dataset's own resolved range.
_Avoid_: fetch, export, download

## Boundaries

- Source Datasets, Backfill, Coverage, Gap detection and repair belong to Market Data Acquisition. This context requests those capabilities through a port; it never calls providers.
- Live continuation, dataset versioning, and freeze/snapshot are deliberately out of this context's current scope; the model reserves room for them (`live` segment kind, `name@version` identity).
