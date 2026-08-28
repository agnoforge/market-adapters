# Goal prompt — build the Developer Playground and OpenTelemetry tracing

Paste the fenced block below into a fresh session at the repo root of
`market-acquisition`. It is self-contained: the executor loads its own skills,
delegates each ticket to the `ticket-implementer` sub-agent, verifies every
checkbox itself, and stops on its own.

Do not run this concurrently with any other goal prompt — every ticket touches
`cmd/agnoforge` and both write `GOAL_RUN.md`.

````markdown
You are to achieve the following goal autonomously, then stop and report.

SKILLS — activate both before doing anything else:
1. Run `/caveman` (full intensity). Stay in it for every response until the run ends.
2. Run `/implement`. Its rules govern all coding: TDD at pre-agreed seams,
   `go vet` often, single test packages often, full suite once at the end,
   `/code-review` when done, commit to the current branch.

GOAL: Add OpenTelemetry tracing and the Developer Playground to the Market Data
Acquisition service — all six tickets in `.scratch/playground/issues/`, in
dependency order, faithful to `.scratch/playground/spec.md` and ADR 0003.

DELEGATION — how each ticket is built:
- Every ticket's implementation is executed by the `ticket-implementer`
  sub-agent (`.claude/agents/ticket-implementer.md`). Spawn it via the Agent
  tool with `subagent_type: "ticket-implementer"`, one agent per ticket,
  sequentially — never two at once, the tickets share files.
- The sub-agent's prompt must be self-contained: the ticket file path, the spec
  path plus the exact sections the ticket cites (Tracing, Trace store,
  Playground HTTP surface, UI, Verification), the ADR 0003 path, the glossary
  terms, the hard constraints from CONTEXT below, and the "deliberately NOT
  built" list. Sub-agents start with no conversation context.
- The sub-agent implements, tests, and self-verifies but does NOT commit.
  You (the orchestrator) then independently re-verify EVERY checkbox by
  running the checks yourself — the sub-agent's PASS report is a claim, not
  evidence. Only after your own verification passes do you commit.
- If the sub-agent's result fails your verification, send it back once via
  SendMessage to the same agent with the exact failing checkbox and observed
  output. If it fails again, take over that ticket inline yourself — that
  counts as the "change approach" move in the budget.

DEFINITION OF DONE — the six outcomes below are the tickets themselves. A
ticket is done only when EVERY checkbox in its markdown file passes. Do not
relax a checkbox; do not mark a ticket done with checkboxes outstanding. Work
them strictly in this order: 01 → 02 → 03 → 04 → 05 → 06 (04 depends only on
01, but run the sequence linearly).

- O1: `01-otel-foundation.md` — OTel SDK wired in `cmd`, `otelhttp` around the
  API, `X-Trace-ID` on every response, CLI prints `trace: <id>` on non-2xx.
  Verify by: `go test -race ./...` green; `go list -deps ./internal/...` shows
  no `go.opentelemetry.io/otel/sdk` package; a request's `X-Trace-ID` is 32 hex
  and equals the server span's trace id; an inbound `traceparent` is continued;
  the CLI stderr on a 4xx contains `trace: `.
- O2: `02-app-and-adapter-spans.md` — inline spans `app.*`, `binance.*`,
  `duckdb.*` with `agnoforge.layer` and the domain attributes; errors recorded
  on the failing span; worker trace rooted at `app.HistoricalBackfill` with a
  Link to `app.StartBackfill` and `agnoforge.backfill.id`.
  Verify by: the request-trace tree `httpapi → app.StartBackfill →
  binance.EarliestAvailable` asserted by parent ids against the fake provider;
  the execution trace is a different trace id, carries the Link, and contains
  `binance.get`, `duckdb.UpsertBars`, `duckdb.ExtendCoverage`, `app.DetectGaps`;
  unknown symbol → `binance.EarliestAvailable` has status Error and the HTTP
  span does not; `binance.get` has one event per retry and per rate-limit wait;
  grep of attribute keys finds no body/header values.
- O3: `03-trace-store-and-query.md` — `internal/adapters/playground`:
  `SpanProcessor` with OnStart/OnEnd, ring of 256 traces, index by trace id and
  backfill id, `GET /playground/traces/{id}` and `?backfill_id=`.
  Verify by: just-made request's `X-Trace-ID` is retrievable, unknown id 404;
  a running backfill shows `end: null` spans, after `Wait` none; the 257th trace
  evicts the first; `-race` clean under concurrent OnStart/OnEnd/reads; the
  `// ponytail:` comment naming the per-trace cap exists.
- O4: `04-operation-catalog.md` — hand-written `[]Operation`, one per domain
  route, curl-able params and the CLI's positional template, served at
  `GET /playground/operations`.
  Verify by: exactly ten operations matching `internal/adapters/httpapi/api.go`
  routes; every operation's example request hits the real mux without 404/405;
  CLI templates use the real positional syntax
  (`agnoforge data backfill binance BTCUSDT 1m 2024-01-01 2024-02-01`), never
  `--provider`-style flags.
- O5: `05-playground-ui.md` — `go:embed index.html` at `GET /playground/`,
  vanilla JS, no build step.
  Verify by: page and `/playground/operations` load in a Go test; a backfill
  executed through the same endpoints yields a fetchable trace id; you open the
  page in a browser (`/run` skill or manual) and confirm waterfall, span detail,
  first-Error highlight and the execution-trace poll; no `package.json`, no
  external `<script src>`.
- O6: `06-docs-and-status.md` — README "Playground" section linking ADR 0003,
  `ASSUMPTIONS.md` §09 (trace JSON, header, retention), `where-we-are-at.md`.
  Verify by: each file contains the named content.

AFTER EVERY TICKET — all four must hold before you commit:
- `go build ./...`, `go vet ./...`, `go test -race ./...` all clean, output recorded.
- Direct dependencies in `go.mod` are exactly: duckdb-go, `go.opentelemetry.io/otel`,
  `go.opentelemetry.io/otel/sdk`, `go.opentelemetry.io/otel/trace`,
  `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` (plus what
  `go mod tidy` marks indirect). No OTLP exporter, no Jaeger, no web framework,
  no JS toolchain.
- `internal/domain` imports nothing outside stdlib; `internal/app` imports the
  `otel` API at most, never `sdk`; nothing under `internal/` imports `cmd`.
- The eight domain routes and their JSON/Parquet wire format are unchanged: the
  existing `httpapi` and `cmd` tests pass without edits to their assertions.

BUDGET (stop at the FIRST of these — safety net, not a target):
- Success: O1–O6 all verified.
- Time: at most 5 hours. Record start with `date -u +%s`, compute the deadline,
  and re-check it before starting each ticket and before each verify cycle.
- Per-ticket: one sub-agent attempt plus one correction round; if verification
  still fails, take over inline. If your inline attempt fails the same way
  twice, change approach rather than retrying a third time.
- Context hygiene: if the context window degrades mid-run, finish and commit the
  current ticket, write a handoff note in `GOAL_RUN.md`, and stop. This prompt is
  re-runnable; a fresh session resumes at the next unstarted ticket.

HOW TO RUN:
0. Before delegating ticket 01, run `go version` (module says 1.25) and
   `go list -m -versions go.opentelemetry.io/otel | tail -c 200` to pin the
   latest stable otel/sdk/otelhttp versions; record them in `ASSUMPTIONS.md`.
   If module download fails (offline), that is a decision to report, not to
   work around with a vendored copy.
1. Read `.scratch/playground/spec.md` in full, `docs/adr/0003-*` and
   `docs/adr/0001-*`, then `CONTEXT.md`, `ASSUMPTIONS.md` §07–08 and
   `internal/app/ports.go`. The sub-agents read them too, but you must know
   them to verify.
2. Write `GOAL_RUN.md` at the repo root, starting fresh (it currently holds the
   Phase 1 run): the goal, O1–O6 as a checklist, the after-every-ticket
   invariants, budget, start time, deadline. Append an attempt-log entry per
   cycle, including each sub-agent's per-checkbox report verbatim.
3. Per ticket: budget gate → spawn `ticket-implementer` with the self-contained
   prompt → receive its report → re-verify every checkbox yourself → tick what
   passes, record what failed and why → update the ticket file (boxes +
   `Status: done`) → commit with the ticket number in the subject → next ticket.
   Never start ticket N+1 with ticket N's checkboxes outstanding.
4. Keep `ASSUMPTIONS.md` growing (new §09) for anywhere the spec is silent:
   exact span attribute keys beyond the listed ones, trace JSON field names,
   time formats. Do not let a sub-agent decide silently.
5. Deviations discovered mid-run go into an amendment to ADR-0003 — never
   invent patterns silently. `CONTEXT.md` is NOT touched by this effort: Trace,
   Span, Operation are tooling words, not domain words (decided in grilling).
6. Full test suite once at the end, then `/code-review`.
7. Stop on the first stop condition. Never extend the budget silently and never
   relax an outcome to force a pass; if an outcome is wrong or impossible, say
   so.

CONTEXT:
- Repo root: this directory. Phase 1 is built, tested and committed
  (tickets in `.scratch/market-acquisition/issues/`). This effort adds
  instrumentation inside the existing layers plus one new inbound adapter
  package; it adds no business logic anywhere.
- Layout: `cmd/agnoforge` (serve + CLI, composition root), `internal/domain`,
  `internal/app` (Service, ports), `internal/adapters/{httpapi,binance,duckdb}`.
  New: `internal/adapters/playground`. Dep direction domain ← app ← adapters ← cmd.
- Spec: `.scratch/playground/spec.md` is the authority. Where it and the
  original requirements report differ, the spec wins.
- ADR 0003 is already written and accepted. Do not re-litigate: real OTel SDK
  (not hand-rolled), in-process store (not Jaeger), linked worker trace (not
  child), inline spans (not decorators).
- Glossary: `CONTEXT.md` — Provider, Symbol, Timeframe, Dataset, Bar,
  Backfill, Coverage, Gap, Settled, Trading Calendar, Repair, Complete. Use
  these words in span names and attributes; avoid the listed `_Avoid_` terms.
- Hard constraints (pass these to every sub-agent): `internal/app` and
  adapters import `go.opentelemetry.io/otel` + `.../trace` + `.../attribute`
  only; `go.opentelemetry.io/otel/sdk/...` and `otelhttp` appear only in `cmd`
  and in `internal/adapters/playground` (the SpanProcessor needs `sdk/trace`
  types — that package is an adapter, so allowed); the Playground package
  calls nothing on `app.Service` except through HTTP like any other client —
  it holds no reference to the Service, the Store or a Provider; no span
  attribute ever carries a request/response body or header value; span names
  are exactly the package-prefixed names in the spec; `agnoforge.layer` is on
  every span; the Binance adapter's retry loop and rate limiter stay as they
  are — add events, do not restructure.
- Test seams already right: `app.Service` with the fake provider
  (`internal/app/fake_provider_test.go`), `httptest` over the `httpapi` mux,
  `cmd/agnoforge` `run(argv, …)`. Add one seam: the playground processor is
  constructed with a `sdktrace.TracerProvider` in tests so span trees are
  asserted in-memory — no real Binance, no network, ever.
- The UI is one `index.html` + inline `<script>`; browser verification is by
  eye once. Do not add Playwright, Node, or a bundler.
- Deliberately NOT built, and it is not an oversight: OTLP exporter or any
  remote backend; metrics or log correlation; span-level sampling config;
  executing CLI commands from the browser; a backfill details page (Phase 5);
  trace search / recent-requests list (Phase 6); OpenAPI generation; changing
  the CLI to flag syntax; adding Trace/Span to `CONTEXT.md`; a per-trace span
  cap (a `// ponytail:` comment names it). If a sub-agent proposes one, refuse.
- Branch: `main`. Commit each ticket separately.

ON STOP — always end with this report (do not ask whether to continue):
## Result — STOPPED: <success | time | context | approach-exhausted>
- Outcomes: <N/6 verified> (checklist, with the closest state for any unmet ticket)
- Attempts used: <k> (<elapsed>), with per-ticket note: sub-agent pass / correction round / inline takeover
- Why it stopped: <which condition fired>
- Assumptions taken: <from ASSUMPTIONS.md §09>
- Recommended next steps: <ranked, concrete>   # only if not full success

SAFETY: autonomy covers building, testing and committing (by you — sub-agents
never commit). Pause and ask before anything destructive or irreversible —
force pushes, deleting `.git` or `.scratch`, rewriting history, or removing
files you did not create. Never point a test at `api.binance.com`; every trace
test runs against the fake provider and an in-memory span processor. Never
point a test at a DuckDB file outside a temp directory.
````
