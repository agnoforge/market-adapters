# 01 — Composite dataset definitions: create, get, list, edit, delete

**What to build:** A researcher can declare a named Composite Dataset (instrument, base source, requested range with fixed end or `now`, optional catch-up provider, materialized timeframes, strict/research mode), read it back, list all datasets, edit its config, and delete it — over REST and CLI, persisted in the shared DuckDB file. No building yet: datasets stay in `draft`.

**Blocked by:** None — can start immediately.

**Status:** done

- [x] Create accepts a config naming an Instrument, one base source (provider, symbol, 1m), a requested range (`end` may be the literal `now`), optional catch-up provider, a set of materialized timeframes, and mode; rejects invalid slugs, unknown timeframes, an end not after the start, and sources whose declared instrument differs from the dataset's
- [x] `name` is the unique identity (kebab-case slug); creating a duplicate name fails with a clear error
- [x] Get returns the full config plus lifecycle state; list returns all datasets; delete removes the definition (and later its derived data) but can never touch source data
- [x] Config is editable; editing a dataset that has been built marks it `stale` (editing a `draft` leaves it `draft`)
- [x] The composite bounded context owns its own DDL, applied idempotently on startup against the same DuckDB file acquisition uses
- [x] REST routes live under a composites resource following the existing API's conventions (JSON wire shapes, error mapping, method+path mux patterns); CLI subcommands are thin HTTP clients like the existing data commands
- [x] New code lives in the composite bounded context per CONTEXT-MAP; vocabulary follows the composite glossary
- [x] Covered by an HTTP harness test (real handlers, real app service, real temp DuckDB) per the seam plan; `go test -race ./...` green with no network
