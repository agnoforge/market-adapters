# 07 — Query the assembled dataset

**What to build:** A backtesting engine (or chart, or curious human) pulls bars from a ready Composite Dataset by name, timeframe, and range — as JSON or streamed Parquet — knowing nothing about providers, gaps, or storage. This is the consumer contract the whole module exists for.

**Blocked by:** 06 — Higher-timeframe materialization.

**Status:** ready-for-agent

- [ ] A bars query on a composite dataset accepts a Timeframe (1m or any configured materialized frame) and a half-open range, defaulting to the dataset's resolved range when omitted
- [ ] 1m bars are served by reading the referenced source bars across the dataset's Segments in order (SQL over the shared file, read-only); higher frames are served from materialized bars — one endpoint, same wire shapes as the acquisition bars route (decimal strings, Parquet streaming as the default heavy-payload path)
- [ ] Requesting a timeframe the dataset does not materialize, an unknown dataset, or a dataset that has never been built fails with a clear, mapped error
- [ ] Queries against a `stale` or research-mode dataset succeed but the dataset's state/mode is discoverable (via get/quality) — nothing pretends staleness away
- [ ] A dedicated quality endpoint (and CLI subcommand) returns the persisted Quality metadata; CLI gains a query subcommand mirroring the existing data query ergonomics
- [ ] Harness test proves the backtester contract end to end: create → build → query a year of a higher frame as Parquet and the same range as JSON, byte-identical decimals to the store tests' expectations; `go test -race ./...` green
