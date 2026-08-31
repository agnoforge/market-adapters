## Agent skills

### Issue tracker

Issues and specs live as markdown files under `.scratch/<feature>/`. See `docs/agents/issue-tracker.md`.

### Domain docs

Multi-context: `CONTEXT-MAP.md` at the repo root points at one `CONTEXT.md` per context (`internal/<context>/CONTEXT.md`); ADRs in `docs/adr/`. See `docs/agents/domain.md`.

### Status file

After finishing any implementation work, update `where-we-are-at.md` at the repo root without being asked: what was just done (feature level, not file level) and what could happen next — including simple things like "user manually evaluates X". Keep it short; overwrite stale entries rather than appending forever.
