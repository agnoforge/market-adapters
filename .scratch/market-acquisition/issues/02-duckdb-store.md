Status: todo

# DuckDB store adapter

Depends on: 01. Schema: bars (PK provider,symbol,timeframe,open_time; DECIMAL(20,8)), coverage, gaps. Implement Store port incl. INSERT OR REPLACE, coverage union-merge, ReplaceOpenGaps, ExportParquet via COPY TO. Tests against in-memory DuckDB.

## Done when
- [ ] Schema created on open: `bars` PK (provider,symbol,timeframe,open_time) with DECIMAL(20,8) prices/volumes, `coverage`, `gaps`
- [ ] Upserting the same bars twice leaves row count unchanged; a changed value overwrites
- [ ] `ExtendCoverage` merges touching/overlapping ranges into one row
- [ ] `ReplaceOpenGaps` deletes only `open` gaps intersecting the range and leaves `ignored`/`unrecoverable` intact
- [ ] `ExportParquet` output is readable back by DuckDB with the same row count
- [ ] All tests run against in-memory DuckDB; no file left on disk
