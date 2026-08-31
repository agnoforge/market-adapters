# 04 — Catch-up: extend the base provider

**What to build:** A Build whose requested range exceeds what the base source currently covers asks acquisition to fill the difference from the base provider — head and tail — and the dataset becomes `ready` once coverage lands. The researcher never talks to acquisition; the dataset layer orchestrates it.

**Blocked by:** 03 — Build a single-segment dataset.

**Status:** ready-for-agent

- [ ] Build compares the resolved requested range against base coverage and requests acquisition backfills for the missing parts via the port, waiting for completion before assembly
- [ ] The concurrent-backfill rejection from acquisition (a backfill already running for the same source Dataset) is handled by waiting/retrying, not surfaced as a build failure
- [ ] Gaps detected inside the ensured range are repaired through the port before readiness is judged; gaps that remain open after repair follow ticket 03's strict/research rules
- [ ] Missing head: the base provider is backfilled toward the requested start; when the provider's earliest-available floor limits it — strict: not ready, with the shortfall reported; research: the dataset starts where data starts, with available start recorded in Quality
- [ ] A backfill that fails terminally fails the build with the acquisition error preserved
- [ ] Harness tests drive the fake port through: coverage already complete (no backfill requested), tail fill, head fill, head shortfall in both modes, backfill failure, and the concurrent-backfill race; `go test -race ./...` green
