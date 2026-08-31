# 05 — Cross-provider catch-up and Transitions

**What to build:** A dataset configured with a different catch-up provider continues its timeline past the point where the base provider's data ends: the base provider is extended as far as it can go, the catch-up provider fills only the true remainder, and the provider boundary is a validated, visible Transition. Proven with a fake second provider — no new exchange adapter.

**Blocked by:** 04 — Catch-up: extend the base provider.

**Status:** ready-for-agent

- [ ] With a catch-up provider configured, Build first extends base coverage toward the resolved end; only the tail the base provider genuinely cannot supply is requested from the catch-up provider (no chains beyond those two)
- [ ] Assembly produces ordered Segments (`base` then `catch_up`) that abut exactly on the half-open boundary; the segment list records provider, symbol, and range for each
- [ ] Transition validation enforces: no timestamp hole, no overlap (overlapping source data for the same range fails the build — `reject_conflict`), same declared Instrument, same canonical timeframe; a failed validation surfaces the reason, never silently accepts
- [ ] The close→open price delta across each Transition is computed and recorded in Quality (count of transitions plus per-transition delta); no threshold is enforced
- [ ] Without a configured catch-up provider, behaviour is exactly ticket 04's (base provider only)
- [ ] Harness tests with a fake second provider prove: the requirements doc's end-to-end scenario shape (base ends mid-day, catch-up completes the range), overlap rejection, hole rejection, and delta recording; `go test -race ./...` green
