# 03 — Build a single-segment dataset

**What to build:** A researcher triggers a Build on a dataset whose base source already covers the requested range, and the dataset becomes `ready`: end resolved, one base Segment assembled, gaps judged per mode, Quality computed, provenance visible. This is the tracer bullet through the whole Build path — port, assembly, validation, readiness, persistence, API.

**Blocked by:** 01 — Composite dataset definitions.

**Status:** ready-for-agent

- [ ] A consumer-defined AcquisitionPort exists in the composite app layer carrying control-plane capabilities only (coverage, completeness, backfill start/wait, gap detection/repair) — no bar reading; the production adapter wraps the acquisition application service in-process
- [ ] Build resolves `now` to the last fully closed 1m bar at build time and persists the Resolved End; fixed ends pass through; completeness and quality are judged against the resolved end
- [ ] Build assembles one base Segment (kind `base`) covering what the base source supplies within the requested range; segments are recomputed and replaced wholesale on every successful build
- [ ] Strict mode: any open Gap intersecting the resolved range prevents `ready` (build ends `failed` with the gaps reported); research mode: the dataset becomes `ready` with gaps listed in Quality and the dataset visibly flagged as research
- [ ] Lifecycle transitions `draft|stale|ready → building → ready|failed` are persisted on the dataset row; a failed build preserves its error; a second concurrent Build of the same dataset is rejected with a clear error
- [ ] Quality is computed and persisted: requested and resolved range, available range, expected/actual bar counts, coverage percentage, open gap count and list, mode, last build time
- [ ] Get returns the ordered Segment list and Quality — the provenance API; the build endpoint and CLI subcommand follow the existing adapter conventions
- [ ] HTTP harness test (fake AcquisitionPort, seeded source bars in real temp DuckDB) proves create → build → get end to end in both modes; `go test -race ./...` green
