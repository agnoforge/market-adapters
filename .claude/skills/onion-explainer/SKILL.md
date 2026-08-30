---
name: onion-explainer
description: "Generate a self-contained 3-pane HTML explainer that teaches a system from the inside out: one concept per page, one SVG on the right that grows outward from a fixed centre (core → application → ports → adapters → composition root → full picture → runtime traces → tests → cheat sheet), clickable diagram, step-by-step flow highlighting, dark/light. Trigger: /onion-explainer <path> [output.html], or when the user asks for a progressive / layer-by-layer / 'build the diagram in the reader's head' walkthrough of an architecture (hexagonal, layered, onion). Reference example: docs/architecture-inside-out.html in market-acquisition."
---

# /onion-explainer <path> [output]

One HTML file, no libraries. Guiding rule: **do not teach the diagram — build the diagram in the reader's head.** Nothing appears on the right pane before the middle pane has explained it.

Differs from `/architecture-walkthrough` (a reference that shows everything at once): this one is a *teaching order*. Both can coexist; cross-link them.

## 1. Read the code — all of it

- List and `cat` every source file of the target (small repo: everything; large: entry point, every interface, one file per layer, the tests).
- Read `CONTEXT.md` / glossary, `docs/adr/*`, package doc comments, test harnesses (they tell you the real seams).
- Record the commit hash + date for the header.
- **Write down every claim you intend to make and where in the code it is true.** Status codes, what a test fakes, which file names a concrete type, what a package imports. If you cannot point at the line, do not write the sentence. Grep before handing over.

## 2. Find the onion

Identify, in this order, and only what actually exists:

| Ring | Question | Typical answer |
|---|---|---|
| Core | what does the system *talk about*? | domain types, invariants, stdlib-only package |
| Application | what can it *do*? | use cases / service methods |
| Ports | what does the application *need from outside*, in its own vocabulary? | interfaces owned by the app |
| Outbound adapters | who *keeps* those promises? | DB, HTTP client, queue |
| Inbound adapter | who *asks* the application to do things? | HTTP API, CLI, consumer |
| Composition root | who *names the concrete types*? | `cmd/…`, `main`, DI container |

If a ring is missing (no inbound port interface, no separate application layer, ports as function types), **say so on the page in a `.box.warn` "Honest note"**. Never draw symmetry the code does not have.

Pick **one running example** (one dataset / one request / one entity) and reuse it in every chapter.

## 3. Page plan (default; merge or drop, never pad)

```
00 The problem            reveal=""            plain language, no architecture words; ends with the organising question
01 Core layer             reveal="core"        vocabulary table; What it knows / must NOT know / who calls / what it calls
02..0k one core concept   reveal="core" focus=n-<x>   plain example first (monospace .tl block), then operations, then code
0k+1 Application          reveal="core app"    verbs; "a new box appeared AROUND the old one"
     one use case each    focus=n-<usecase>    intention / inputs / domain used / needs from outside / result
     "The app needs help" reveal="core app portq"   two honest questions ("should X know how Y works? No.") → unnamed sockets
     one port each        reveal="core app ports"   interface verbatim; doc-comment contract; error sentinels
     one outbound adapter each   cumulative reveal  "what stops here" table; where a second implementation would go
     inbound adapter      + "Honest note" on inbound ports
     Composition root     reveal=…+cmd         wiring code, read downward; dashed frame around everything
     Whole picture        reveal=all           ascii diagram + DERIVE the dependency rule from the pieces + 4–5 "where does X go?" <details>
     Trace A (happy path) data-flow="1"        6–8 steps, each names nodes+edges; every hop is adapter→app→port→adapter
     Trace B (error/repair path)
     Testing              table: layer / how tested / real vs fake — from the actual _test files
     Cheat sheet          thing / layer / responsibility / where + "where does new code go?" table
```

Each major page answers, in order: what is it · why does it exist · what problem it solves · where it lives · what it knows · what it must NOT know · who calls it · what it calls · small example · `▸ Show me the code` (collapsed).

Label theory vs. project: `<span class="badge concept">Concept</span>` in `.box.concept`; `<span class="badge agno">{{SYSTEM}}</span>` in the layer-coloured `.box`.

## 4. Build from `template.html`

Copy it next to the target's docs and fill in. Contracts are documented as HTML comments inside the template; the ones that break silently:

- `data-reveal` tokens must match `L-<token>` group ids **and** the `ALL_LAYERS` array in the script. Reveal sets are **cumulative** — a later page never hides an earlier ring.
- The centre rect (x 110–410, y 252–448) **never moves**; rings are drawn around it. Spatial memory is the point.
- Every clickable `<g class="node" id="n-…" data-go="<page id>">` needs a first-child `<rect>`; the focus/flow glow targets it.
- Steps: `<li data-nodes="n-a n-b" data-edges="e-a-b">`; edge ids must exist. Nodes not in the step are dimmed except the app/core/cmd frames.
- Colours only via `fill-domain/app/port/adapter/ext/root` classes → both themes work. No hex inside `<svg>`.
- Chip widths: ~7px per character at 10.5px bold + 16px padding. Measure by rendering, not guessing.
- Keep the topbar line: source commit + "if this page and the code disagree, the code wins" + link to the reference doc.

## 5. Verify before handing over

1. `python3 -m http.server 8765 &` in the docs folder (browsers block scripts/hash routing quirks on `file:` in some setups).
2. Open `#problem`, a core-concept page, `#needs`, `#root`, and one trace page. Read console (must be empty). Screenshot light **and** dark.
3. Look for: one-word-per-line wrapping in `.steps li small`, chip text overflowing its rect, dimmed nodes that should be lit, nav not scrolling to the active item, a reveal set that shrinks.
4. Re-grep every factual claim you were unsure about; delete the sentence if the code does not back it.
5. Kill the server; close the tab; update the project status file if it has one. Do not commit unless asked.

## Output

Path + page list (one line each) + the honest-note asymmetries you recorded + what was skipped (animation, zoom/pan, progress bar are optional by design).
