# 08 — Observability and onboarding

**What to build:** A Build is as diagnosable as any acquisition operation: traced end to end with the repo's OpenTelemetry conventions, visible in the playground, and documented for the next human. Closes the loop on "dataset construction should be observable and queryable."

**Blocked by:** 07 — Query the assembled dataset.

**Status:** ready-for-agent

- [ ] The composite context follows the ADR-0003 conventions: vendor-neutral OTel API only, its own tracer scope, its own layer attribute value, span names matching the existing naming style, errors marked on spans
- [ ] A Build's trace shows its phases (resolve, ensure coverage, assemble, validate, materialize, quality) as child spans, with dataset name and range attributes; failures point at the failing phase
- [ ] Composite operations appear in the playground's operation catalog and their traces render in the existing trace UI; responses carry the trace id header like every other route
- [ ] README documents the composite REST surface and CLI subcommands alongside the acquisition ones; the status file is updated
- [ ] A harness or playground-level test proves a build produces a recorded trace with the expected span hierarchy; `go test -race ./...` green
