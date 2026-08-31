# Context Map

## Contexts

- [Market Data Acquisition](./CONTEXT.md) — acquires and stores provider-specific OHLCV bars exactly as reported; source truth
- [Composite Market Dataset](./internal/composite/CONTEXT.md) — assembles source Datasets into derived, consumer-ready datasets with materialized higher timeframes

## Relationships

- **Composite → Acquisition**: Composite consumes acquisition through a consumer-defined port (coverage, completeness, backfill, gaps, bars). It never calls providers directly and never mutates source Datasets.
- **Shared terms**: `Bar`, `Range`, `Provider`, `Symbol`, `Gap`, `Coverage`, `Complete` are owned by Acquisition; Composite uses them with Acquisition's meaning.
- **Divergent term**: `Timeframe` — fixed-duration only in Acquisition; calendar-capable (`1w`, `1M`) in Composite.
- **Owned by Composite**: `Instrument`, `Composite Dataset`, `Segment`, `Build`, `Materialization` (reserved in Acquisition's glossary, defined only here).
