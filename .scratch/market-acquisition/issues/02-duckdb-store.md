Status: todo

# DuckDB store adapter

Depends on: 01. Schema: bars (PK provider,symbol,timeframe,open_time; DECIMAL(20,8)), coverage, gaps. Implement Store port incl. INSERT OR REPLACE, coverage union-merge, ReplaceOpenGaps, ExportParquet via COPY TO. Tests against in-memory DuckDB.
