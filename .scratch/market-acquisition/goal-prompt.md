# Goal prompt — build the Market Data Acquisition service, Phase 1

Paste the fenced block below into a fresh session at the repo root of
`market-acquisition`. It is self-contained: the executor loads its own skills,
delegates each ticket to the `ticket-implementer` sub-agent, verifies every
checkbox itself, and stops on its own.

````markdown
You are to achieve the following goal autonomously, then stop and report.

SKILLS — activate both before doing anything else:
1. Run `/caveman` (full intensity). Stay in it for every response until the run ends.
2. Run `/implement`. Its rules govern all coding: TDD at pre-agreed seams,
   `go vet` often, single test packages often, full suite once at the end,
   `/code-review` when done, commit to the current branch.

GOAL: Build Phase 1 of the Market Data Acquisition service — all eight tickets
in `.scratch/market-acquisition/issues/`, in dependency order, faithful to
`.scratch/market-acquisition/spec.md`.

DELEGATION — how each ticket is built:
- Every ticket's implementation is executed by the `ticket-implementer`
  sub-agent (`.claude/agents/ticket-implementer.md`). Spawn it via the Agent
  tool with `subagent_type: "ticket-implementer"`, one agent per ticket,
  sequentially — never two at once, the tickets share files.
- The sub-agent's prompt must be self-contained: the ticket file path, the spec
  path plus the exact sections the ticket cites (Ports, Rules, HTTP API, CLI),
  the glossary terms, the hard constraints from CONTEXT below, and the
  "deliberately NOT built" list. Sub-agents start with no conversation context.
- The sub-agent implements, tests, and self-verifies but does NOT commit.
  You (the orchestrator) then independently re-verify EVERY checkbox under the
  ticket's `## Done when` by running the checks yourself — the sub-agent's PASS
  report is a claim, not evidence. Only after your own verification passes do
  you commit.
- If the sub-agent's result fails your verification, send it back once via
  SendMessage to the same agent with the exact failing checkbox and observed
  output. If it fails again, take over that ticket inline yourself — that counts
  as the "change approach" move in the budget.

DEFINITION OF DONE — the eight outcomes are the tickets. A ticket is done only
when EVERY checkbox in its `## Done when` passes. Do not relax a checkbox. Work
strictly in this order: 01 → 02 → 03 → 04 → 05 → 06 → 07 → 08.

- O1: `01-domain-and-skeleton.md` — module, hexagonal layout, domain types.
  Verify by: `go build ./... && go vet ./...` clean; Timeframe/Range/Bar/Calendar
  tests green; `internal/domain` imports only stdlib.
- O2: `02-duckdb-store.md` — Store port over DuckDB.
  Verify by: schema + PK; double upsert leaves count unchanged; coverage
  union-merge; ReplaceOpenGaps preserves ignored/unrecoverable; Parquet
  round-trips through DuckDB; in-memory only.
- O3: `03-binance-adapter.md` — Provider port over `/api/v3/klines`.
  Verify by: the 13 timeframes and nothing else; 1000-per-page paging with no
  duplicate/missing bar across a 2500-bar fixture; in-progress candle never
  yielded; 429/Retry-After retry, 5-strike failure, unknown symbol permanent;
  shared token bucket with fake clock; invalid bars dropped; httptest only.
- O4: `04-backfill-usecase.md` — async Backfill.
  Verify by: id + effective range; 409-equivalent on running Dataset; per-page
  persist + Coverage; cancel keeps landed pages; failure keeps Coverage honest;
  DetectGaps on every terminal state; rerun is a no-op; app imports no adapter.
- O5: `05-gaps-and-complete.md` — expected − present.
  Verify by: one Gap per contiguous run; nothing outside Coverage; fake
  weekend calendar yields no Gap; ignored/unrecoverable excluded; auto
  `repaired`; IsComplete semantics exact.
- O6: `06-repair-and-gap-status.md` — Repair + status changes.
  Verify by: Repair = Backfill over the Gap range; status transitions as listed;
  ignoring the only Gap makes the range Complete.
- O7: `07-http-api.md` — REST adapter.
  Verify by: every spec route tested success + failure; Parquet default with
  `X-Complete`/`X-Gaps`; JSON on request; stdlib mux only; no adapter imports.
- O8: `08-cli.md` — `agnoforge serve` + `agnoforge data …`.
  Verify by: each subcommand hits its route; `query -o`; exit codes; stdlib
  `flag`; the end-to-end backfill→complete test against a fake Binance passes.

AFTER EVERY TICKET — all four must hold before you commit:
- `go build ./...`, `go vet ./...`, `go test ./...` all clean, output recorded.
- `go.mod` contains only `github.com/duckdb/duckdb-go` (plus its transitive
  deps). No router, CLI framework, ORM, logging, or retry library. If you reach
  for one, you have taken the wrong approach.
- Dependency direction holds: `domain` imports nothing; `app` imports
  `domain` + ports only; adapters import `app`/`domain`; only `cmd/` names
  concrete adapters. Check with `go list -deps` per package.
- Tests never touch the network or write outside a temp dir / in-memory DuckDB.

BUDGET (stop at the FIRST of these — safety net, not a target):
- Success: O1–O8 all verified.
- Time: at most 6 hours. Record start with `date -u +%s`, compute the deadline,
  re-check before each ticket and each verify cycle.
- Per-ticket: one sub-agent attempt plus one correction round; then inline
  takeover. If your inline attempt fails the same way twice, change approach.
- Context hygiene: if the context window degrades mid-run, finish and commit
  the current ticket, write a handoff note in `GOAL_RUN.md`, and stop. This
  prompt is re-runnable; a fresh session resumes at the next unstarted ticket.

HOW TO RUN:
0. Before delegating ticket 01, run `go version` (need 1.23+ for `iter`; 1.25
   is installed) and confirm cgo works: `CGO_ENABLED=1 go env CGO_ENABLED`.
   If DuckDB fails to link, that is a decision to report (ADR-0002 accepted
   cgo deliberately), not something to work around by swapping the store.
1. Read `.scratch/market-acquisition/spec.md` in full, `CONTEXT.md` for the
   ubiquitous language, and `docs/adr/0001-*`, `docs/adr/0002-*` for decisions
   already taken. The sub-agents read them too, but you must know them to verify.
2. Write `GOAL_RUN.md` at the repo root: the goal, O1–O8 as a checklist, the
   after-every-ticket invariants, budget, start time, deadline. Append an
   attempt-log entry per cycle, including each sub-agent's per-checkbox report
   verbatim.
3. Per ticket: budget gate → spawn `ticket-implementer` with the self-contained
   prompt → receive its report → re-verify every checkbox yourself → tick what
   passes, record what failed and why → update the ticket file (boxes +
   `Status: done`) → commit with the ticket number in the subject → next ticket.
   Never start ticket N+1 with ticket N's checkboxes outstanding.
4. Keep a running `ASSUMPTIONS.md` merging your assumptions and any the
   sub-agents report, for anywhere the spec is silent (e.g. exact JSON field
   names, Parquet column types). Do not let a sub-agent decide silently.
5. Deviations discovered mid-run go into a new ADR — never invent patterns
   silently. New domain vocabulary goes into `CONTEXT.md` as it crystallises,
   glossary-only, no implementation detail.
6. Full test suite once at the end, then `/code-review`, then update
   `where-we-are-at.md` (project rule: what was done, what could happen next).
7. Stop on the first stop condition. Never extend the budget silently and never
   relax an outcome to force a pass; if an outcome is wrong or impossible, say so.

CONTEXT:
- Repo root: this directory. Greenfield — no Go code exists yet; ticket 01
  creates the module (`go mod init`, module path your choice, record it in
  ASSUMPTIONS.md) and the layout `cmd/agnoforge`, `internal/domain`,
  `internal/app`, `internal/adapters/{binance,duckdb,httpapi}`.
- Spec: `.scratch/market-acquisition/spec.md` is the authority. Its Ports
  section is the seam contract: `Provider` (Name, SupportedTimeframes,
  EarliestAvailable, Calendar, Bars iterator) and `Store`. Its Rules section is
  law: half-open ranges on UTC-ms open_time, end clipped to last closed bar,
  start clipped to EarliestAvailable, invalid bars dropped, DECIMAL(20,8), one
  running Backfill per Dataset, states running/completed/failed/cancelled,
  DetectGaps on every terminal state, Complete = ⊆ Coverage ∧ no open Gap,
  queries flag incompleteness but never refuse.
- Tickets: `.scratch/market-acquisition/issues/01`–`08`, one per outcome,
  `Depends on:` lines are the order.
- ADRs: `docs/adr/0001-no-scheduler-abstraction.md` — no scheduler/broker/job
  table; Backfill registry is in-memory with a `ponytail:` comment naming the
  upgrade path. `docs/adr/0002-duckdb-embedded-store.md` — DuckDB via cgo, one
  file, three tables, no partitioning. Do not re-litigate either.
- Glossary: `CONTEXT.md` — Provider, Symbol, Timeframe, Dataset, Bar, Backfill,
  Coverage, Gap, Repair, Complete, Trading Calendar. `Instrument` is reserved
  and must not appear in code. Use these words in code, tests and commits;
  avoid the `_Avoid_` terms (no "candle", "kline" outside the Binance adapter's
  wire types, "interval", "ingestion job", "series").
- Hard constraints (pass these to every sub-agent): provider-specific
  behaviour (paging, weights, symbol spelling, Retry-After) lives only in
  `internal/adapters/binance`; `internal/app` never mentions Binance or DuckDB;
  every stored Bar is fully closed and passed `Validate`; Coverage is extended
  only for pages actually persisted; gap detection is expected − present −
  (ignored ∪ unrecoverable); Parquet export is DuckDB `COPY … TO`, not a
  hand-rolled writer; all HTTP via stdlib `net/http` + Go 1.22 pattern mux;
  CLI via stdlib `flag`. These are enforced by tests and `go list -deps`
  checks, not by review.
- Test seams, already the right height: fake `Provider` for `app` tests, real
  in-memory DuckDB (`:memory:`) for store and app tests, `httptest.Server`
  standing in for Binance for the adapter and the end-to-end CLI test.
- Deliberately NOT built, and it is not an oversight: scheduler/broker/job
  persistence; OpenTelemetry (structured logs via `log/slog` only); logical
  instruments; derived/materialized timeframes; composite datasets;
  Strict/Research mode; CSV import; the Binance bulk-archive adapter
  (`data.binance.vision`) — reserved, see spec "Reserved for later"; `1s`,
  `1w`, `1M` timeframes; auth; gap history. If a sub-agent proposes one, refuse.
- Branch: `main`. Commit each ticket separately, message subject starting with
  the ticket number. End commit bodies with
  `Claude-Session: <this session's URL>` if the harness provides one.

ON STOP — always end with this report (do not ask whether to continue):
## Result — STOPPED: <success | time | context | approach-exhausted>
- Outcomes: <N/8 verified> (checklist, with the closest state for any unmet ticket)
- Attempts used: <k> (<elapsed>), per-ticket: sub-agent pass / correction round / inline takeover
- Why it stopped: <which condition fired>
- Assumptions taken: <from ASSUMPTIONS.md>
- Recommended next steps: <ranked, concrete>   # only if not full success

SAFETY: autonomy covers building, testing and committing (by you — sub-agents
never commit). Pause and ask before anything destructive or irreversible —
force pushes, deleting `.git` or `.scratch`, rewriting history, or removing
files you did not create. Tests and dev runs must never hit `api.binance.com`;
`BINANCE_BASE_URL` always points at an httptest server in tests. Never point
the service at a DuckDB file outside a temp directory during tests.
````
