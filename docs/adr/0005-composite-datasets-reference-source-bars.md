---
status: accepted
---
# Composite datasets reference source bars; only a future freeze copies them

A composite dataset's 1-minute timeline is not physically copied: segments are metadata (provider, symbol, range, kind), and bars are read from acquisition's source tables at query time. This makes "source datasets remain unchanged" free, avoids duplicating the largest data in the system, and keeps assembly to metadata writes. Materialized higher-timeframe bars *are* persisted (rebuildable, stamped with a materialization version), because recomputing aggregation on every query would tax every consumer.

The acquisition port is control plane only (coverage, completeness, backfill, gaps). For the data plane, the composite store adapter reads acquisition's `bars` table read-only in SQL — the shared DuckDB file is the accepted data plane, and staging bars through Go values just to aggregate them back in SQL would copy the largest data in the system for ceremony. A future service split replaces that one adapter read with a Parquet/API fetch; the domain model doesn't change.

## Consequences

- A later repair or re-download of source data changes composite query results without any composite-side action. This is accepted for v1; reproducibility-of-inputs is the job of the deferred freeze/snapshot capability, which will copy bars into a stable artifact. Each build records its resolved end and quality metadata, so what was built remains explainable (reproducibility-of-explanation).
