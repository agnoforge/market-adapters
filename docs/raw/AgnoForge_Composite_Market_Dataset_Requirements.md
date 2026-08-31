# AgnoForge Composite Market Dataset Module — Structured Requirements

## 1. Overview

### 1.1 Working Name

**AgnoForge Composite Market Dataset Module**

Alternative service-level name:

**Market Dataset Service**

The exact final name remains open, but the architectural role is clear: this module sits between raw market-data acquisition and downstream consumers such as backtesting, charting, analytics, and live trading.

---

## 2. Purpose

The existing **Market Data Acquisition** module is responsible for faithfully acquiring and storing provider-specific market data.

The new **Composite Market Dataset** module is responsible for turning one or more source datasets into a consumer-ready dataset.

It should be able to:

- Select a source dataset as the base.
- Extend that dataset toward the present when required.
- Use another provider as a fallback or continuation source when explicitly configured.
- Incorporate live market data.
- Materialize higher timeframes.
- Preserve source provenance.
- Validate data quality.
- Expose a stable dataset abstraction to downstream systems.

The key architectural principle is:

> Acquisition preserves source truth. The Composite Dataset module assembles and derives consumer-ready market datasets without mutating the original provider datasets.

---

# 3. Bigger-Picture Architecture

The high-level market-data flow should become:

```text
External Market Providers
        |
        v
Market Data Acquisition
        |
        v
Provider-Specific Source Datasets
        |
        v
Composite Market Dataset Module
        |
        +--> Dataset Assembly
        +--> Catch-Up / Gap Completion
        +--> Live Continuation
        +--> Quality Validation
        +--> Higher-Timeframe Materialization
        |
        v
Consumer-Ready Market Dataset
        |
        +--> Backtesting Engine
        +--> Charting
        +--> Strategy Research
        +--> Analytics
        +--> Live Trading
```

Downstream consumers should not need to know:

- which exchange supplied each source segment,
- how missing ranges were downloaded,
- how live data was connected,
- how higher timeframes were generated,
- or how source datasets are physically stored.

They should consume a well-defined dataset abstraction.

---

# 4. Architectural Boundary

## 4.1 Market Data Acquisition

The acquisition module remains responsible for:

- Downloading provider-specific market data.
- Provider-specific pagination.
- Provider-specific rate limits.
- Provider-specific API behavior.
- Normalization into the canonical source representation.
- Source dataset storage.
- Source provenance.
- Source coverage.
- Source gap detection and repair.
- Querying provider-specific source data.

It should **not** become responsible for assembling datasets for specific downstream use cases.

---

## 4.2 Composite Market Dataset Module

The new module is responsible for:

- Creating derived datasets from one or more source datasets.
- Selecting and recording a base dataset.
- Extending the dataset to a requested end time.
- Requesting missing source data through acquisition capabilities.
- Combining source segments according to explicit policies.
- Attaching live continuation data.
- Materializing requested higher timeframes.
- Tracking dataset-level provenance.
- Validating dataset completeness and quality.
- Serving consumer-ready datasets.

---

# 5. Why This Module Exists

Backtesting and live trading have different requirements from raw acquisition.

The acquisition service may store canonical data such as:

```text
Binance / BTCUSDT / 1m
2022-01-01 -> 2026-08-30 13:00
```

A backtesting engine may need:

```text
BTC/USD
2025-01-01 -> 2026-08-30
1m
5m
15m
30m
1h
4h
1d
1w
1M
```

A live trading system may need:

```text
Historical context
        +
Catch-up data
        +
Current live data
```

Those responsibilities should not be pushed into the acquisition service itself.

---

# 6. Core Concept: Source Dataset

A **Source Dataset** represents market data from one specific provider.

Conceptually:

```text
provider
provider_symbol
logical_instrument
timeframe
range
```

Example:

```text
provider: Binance
symbol: BTCUSDT
logical instrument: BTC/USD
timeframe: 1m
range: 2022-01-01 -> 2026-08-30 13:00
```

Source datasets must remain immutable in meaning:

> A Binance source dataset means data reported by Binance.

Another provider's data must never silently overwrite or rewrite it.

---

# 7. Core Concept: Composite Dataset

A **Composite Dataset** is a derived dataset assembled for consumption.

It may contain:

```text
Base historical source
        +
Catch-up source
        +
Live source
        +
Derived higher timeframes
```

Example:

```text
Composite Dataset: BTC/USD Research Dataset

Base:
  Binance BTCUSDT 1m
  2022-01-01 -> 2026-08-30 13:00

Catch-up:
  Coinbase BTC-USD 1m
  2026-08-30 13:00 -> 2026-08-30 17:00

Live:
  Coinbase BTC-USD stream
  from 2026-08-30 17:00 onward

Materialized:
  5m
  15m
  30m
  1h
  4h
  1d
  1w
  1M
```

This dataset is **not** original exchange data.

It is a derived interpretation assembled according to a declared configuration.

---

# 8. Composite Dataset Configuration

Users or calling services should be able to create a composite dataset using an explicit configuration.

A conceptual configuration could include:

```text
name
logical_instrument
canonical_timeframe

base_source
requested_start
requested_end

catch_up_policy
catch_up_provider

live_policy
live_provider

materialized_timeframes

quality_policy
merge_policy
```

Example:

```yaml
name: btc-usd-research

instrument: BTC/USD

base:
  provider: binance
  symbol: BTCUSDT
  timeframe: 1m

range:
  start: 2024-01-01T00:00:00Z
  end: now

catch_up:
  provider: coinbase
  enabled: true

live:
  provider: coinbase
  enabled: true

materialize:
  - 5m
  - 15m
  - 30m
  - 1h
  - 4h
  - 1d
  - 1w
  - 1M

quality:
  strict: true
```

The exact schema remains an engineering decision.

---

# 9. Base Dataset Selection

The system must allow a user to choose an existing source dataset as the base.

Example:

```text
Base source:
Binance / BTCUSDT / 1m

Available source range:
2022-01-01 -> 2026-08-30 13:00
```

The Composite Dataset module should reuse already-acquired data rather than downloading the same historical range again unnecessarily.

---

# 10. Requested Composite Range

A composite dataset should have an explicit requested range.

Example:

```text
Requested:
2024-01-01 -> now
```

The module should compare the requested range against the selected base source coverage.

Conceptually:

```text
requested range
-
available base coverage
=
missing range
```

The missing portion may then be resolved according to the configured catch-up policy.

---

# 11. Catch-Up / Continuation Backfill

A major requirement is the ability to extend an existing source dataset toward the present.

Example:

```text
Base source available through:
13:00

Current requested end:
17:00

Missing:
13:00 -> 17:00
```

The Composite Dataset module should be able to request acquisition for the missing range.

Important:

> The Composite Dataset module should not implement exchange downloading itself.

Instead, it should request the Market Data Acquisition module to acquire the necessary source data.

---

# 12. Cross-Provider Catch-Up

The catch-up provider does not have to be the same provider as the base source.

Example:

```text
Base:
Binance
2024-01-01 -> 13:00

Catch-up:
Coinbase
13:00 -> 17:00
```

This is permitted only when explicitly configured.

The resulting dataset must remain clearly marked as composite.

The system must preserve the boundary between the two source segments.

---

# 13. Provenance

Every part of a composite dataset must remain traceable to its source.

At minimum, the system should be able to answer:

```text
Where did this bar come from?
```

Possible provenance metadata:

```text
source_provider
source_symbol
source_dataset_id
source_timeframe
source_open_time

composite_dataset_id
segment_id
derivation_type
materialization_version
```

For higher-timeframe bars, provenance may refer to the lower-timeframe source segment(s) from which they were derived.

The system must never silently discard provenance.

---

# 14. Dataset Segments

A composite dataset should conceptually be composed of ordered **segments**.

Example:

```text
Segment 1
  provider: Binance
  range: 2024-01-01 -> 2026-08-30 13:00

Segment 2
  provider: Coinbase
  range: 2026-08-30 13:00 -> 2026-08-30 17:00

Segment 3
  provider: Coinbase Live
  range: 2026-08-30 17:00 -> open-ended
```

Segments make provenance and transitions explicit.

A segment may be:

- Historical source.
- Catch-up source.
- Live continuation.
- Future synthetic or repaired source, if explicitly supported.

---

# 15. Merge Rules Must Be Explicit

The system must not blindly concatenate or merge data from multiple providers.

A composite configuration should define how conflicts are handled.

Possible policies include:

```text
primary_provider_wins
latest_segment_wins
reject_conflict
prefer_historical_source
prefer_live_source
manual_resolution
```

Future conflict rules may also consider:

- Timestamp equality.
- Price discrepancy tolerance.
- Volume differences.
- Provider priority.
- Known provider outages.

The initial implementation may support only a very small set of conservative policies.

---

# 16. Source Transition Validation

When changing providers between segments, the system should validate the transition.

Example checks:

- No timestamp overlap unless intentionally allowed.
- No unfilled timestamp hole.
- Matching logical instrument.
- Compatible market type.
- Compatible quote/base semantics.
- Same canonical timeframe.
- Acceptable price discontinuity, if that check is enabled.

A transition that cannot be safely resolved should be surfaced rather than silently accepted.

---

# 17. Canonical Timeframe

The preferred source resolution remains:

```text
1 minute
```

The Composite Dataset module should normally use 1-minute source data as the canonical lower-level dataset.

Higher timeframes should be derived from the canonical source where practical.

---

# 18. Higher-Timeframe Materialization

The Composite Dataset module should support materializing standard higher timeframes such as:

```text
5m
15m
30m
1h
4h
1d
1w
1M
```

Materialization should happen after the source dataset has been assembled and validated.

Conceptually:

```text
Composite 1m Dataset
        |
        v
Materializer
        |
        +--> 5m
        +--> 15m
        +--> 30m
        +--> 1h
        +--> 4h
        +--> 1d
        +--> 1w
        +--> 1M
```

---

# 19. Materialization Requirements

Higher-timeframe materialization must be:

- Deterministic.
- Rebuildable.
- Idempotent.
- Gap-aware.
- Provenance-aware.
- Versionable if aggregation rules evolve.

Derived data should never become an irreversible source of truth.

It should always be possible to rebuild it from lower-timeframe source data.

---

# 20. Calendar-Based Timeframes

Weekly and monthly materialization may require calendar-based boundaries rather than fixed-duration arithmetic.

For example:

```text
1w
1M
```

These should not be treated as simple fixed millisecond durations unless that behavior is explicitly desired.

The materialization layer may therefore require a richer timeframe abstraction than the acquisition module currently uses for fixed-duration provider bars.

---

# 21. Gap Awareness

The Composite Dataset module must be aware of gaps in the source data.

A composite dataset should not be considered complete merely because multiple sources were joined.

The module should detect:

- Missing source ranges.
- Missing expected bars.
- Provider-transition gaps.
- Gaps introduced by failed catch-up.
- Gaps in live continuation.
- Incomplete materialization windows.

---

# 22. Gap Completion

When a requested range is not complete, the Composite Dataset module should determine whether the missing data can be acquired.

Possible flow:

```text
Requested Composite Range
        |
        v
Check Available Source Coverage
        |
        +--> Complete
        |
        +--> Missing Range
                 |
                 v
          Request Acquisition
                 |
                 v
          Re-check Coverage
                 |
                 +--> Complete
                 +--> Still Incomplete
```

The acquisition system remains responsible for performing the actual backfill.

---

# 23. Live Continuation

The Composite Dataset module should eventually support extending a historical dataset using live market data.

Conceptually:

```text
Historical dataset
        |
        v
Catch-up to current time
        |
        v
Attach live stream
        |
        v
Continuous consumer-ready dataset
```

A downstream live trading system should not have to independently stitch together:

- historical storage,
- recent backfill,
- and real-time streaming.

That stitching belongs in the dataset layer.

---

# 24. Historical-to-Live Handover

The system must define an explicit handover point from historical data to live data.

Example:

```text
Historical data ends:
16:59

Live stream begins:
17:00
```

The handover should avoid:

- duplicate bars,
- skipped bars,
- overlapping final historical / first live bars,
- using a still-forming bar as a finalized historical bar.

The live continuation design may need to distinguish between:

- forming bars,
- closed bars,
- ticks/trades,
- and finalized OHLCV bars.

The exact live-data model remains open.

---

# 25. Backtesting Consumer

The backtesting engine should consume the Composite Dataset abstraction rather than raw provider-specific data directly.

It should be able to request something conceptually like:

```text
dataset:
  btc-usd-research

range:
  2024-01-01 -> 2025-01-01

required_timeframes:
  - 1m
  - 5m
  - 15m
  - 1h
  - 4h
  - 1d
```

The backtesting engine should not need to know:

- which source provider supplied each period,
- how a gap was filled,
- how a timeframe was materialized,
- or where the data is physically stored.

---

# 26. Live Trading Consumer

Live trading should use the same logical dataset model where practical.

The benefit is consistency:

```text
Historical research
        |
        v
Backtesting
        |
        v
Live trading
```

can use the same:

- logical instruments,
- candle definitions,
- timeframe semantics,
- data-quality concepts,
- and provenance model.

This reduces differences between historical and live environments.

---

# 27. Charting Consumer

Charting should also be able to use Composite Datasets.

A chart may request:

```text
BTC/USD
15m
2026-08-01 -> now
```

The dataset layer should determine whether those bars come from:

- stored historical materialization,
- newly materialized source data,
- catch-up data,
- live data,
- or a combination.

---

# 28. Dataset Quality

A Composite Dataset should expose quality metadata.

Potential metadata includes:

```text
requested_start
requested_end

available_start
available_end

coverage_percentage
expected_bar_count
actual_bar_count

gap_count
open_gap_count

source_count
provider_transition_count

last_historical_sync
live_status

materialized_timeframes
last_validation_time
```

---

# 29. Strict vs Research Usage

The existing idea of strict and research-oriented data usage remains useful.

## Strict Mode

A strict dataset request should require:

- Full requested coverage.
- No open gaps.
- Valid provider transitions.
- Complete required materializations.
- No unresolved source conflicts.

If any requirement fails, the dataset should not be declared ready.

---

## Research Mode

Research Mode may allow:

- Known gaps.
- Explicitly tolerated discontinuities.
- Unresolved but acknowledged quality warnings.

The resulting dataset must be marked accordingly.

Research Mode must never make an imperfect dataset appear equivalent to a strict dataset.

---

# 30. Dataset Lifecycle

A Composite Dataset may have a lifecycle such as:

```text
draft
resolving_sources
acquiring
assembling
validating
materializing
ready
degraded
failed
archived
```

A simpler initial implementation may use fewer states.

The important requirement is that dataset construction should be observable and queryable rather than existing only as an implicit operation.

---

# 31. Reproducibility

A Composite Dataset should be reproducible from its configuration and source datasets.

The system should persist enough information to reconstruct:

```text
Which base source was selected?
Which provider filled which range?
Which merge policy was used?
Which materialization rules were used?
Which dataset version was produced?
```

This is especially important for backtesting reproducibility.

---

# 32. Dataset Versioning

A future version should support dataset versions.

Example:

```text
btc-usd-research / v1
btc-usd-research / v2
```

A new version may be created when:

- source coverage changes,
- gaps are repaired,
- provider fallback is changed,
- materialization rules change,
- conflict resolution changes,
- or new data is frozen for a reproducible backtest.

This avoids silently changing historical research inputs.

---

# 33. Freeze / Snapshot Capability

For backtesting and research, it should eventually be possible to freeze a composite dataset.

Example:

```text
btc-usd-research
snapshot: 2026-08-30T17:00:00Z
```

A frozen snapshot provides stable inputs for reproducible backtest results.

Live trading datasets, by contrast, may remain continuously advancing.

---

# 34. Suggested Hexagonal Architecture

The new module should follow the same architectural principles as the acquisition module.

Conceptually:

```text
Inbound Adapters
        |
        +--> REST
        +--> CLI
        +--> Desktop UI
        +--> Future internal service calls
        |
        v
Composite Dataset Use Cases
        |
        v
Domain
        |
        +--> CompositeDataset
        +--> DatasetSegment
        +--> DatasetSource
        +--> DatasetRange
        +--> MaterializationPlan
        +--> QualityStatus
        +--> Provenance
        |
        v
Outbound Ports
        |
        +--> Source Dataset Reader
        +--> Market Data Acquisition
        +--> Composite Dataset Store
        +--> Live Market Data Source
        +--> Materializer
        +--> Observability
```

---

# 35. Suggested Application Use Cases

Potential use cases include:

```text
CreateCompositeDataset
GetCompositeDataset
ListCompositeDatasets
BuildCompositeDataset
RefreshCompositeDataset
ValidateCompositeDataset
MaterializeTimeframes
AttachLiveSource
DetachLiveSource
FreezeCompositeDataset
DeleteCompositeDataset
QueryCompositeBars
GetDatasetQuality
GetDatasetProvenance
```

The MVP does not need to implement all of them immediately.

---

# 36. Acquisition Integration Port

The Composite Dataset module should depend on the acquisition subsystem through an abstraction.

Conceptually:

```text
MarketDataAcquisitionPort
```

Possible capabilities:

```text
FindSourceDatasets(...)
GetCoverage(...)
EnsureRange(...)
QueryBars(...)
GetGaps(...)
IsComplete(...)
```

The important principle is:

> Composite Dataset asks for source-data capabilities. It does not call Binance, Coinbase, or another exchange directly.

---

# 37. Live Data Port

Live market data should also be behind an abstraction.

Conceptually:

```text
LiveMarketDataPort
```

Possible capabilities:

```text
Subscribe(...)
Unsubscribe(...)
CurrentStatus(...)
```

Provider-specific WebSocket or streaming behavior remains in adapters.

---

# 38. Storage

The Composite Dataset module may initially share the same physical DuckDB database as the acquisition module.

Logical separation is more important than immediately having separate databases.

Possible logical storage areas:

```text
source bars
source coverage
source gaps

composite dataset definitions
composite dataset segments
composite provenance
materialized bars
dataset quality
dataset versions
```

Physical separation can be revisited later.

---

# 39. Repository / Service Boundary

## Current Recommendation

Do **not** split into a separate repository yet.

Keep:

```text
one repository
```

with clearly separated modules / bounded contexts.

Conceptually:

```text
agnoforge/
  market-acquisition/
  market-datasets/
  backtesting/
  ...
```

or the equivalent package layout appropriate to the current codebase.

The exact directory structure should follow the existing repository conventions.

---

# 40. Why Not Separate Repositories Yet

Separating repositories now would introduce overhead around:

- version coordination,
- local development,
- shared domain contracts,
- integration testing,
- deployment,
- API compatibility,
- and release management.

The modules are still closely related and evolving together.

The correct first boundary is:

> Separate architectural responsibility, not necessarily separate deployment.

---

# 41. Future Service Split

The Composite Dataset module may later become an independently deployed service if there is a concrete reason.

Possible reasons include:

- Independent scaling.
- Long-running dataset construction.
- Large materialization workloads.
- Separate release lifecycle.
- Multi-process live-data handling.
- Remote consumers.
- Shared server deployment.
- Failure isolation requirements.

The architecture should make such a split possible without requiring it now.

---

# 42. Initial Deployment Recommendation

For the initial desktop-first product:

```text
Single application / process
        |
        +--> Market Acquisition module
        +--> Composite Dataset module
        +--> Backtesting module
        +--> HTTP / CLI / Desktop adapters
```

These should still communicate through application boundaries rather than reaching into each other's infrastructure details.

---

# 43. Internal Communication

If both modules live inside the same process initially, the Composite Dataset module may call an application-level interface exposed by the acquisition module directly.

Conceptually:

```text
Composite Dataset
        |
        v
Acquisition Application Interface
        |
        v
Acquisition Use Cases
```

If the system later becomes distributed, that same conceptual boundary could be implemented using:

```text
REST
gRPC
message broker
or another IPC mechanism
```

without changing the Composite Dataset domain model.

---

# 44. MVP Scope

A practical first version of the Composite Dataset module should support:

1. Create a composite dataset definition.
2. Select one existing 1-minute source dataset as the base.
3. Select a requested historical range.
4. Detect whether the requested range exceeds available base coverage.
5. Ask the acquisition module to fill the missing trailing range.
6. Initially use either:
   - the same provider, or
   - an explicitly configured fallback provider.
7. Assemble ordered source segments.
8. Validate that there are no unresolved gaps.
9. Materialize a limited set of higher timeframes.
10. Query the assembled dataset.
11. Expose provenance for returned data.
12. Make the result consumable by the backtesting engine.

Live streaming may be added immediately afterward if it increases scope too much for the first version.

---

# 45. MVP Simplifications

The first version may deliberately avoid:

- Arbitrary provider conflict resolution.
- Mixing many providers in the same time range.
- Automatic provider ranking.
- Price-quality scoring.
- Automatic cross-provider anomaly correction.
- Distributed processing.
- Separate repositories.
- Separate deployment.
- Persistent message brokers.
- Full live tick normalization.
- Automatic dataset version branching.

The architecture should allow these later without requiring them now.

---

# 46. High-Level Acceptance Criteria

The first version is successful when:

1. An existing source dataset can be selected as a base.
2. A requested composite range can be declared.
3. The system can determine which part of the requested range is already available.
4. Missing trailing history can be requested from the acquisition module.
5. The catch-up provider may differ from the base provider when explicitly configured.
6. Source datasets remain unchanged.
7. The composite dataset records where every segment came from.
8. Provider boundaries are visible in provenance.
9. A complete 1-minute composite timeline can be assembled.
10. Open source-data gaps prevent strict readiness.
11. Higher timeframes can be generated from the assembled 1-minute dataset.
12. Materialized data can be rebuilt.
13. The backtesting engine can consume the composite dataset without knowing provider details.
14. The module lives as a separate bounded context from acquisition.
15. Both modules can remain in the same repository and process initially.
16. The architecture does not prevent a future service split.

---

# 47. Example End-to-End Scenario

Assume the system already contains:

```text
Binance / BTCUSDT / 1m

Coverage:
2024-01-01 00:00
->
2026-08-30 13:00
```

The user creates:

```text
Composite:
BTC/USD Research Dataset

Requested:
2024-01-01 -> 2026-08-30 17:00

Base:
Binance / BTCUSDT / 1m

Catch-up:
Coinbase / BTC-USD / 1m
```

The system performs:

```text
1. Read Binance coverage.

2. Determine:
   Base covers 2024-01-01 -> 13:00.

3. Determine missing tail:
   13:00 -> 17:00.

4. Ask Market Data Acquisition:
   Ensure Coinbase BTC-USD 1m exists for 13:00 -> 17:00.

5. Wait for / observe acquisition completion.

6. Validate Coinbase source coverage.

7. Assemble:

   Segment A
   Binance
   2024-01-01 -> 13:00

   Segment B
   Coinbase
   13:00 -> 17:00

8. Validate:
   no gap
   no unintended overlap
   same logical instrument
   valid provider transition

9. Materialize:
   5m
   15m
   30m
   1h
   4h
   1d
   1w
   1M

10. Mark dataset ready.

11. Backtesting engine consumes:
    BTC/USD Research Dataset

12. Provenance remains available for every segment.
```

A later live-enabled version continues:

```text
13. Attach Coinbase live source at 17:00.

14. Closed live 1m bars extend the composite timeline.

15. Higher timeframes are incrementally updated.

16. Live trading and charting consume the advancing dataset.
```

---

# 48. Key Product Principles

## 48.1 Source Truth Is Immutable in Meaning

Provider-specific source datasets always represent what that provider reported.

---

## 48.2 Composite Means Derived

A multi-provider dataset must always be visibly identified as derived.

---

## 48.3 Provenance Is Mandatory

No source switch or merge may become invisible.

---

## 48.4 Acquisition and Assembly Are Separate

Downloading data and deciding how datasets are assembled are different responsibilities.

---

## 48.5 Materialization Happens Above Source Acquisition

Higher-timeframe aggregation belongs in the dataset layer, not the provider acquisition layer.

---

## 48.6 Consumers Depend on Datasets, Not Exchanges

Backtesting, charting, analytics, and live trading should consume the dataset abstraction.

---

## 48.7 Same Repository Is Fine

Architectural separation does not require repository separation.

---

## 48.8 Split Deployment Only When Needed

A future independent service should be driven by operational requirements, not by premature decomposition.

---

## 48.9 Dataset Construction Must Be Reproducible

A historical backtest should be able to explain exactly which market data was used.

---

## 48.10 Imperfect Data Must Remain Visible

Gaps, conflicts, fallbacks, and provider transitions should never be silently hidden.

---

# 49. Open Questions

The following items require later design decisions:

- Final module / service name.
- Exact CompositeDataset domain model.
- Exact source-segment model.
- Whether a composite dataset physically copies source bars or references source datasets.
- Whether materialized bars are persisted or computed lazily.
- Storage schema.
- Dataset versioning strategy.
- Snapshot / freeze semantics.
- Exact logical instrument model.
- Symbol mapping between providers.
- Quote-currency normalization.
- Cross-provider price discrepancy policy.
- Volume semantics across providers.
- Provider transition validation rules.
- Whether source overlap is allowed.
- Initial set of merge policies.
- Whether catch-up defaults to the base provider.
- Live market-data model.
- Tick vs trade vs OHLCV stream representation.
- Forming-candle behavior.
- Historical-to-live handover semantics.
- Incremental materialization behavior.
- Weekly candle boundary.
- Monthly candle boundary.
- Materialization timezone.
- Strict vs Research Mode API.
- Quality scoring.
- Dataset readiness states.
- Whether dataset build jobs need persistence.
- Whether acquisition completion is observed synchronously, through events, or by polling.
- Whether module-to-module communication should use direct application calls initially.
- Conditions that would justify a separate service or repository.

---

# 50. Recommended Initial Direction

The current preferred direction is:

```text
ONE REPOSITORY
      |
      +--------------------------------------------------+
      |                                                  |
      v                                                  v
Market Data Acquisition                         Composite Market Dataset
      |                                                  |
      | provider-specific source truth                   | derived consumer dataset
      |                                                  |
      +--> Binance source                                +--> choose base
      +--> Coinbase source                               +--> ensure requested coverage
      +--> source gaps                                   +--> assemble segments
      +--> source repair                                 +--> preserve provenance
      +--> source coverage                               +--> validate quality
                                                         +--> materialize timeframes
                                                         +--> attach live continuation
                                                                  |
                                                                  v
                                                      Consumer-Ready Dataset
                                                                  |
                                        +-------------------------+----------------------+
                                        |                         |                      |
                                        v                         v                      v
                                  Backtesting                 Charting              Live Trading
```

The central design principle is:

> Keep acquisition focused on faithfully obtaining provider-specific source data. Build a separate dataset layer that assembles, extends, validates, materializes, and serves market data in the form downstream trading systems actually need.
