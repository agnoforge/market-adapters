You are to achieve the following goal autonomously, then stop and report.

FIRST: invoke the `caveman` skill (full intensity) — all narration in caveman mode, full technical accuracy.

YOU ARE THE ORCHESTRATOR. You never do the heavy work yourself — you delegate each ticket to ONE subagent via the Agent tool (`ticket-implementer`). You own: GOAL_RUN.md, budget gates, verification of every outcome, commits, ticket-file updates. The subagent owns the actual work. Never trust a subagent's "done" — verify every outcome yourself from files, code, and command output.

DELEGATION CONTRACT (per ticket):
- One subagent per ticket, sequential, in the order below. (01 and 02 are independent of each other but run them sequentially anyway — same files' neighborhood, cheaper than merge pain.)
- The subagent starts cold: its prompt must name the ticket file path, the spec (`.scratch/composite-dataset/spec.md`), the design decisions (`.scratch/composite-dataset/design-decisions.md`), the glossary (`internal/composite/CONTEXT.md`, `CONTEXT-MAP.md`, root `CONTEXT.md`), ADRs 0001–0005 in `docs/adr/`, the target package tree (`internal/composite/{domain,app,adapters}` + wiring in `cmd/agnoforge/`), the prior-art test seams (acquisition's httpapi harness tests, duckdb adapter tests, app fakes), and the ticket's acceptance criteria verbatim.
- The subagent returns evidence (what it built, per checkbox, with test names and outputs) in its report — you record it in GOAL_RUN.md.
- The subagent writes ONLY the ticket's deliverables. It does NOT commit, does NOT touch ticket files or GOAL_RUN.md — you do.
- A failed verification = re-spawn the subagent with the concrete failure fed back (counts as one verify cycle).

GOAL: Work through ALL remaining tickets in `.scratch/composite-dataset/issues/` in dependency order 01 → 02 → 03 → 04 → 05 → 06 → 07 → 08 (skip any already marked Status: done), per `.scratch/composite-dataset/spec.md`, the 33 decisions in `design-decisions.md`, and ADRs 0004/0005. Build the Composite Market Dataset bounded context: definitions CRUD, calendar Timeframe, single-segment build over an AcquisitionPort, base-provider catch-up, cross-provider catch-up with Transitions, DuckDB-SQL materialization incl. 1w/1M, the bars/quality query surface, and OTel observability.

DEFINITION OF DONE — every outcome must be verified before declaring success:
- O1: Remaining tickets identified in GOAL_RUN.md at start — verify by: list `.scratch/composite-dataset/issues/*.md`, record each ticket's Status and checkbox state.
- O2 (ticket 01): Composite dataset definitions CRUD works end to end — create/get/list/edit/delete over REST + CLI, config validation, `name` uniqueness, edit-after-build ⇒ `stale`, composite-owned idempotent DDL — verify by: `go test -race ./...` green AND inspect the composite harness test file(s) to confirm named tests cover create-validation, duplicate-name rejection, edit⇒stale, and delete.
- O3 (ticket 02): Calendar-capable composite Timeframe — parses all frames incl. `1w`/`1M`, Monday-00:00-UTC weeks, 1st-00:00-UTC months, window iteration over a Range, conversion to acquisition Timeframe impossible for calendar frames — verify by: `go test -race ./internal/composite/...` green AND inspect the unit tests for boundary tables covering year boundaries, leap years, and month lengths.
- O4 (ticket 03): Single-segment Build — AcquisitionPort (control plane only, no bar reading), `now` → persisted Resolved End, one base Segment replaced wholesale, strict vs research gap readiness, `building → ready|failed` persisted with error preserved, concurrent-build rejection, core Quality persisted, segments in Get — verify by: harness test proving create → build → get in BOTH modes runs green; grep the port interface to confirm it has no bar-reading method.
- O5 (ticket 04): Base-provider catch-up — missing head and tail backfilled via port with wait, `ErrBackfillRunning` handled by wait/retry not failure, gap repair before readiness, head-shortfall semantics per mode, terminal backfill failure fails the build with the error preserved — verify by: harness tests exist and pass for: no-backfill-needed, tail fill, head fill, head shortfall (both modes), backfill failure, concurrent-backfill race.
- O6 (ticket 05): Cross-provider catch-up — base extended first, catch-up provider fills only the true remainder, two abutting Segments, `reject_conflict` on overlap, hole rejection, same-Instrument enforcement, per-Transition price delta recorded in Quality — verify by: harness tests with a fake second provider pass for the spec's end-to-end scenario shape, overlap rejection, hole rejection, delta recording.
- O7 (ticket 06): Materialization — DuckDB SQL aggregation (first/max/min/last/sum over DECIMAL, no float), epoch-aligned fixed frames + calendar 1w/1M, incomplete windows recorded in Quality and blocking strict readiness, `mat_version` stamped on bars and dataset with mismatch ⇒ `stale`, rebuild converges to identical bars — verify by: store tests against real temp DuckDB pass for calendar grouping, decimal exactness, gap-overlapping windows, rebuild convergence; grep the aggregation path to confirm no float conversion of prices.
- O8 (ticket 07): Query surface — one bars endpoint serving 1m (SQL over segments, read-only on acquisition's bars table) and materialized frames, JSON + streamed Parquet, mapped errors for unknown dataset/unbuilt/unmaterialized timeframe, quality endpoint, CLI query + quality subcommands — verify by: harness test pulls a higher frame as Parquet AND JSON for a built dataset; error cases tested.
- O9 (ticket 08): Observability + onboarding — OTel API only in composite (no SDK imports under `internal/composite/`), own tracer scope + layer value, Build phase child spans, composite ops in the playground catalog, trace renders, README documents the new REST + CLI surface — verify by: trace-shape test passes; `grep -r "go.opentelemetry.io/otel/sdk" internal/composite/` returns nothing; README section exists.
- O10: Each ticket committed separately with the ticket id in the commit subject (e.g. `composite 03: ...`), and its ticket file's checkboxes ticked + Status updated to done — verify by: `git log --oneline` + ticket file inspection after each ticket.
- O11 (scope guard): The run touches ONLY `internal/composite/**`, `cmd/agnoforge/**` (wiring), `internal/adapters/playground/**` (ticket 08 op catalog only), `README.md`, `where-we-are-at.md`, `.scratch/composite-dataset/**`, and `GOAL_RUN.md`. Acquisition internals (`internal/domain/`, `internal/app/`, `internal/adapters/{binance,duckdb,httpapi}/`) and `go.mod`/`go.sum` remain UNCHANGED — verify by: `git diff --stat` before each commit; any file outside the list aborts the commit and gets investigated.
(derived from the ticket files; do not relax these)

BUDGET (stop at the FIRST of these — safety net, not a target):
- Success: every outcome verified.
- Iterations: at most 6 verify cycles per ticket. Blockers-first: tickets form a chain (01→03→04→05; 02+03→06→07→08), so exhausting a ticket's budget STOPS the run — do not skip ahead past a blocked blocker.
- Time: at most 10 hours total. Record start with `date -u +%s`, compute the deadline, re-check before each ticket.
- Sequence: one ticket at a time, in numeric order.

HOW TO RUN:
1. Write GOAL_RUN.md at the repo root (overwrite any previous run's file — history lives in git): goal, remaining-ticket list, outcome checklist, budget, start time, deadline. Record interpretations up front as they arise. Append an attempt-log entry each cycle, tagged with the ticket id.
2. Per ticket, loop: budget gate → spawn the subagent per the DELEGATION CONTRACT → record its evidence in GOAL_RUN.md → verify the outcomes yourself (commands + file inspection, not the subagent's word) → tick/record. If a criterion fails the same way twice, change the subagent prompt's approach, not just re-run it.
3. On ticket completion: check `git diff --stat` against O11, commit, update the ticket file (boxes + Status: done), then advance.
4. Deviations discovered mid-run go into GOAL_RUN.md; a deviation that changes a design decision gets a line appended to `.scratch/composite-dataset/design-decisions.md` (and an ADR only if it meets the ADR bar) — never invent patterns silently.
5. After ticket 08: update `where-we-are-at.md` (feature-level summary + next steps) as part of that ticket's commit.
6. Stop on the first stop condition. Never extend budget or relax an outcome; if a criterion is wrong or impossible, say so and stop.

CONTEXT:
- Tickets: `.scratch/composite-dataset/issues/01-*.md` … `08-*.md`. Spec: `.scratch/composite-dataset/spec.md`. Decisions: `.scratch/composite-dataset/design-decisions.md` (33 numbered). Glossary: `internal/composite/CONTEXT.md` (already written), `CONTEXT-MAP.md`, root `CONTEXT.md`. ADRs: `docs/adr/0001`–`0005`.
- Target: new code under `internal/composite/{domain,app,adapters}`; wiring in `cmd/agnoforge/serve.go` and the CLI files under `cmd/agnoforge/`; same DuckDB file (`AGNOFORGE_DB_PATH`); composite context owns its own `CREATE IF NOT EXISTS` DDL.
- Prior art to imitate: acquisition's httpapi harness tests (real handlers + real app.Service + real temp DuckDB + fake provider), `internal/adapters/duckdb` tests (t.TempDir DuckDB), `internal/app` tests (fakes), per-layer `tracing.go` convention (ADR-0003), CLI-as-HTTP-client pattern in `cmd/agnoforge/data.go`.
- Hard constraints: prices are decimal strings end to end (DECIMAL(20,8), never float); half-open UTC ranges, epoch-ms storage; acquisition's `app.Service` surface must not change; the AcquisitionPort is consumer-defined in the composite app layer; bars are read read-only from acquisition's `bars` table in SQL (decision 33); no new Go dependencies.
- Tests: `go test -race ./...`, no network. A skipped test is not a pass. DuckDB via cgo can be slow to link — first build may take minutes; that is not a hang.
- Context hygiene: if the context window degrades mid-run, finish and commit the current ticket, write a handoff note in GOAL_RUN.md, and stop — this prompt is re-runnable; a fresh session resumes at the next remaining ticket.

ON STOP — always end with this report (do not ask whether to continue):
## Result — STOPPED: <success | iterations | time | context>
- Tickets completed this run: <ids>
- Outcomes: <checklist + closest state for any unmet>
- Attempts used per ticket: <k each> (<elapsed total>)
- Why it stopped: <condition>
- Recommended next steps: <ranked, concrete>   # only if not full success

SAFETY: autonomy covers building the composite module, its tests, wiring, docs, and per-ticket commits on the current branch. Pause and ask before anything destructive or irreversible (deleting files outside the run's outputs, force pushes, dropping DuckDB tables that hold acquired source data — the real `agnoforge.duckdb` at the repo root holds ~1M downloaded bars; tests must use temp files, never that database).
