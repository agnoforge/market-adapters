# Goal prompt — build the `notes` CLI and the Recycle Bin it needs

Paste the fenced block below into a fresh session at the repo root of
`smarter-notes`. It is self-contained: the executor loads its own skills,
delegates each ticket's implementation to the `ticket-implementer` sub-agent
(Opus 5, high effort), verifies every checkbox itself, and stops on its own.

Run this effort and `.scratch/notes-v2-editor-and-atlas/goal-prompt.md` **one at
a time, never concurrently** — both touch the session module, and both write
`GOAL_RUN.md`.

````markdown
You are to achieve the following goal autonomously, then stop and report.

SKILLS — activate both before doing anything else:
1. Run `/caveman` (full intensity). Stay in it for every response until the run ends.
2. Run `/implement`. Its rules govern all coding: TDD at pre-agreed seams,
   typecheck often, single test files often, full suite once at the end,
   `/code-review` when done, commit to the current branch.

GOAL: Build the `notes` CLI and the Recycle Bin it needs — all seven tickets in
`.scratch/notes-cli/issues/`, in dependency order, faithful to
`.scratch/notes-cli/spec.md`.

DELEGATION — how each ticket is built:
- Every ticket's implementation is executed by the `ticket-implementer`
  sub-agent (defined in `.claude/agents/ticket-implementer.md`; it runs on
  Opus 5 at high reasoning effort). Spawn it via the Agent tool with
  `subagent_type: "ticket-implementer"`, one agent per ticket, sequentially —
  never two at once, the tickets share files.
- The sub-agent's prompt must be self-contained: the ticket file path, both spec
  paths plus the exact sections the ticket cites, the glossary terms, the hard
  constraints from CONTEXT below, and a reminder that worked-example strings are
  exact. Sub-agents start with no conversation context — include everything they
  need.
- The sub-agent implements, tests, and self-verifies but does NOT commit.
  You (the orchestrator) then independently re-verify EVERY checkbox by
  running the checks yourself — the sub-agent's PASS report is a claim, not
  evidence. Only after your own verification passes do you commit.
- If the sub-agent's result fails your verification, send it back once via a
  follow-up message (SendMessage to the same agent) with the exact failing
  checkbox and observed output. If it fails again, take over that ticket
  inline yourself — that counts as the "change approach" move in the budget.

DEFINITION OF DONE — the seven outcomes below are the tickets themselves. A
ticket is done only when EVERY checkbox in its markdown file passes. Do not relax
a checkbox; do not mark a ticket done with checkboxes outstanding. Work them
strictly in this order: 01 → 02 → 03 → 04 → 05 → 06 → 07. (01 and 02 are both
unblocked and 03/04 depend only on 01, but run the sequence linearly.)

- O1: `01-the-notes-binary-and-where-it-points.md` — the `notes` executable, its
  single `run(argv, io)` entry point, root resolution, and `notes open`.
  Verify by: the entry point is the only thing any CLI test drives — no test
  spawns a process, touches the real home directory, or needs a terminal;
  `notes open`'s success and failure worked examples byte-exact; root resolution
  honours `--root` over `NOTES_ROOT` over the recorded path and exits 2 naming
  all three when none is set; `--json` prints exactly one pretty object with a
  trailing newline; JSON-mode failures go to stdout with nothing on stderr;
  human-mode errors go to stderr prefixed `error: `; exit codes 0/1/2 asserted;
  `npm link` produces a working `notes`; no new dependency in `package.json`.
- O2: `02-deleting-becomes-recoverable.md` — the Recycle Bin, in the domain.
  Verify by: a deleted Note lands in `.trash/` and vanishes from every listing,
  count, Query result and Atlas node; a deleted Folder becomes one entry with
  correct subfolder and note counts; entry ids are timestamp-formatted with a
  counter and unique within the same second; a trashed Note's mtime survives;
  the payload is recoverable by hand from the filesystem without the index; the
  app's delete lands in the same bin and its confirm copy no longer says
  "permanent"; a folder that has never had a deletion contains no `.trash` at
  all, proven with a recursive content+mtime snapshot; nothing outside `.trash/`
  is ever written; the gate is NARROWED, not switched off (see CONTEXT).
- O3: `03-reading-the-tree-from-the-terminal.md` — `tree`, `ls`, `cat`.
  Verify by: each command's §2 worked example, success and failure, byte-exact;
  `tree --query` shows Match Counts; `ls --all` spans the tree; ordering is
  date-edited descending with ties on Title; empty results are successful empty
  lists, not errors; `cat --content-only` prints the body alone; `--json` ids
  round-trip as arguments and dates are ISO 8601 UTC; dot-entries, non-`.md`
  files and the Recycle Bin appear nowhere; every reading command leaves a
  recursive snapshot unchanged; `--help` names the `ls --all --query --json` and
  `tree --json` spellings for search and the Atlas.
- O4: `04-writing-from-the-terminal.md` — `add`, `edit`, `append`, `retitle`,
  `mv`, `mkdir`, `rename-folder`, `mvdir`.
  Verify by: each §2 worked example, success and failure, byte-exact; `append`
  inserts a separating newline ONLY when the existing body does not already end
  in one and adds nothing else, proven by reading the file back, including the
  empty-Note case; `-` and `--text -` read stdin; collisions and self-nesting
  moves fail and change nothing; the Root Folder cannot be renamed or moved;
  every write is byte-identical to what was supplied with no added markup ever.
- O5: `05-destructive-commands-an-agent-can-be-trusted-with.md` — `rm`, `rmdir`.
  Verify by: both move to the Recycle Bin and reproduce their §2 failure messages
  verbatim; without `--yes` and without a terminal both exit 2 and leave a
  recursive snapshot unchanged; with a terminal and no `--yes` both prompt once
  and a decline changes nothing at exit 0; `--dry-run` changes nothing, exits 0,
  and is honoured with or without `--yes`; the Folder dry run reports folder,
  subfolder and note counts for the whole subtree in the exact shape the spec
  shows; the Root Folder cannot be deleted.
- O6: `06-browsing-and-restoring-the-recycle-bin.md` — the `trash` subcommands.
  Verify by: `list` shows id, type, name, origin and deletion time newest first;
  `get` returns full detail including counts for a Folder entry; `restore` puts a
  Note back at its origin with its body intact and removes the entry, and
  restores a Folder's whole subtree; a missing origin and a name collision each
  fail changing nothing, and `--folder` succeeds for the first; `delete --yes`
  and `empty --yes` remove payloads and entries leaving the live tree untouched;
  both destructive trash commands carry the full ticket-05 safety machinery;
  nothing outside `.trash/` is read or written.
- O7: `07-conformance-and-install.md` — the sweep.
  Verify by: one suite drives all thirteen §2 commands through the CLI entry
  point against a real temp folder, asserting each worked example success and
  failure byte for byte, plus `append` and the `trash` subcommands; every
  command's `--json` form parses and carries the same facts as its human form;
  the exit-code contract asserted end to end; the existing usecase-level
  conformance test unchanged and still passing; `npm link` works; the README
  documents install, root configuration, the command list and the three shared
  flags; `npm test`, `npm run build` and `npm run gate` all pass.

AFTER EVERY TICKET — all four must hold before you commit:
- `npm test` green and `npx tsc --noEmit` clean, output recorded.
- `npm run gate` passes.
- No new runtime or dev dependency in `package.json`. Argument parsing is Node's
  built-in `parseArgs`, the prompt is built-in `readline`. If you reach for a CLI
  framework, you have taken the wrong approach.
- Notes themselves still carry zero app-written metadata — no frontmatter, no
  ids, ever. `.trash/` is the ONLY place app-specific data may go.

BUDGET (stop at the FIRST of these — safety net, not a target):
- Success: O1–O7 all verified.
- Time: at most 6 hours. Record start with `date -u +%s`, compute the deadline,
  and re-check it before starting each ticket and before each verify cycle.
- Per-ticket: one sub-agent attempt plus one correction round; if verification
  still fails, take over inline. If your inline attempt fails the same way
  twice, change approach rather than retrying a third time.
- Context hygiene: if the context window degrades mid-run, finish and commit the
  current ticket, write a handoff note in `GOAL_RUN.md`, and stop. This prompt is
  re-runnable; a fresh session resumes at the next unstarted ticket.

HOW TO RUN:
0. Before delegating ticket 01, check `node --version`. The binary relies on
   Node's native TypeScript stripping and needs 22.18+ or 23+. If Node is older,
   that is a decision to report — the documented fallback is a `tsc` emit into a
   build directory — not something to work around silently.
1. Read `.scratch/notes-cli/spec.md` in full, then `.scratch/notes-v2/spec.md`
   §2 in full — §2 is the golden output for thirteen of the commands and was
   written before the binary existed. Read `CONTEXT.md` for the ubiquitous
   language and `docs/adr/0004-*` and `docs/adr/0005-*` for the decisions already
   taken. The sub-agents read the specs too, but you must know them to verify.
2. Write `GOAL_RUN.md` at the repo root, starting fresh (the file currently holds
   the stale notes-v2 run): the goal, O1–O7 as a checklist, the
   after-every-ticket invariants, budget, start time, deadline. Append an
   attempt-log entry per cycle, including each sub-agent's per-checkbox report
   verbatim.
3. Per ticket: budget gate → spawn `ticket-implementer` with the self-contained
   prompt → receive its report → re-verify every checkbox yourself → tick what
   passes, record what failed and why → update the ticket file (boxes +
   `Status: done`) → commit with the ticket number in the subject → next ticket.
   Never start ticket N+1 with ticket N's checkboxes outstanding.
4. Keep a running `ASSUMPTIONS.md` merging your assumptions and any the
   sub-agents report, for anywhere the specs are silent. Ticket 07 requires these
   written up; do not let a sub-agent decide silently.
5. Deviations discovered mid-run go into a new ADR or an amendment to ADR-0005 —
   never invent patterns silently. New domain vocabulary goes into `CONTEXT.md`
   as it crystallises, glossary-only, no implementation detail.
6. Full test suite once at the end, then `/code-review`.
7. Stop on the first stop condition. Never extend the budget silently and never
   relax an outcome to force a pass; if an outcome is wrong or impossible, say
   so.

CONTEXT:
- Repo root: this directory. The app is built and working; this effort adds a
  second inbound adapter over the SAME usecases. If a behaviour differs between
  the app and the CLI, that is a bug in one of the two adapters, not a feature.
- Specs: `.scratch/notes-cli/spec.md` is the authority for this effort.
  `.scratch/notes-v2/spec.md` §2 already specified thirteen of the commands —
  their names, arguments, human output and every failure message — as "a design
  lens". Treat those worked examples as GOLDEN OUTPUT; do not re-derive them and
  do not reword a failure message. `src/spec-conformance.test.ts` already asserts
  them at the usecase layer and must remain unchanged.
- Tickets: `.scratch/notes-cli/issues/01`–`07`, one file per outcome.
- ADRs: `docs/adr/0005-the-recycle-bin-narrows-the-no-metadata-vow.md` is already
  written and records the Recycle Bin decision. Do not re-litigate it; implement
  it. `docs/adr/0004-*` still governs everything else.
- Glossary: `CONTEXT.md` — Folder, Root Folder, All Notes, Note, Title, Atlas,
  Match Count, Query, Recycle Bin. Use these words in code, tests and commits;
  avoid the listed `_Avoid_` terms.
- Hard constraints (pass these to every sub-agent): the CLI contains no domain
  logic, no filesystem access of its own, and no second implementation of
  anything the app does — it calls the existing usecases through the existing
  container; domain imports nothing; usecases import ports + domain and never an
  adapter; only the composition root names concrete types; nothing in
  `domain/`, `ports/` or `usecases/` may import the CLI adapter; identity is the
  path — no synthetic ids for live Notes or Folders (ADR-0004), Recycle Bin
  entries are the sole exception; no random-uuid — trash ids are timestamp plus
  counter. These are enforced by tests and `npm run gate`, not by review.
- The gate is NARROWED in ticket 02, not switched off:
  `scripts/dependency-gate.mjs` currently fails any source file naming
  `index.json` and any naming `randomUUID`. Scope the first to the trash adapter
  only, add a companion rule that `.trash` is the only dot-path any source file
  may name, and leave the `randomUUID` ban exactly as it is.
- Folder trashing reuses the existing recursive copy-and-delete mechanism (the
  File System Access API cannot move directories). Restoring a Folder therefore
  resets contained mtimes and bumps those Notes to "Today". That is accepted in
  spec §8.8 — document it, do not build a workaround.
- Test seams, both already the right height: `run(argv, io)` for everything CLI
  (new in ticket 01, and the only new seam this effort introduces), and the
  existing temp-folder helper for the usecase-level trash work. Every test runs
  against a real temp directory — a test that only exercises an in-memory fake
  does not satisfy a filesystem criterion.
- SAFETY-CRITICAL for tests: `notes open` records a path in the user's config
  directory. Tests must use the INJECTED home directory from `io`, never the real
  `$HOME`, and never a real notes folder. A test that writes to the real config
  directory is a failing test even if it asserts correctly.
- Deliberately NOT built, and it is not an oversight: `notes search` and
  `notes atlas` as separate commands (covered by `ls --all --query --json` and
  `tree --json`); synthetic ids; a Recently Deleted view in the app;
  match-context snippets; `--older-than`; the search date filters. If a sub-agent
  proposes one, refuse it.
- Branch: `main`. Commit each ticket separately.

ON STOP — always end with this report (do not ask whether to continue):
## Result — STOPPED: <success | time | context | approach-exhausted>
- Outcomes: <N/7 verified> (checklist, with the closest state for any unmet ticket)
- Attempts used: <k> (<elapsed>), with per-ticket note: sub-agent pass / correction round / inline takeover
- Why it stopped: <which condition fired>
- Assumptions taken: <from ASSUMPTIONS.md>
- Recommended next steps: <ranked, concrete>   # only if not full success

SAFETY: autonomy covers building, testing and committing (by you — sub-agents
never commit). Pause and ask before anything destructive or irreversible —
force pushes, deleting `.git` or `.scratch`, rewriting history, or removing
files you did not create. Never point a test, a dev run, or the CLI at a real
notes folder — always a temp directory. This effort builds commands that delete
things; every one of them must be exercised against a temp directory only.
````
