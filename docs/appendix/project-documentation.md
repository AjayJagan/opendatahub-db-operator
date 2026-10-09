---
description: General standard for organizing software project documentation for people and AI agents
audience: [ai]
load: When creating, reorganizing, or reviewing project documentation
---

# Project Documentation Standard

Organize documentation by reader and task; see [Cross-Reference and Loading Rules](#cross-reference-and-loading-rules) for linking and loading.

## Keep Source-Defined Facts in Their Source

Do not restate facts already defined in machine-readable sources. Version pins belong in manifests or lockfiles; tool lists and setup steps belong in scripts, build targets, devcontainers, or CI configuration; configuration values belong in the source that defines them. Prose copies drift when the source changes. If a human or AI reader can trivially find a fact in its defining file, link or point to that file instead of copying the fact.

Keep setup and installation in one codified path when practical, such as an install script, setup target, devcontainer, or CI workflow. Tell contributors to run that entrypoint, or point to the workflow when CI owns installation. If no codified path exists, add one before documenting a prose inventory of prerequisites. In `CONTRIBUTING.md`, prefer “Run `<setup-command>`; see `<setup-file>` for exact tools and versions” to listing pins or explaining tool purposes already apparent from that source. Explain a tool only when its project-specific use or a non-obvious constraint helps the reader decide what to do.

## Write for Audience and Task

Set detail by the reader's task and the context guaranteed by the document's audience and loading rules. Include what the reader needs to decide or act. Do not reteach concepts in documents the reader is expected to load; link to them instead. For machine-defined facts, follow [Keep Source-Defined Facts in Their Source](#keep-source-defined-facts-in-their-source). For AI-primary documents, make scope, conditions, and actions explicit. Keep sections useful when loaded alone by including only the minimum context needed to apply them.

Keep prose direct and dense:

- Lead with the point or action. Use active voice, keep one idea per sentence and paragraph, and do not repeat the heading in the opening sentence.
- Cut throat-clearing, generic introductions, needless hedging, filler such as “in order to,” and repeated summaries. Keep a caveat only when it changes a decision or action.
- Name the concrete behavior, constraint, or rationale. When reviewing AI-authored project documentation, remove generic prose that could be pasted unchanged into any project without helping its reader.
- Choose structure to fit the information: use short paragraphs for explanations, bullets for parallel rules or options, numbered lists for ordered steps, and tables for repeated comparisons. Use an example when it clarifies a boundary or correct application; do not turn simple prose into a list or table by default.

## Recommended Layout

```text
README.md
ARCHITECTURE.md
CONTRIBUTING.md
AGENTS.md
CLAUDE.md -> AGENTS.md
docs/
  <subsystem>.md
  appendix/
    <topic>.md
```

Add subsystem or appendix pages only for useful, focused detail; omit empty placeholders. Keep root documents concise entrypoints.

## README.md

**Audience and loading:** The README is the human entrypoint; agents load it for user-facing setup or operation.

**Include:** A concise project and user description, essential prerequisites, a verified quickstart (the shortest path that proves the project works), and a task-oriented help map.

**Keep out:** Developer setup and test matrices, implementation explanations, internal operating procedures, AI instructions, changelogs, and project history; they obscure the user path and age at different rates.

Update the README only when the project’s purpose, user entry path, or essential prerequisites change; skip routine internal changes. Omit frontmatter by default. Link to `CONTRIBUTING.md` for development and `ARCHITECTURE.md` for design; keep their content there.

## ARCHITECTURE.md

Keep the architecture page high-level as the stable system model; update it when system boundaries or major flows change.

**Include:** Major components and responsibilities, boundaries and trust assumptions, domain concepts, data flow, external dependencies, and explanatory diagrams. Source paths and symbols may direct readers to implementation without turning the page into an implementation tour.

**Keep out:** Code snippets, command-by-command procedures, class or function inventories, and details changed by ordinary implementation edits. Put specific contracts, state transitions, and integration procedures in subsystem pages.

```yaml
---
description: System boundaries, major components, and data flow
audience: [human, ai]
load: always
---
```

`AGENTS.md` makes the always-load rule explicit. Architecture pages link to subsystem docs for project behavior and appendix pages for background; those pages link back when system context helps orient readers.

## CONTRIBUTING.md

Keep commands and prerequisites current; stale verification steps waste contributor time.

**Include:** Supported development environment and setup; build and test commands, verification gates, test organization, local debugging, and contribution checks. Identify checks requiring credentials or external services.

**Keep out:** End-user quickstarts, duplicate architecture, exhaustive subsystem contracts, and unrelated team history; link to their source pages.

```yaml
---
description: Local setup, build, test, and verification workflow
audience: [human, ai]
load: When building, testing, debugging, or reviewing a change
---
```

Link to `ARCHITECTURE.md` for system context and subsystem pages for behavior-specific debugging. Keep commands here so readers need not reconstruct them from implementation notes.

## `docs/<subsystem>.md`

Keep each page focused on one important subsystem; choose its filename and `load` description so agents find it before edits.

**Include:** Project-specific contracts, inputs and outputs, state changes, invariants, integrations, configuration, failure modes, diagnostics, and extension points. Use a diagram, table, or short code example when it explains a real interface or constraint better than prose.

**Keep out:** Whole-system overviews, general technology tutorials, unrelated subsystem details, and `CONTRIBUTING.md` procedures. Document actual project use and constraints, not upstream documentation.

```yaml
---
description: Responsibilities and behavior of the <subsystem>
audience: [human, ai]
load: When changing or debugging the <subsystem>
---
```

Subsystem pages own project-specific behavior and integration contracts so architecture remains a map, not a reference manual; appendices hold reusable background. Link to `ARCHITECTURE.md` for system relationships, related subsystem pages for contracts, and appendices for background.

## `docs/appendix/<topic>.md`

Humans are secondary readers; use appendix pages for durable background on project dependencies, protocols, and concepts.

**Include:** Reusable explanations of a technology’s operation and terminology, key tradeoffs, and recurring constraints. A “How this project uses it” section may connect background to the project.

An appendix may combine broadly applicable guidance with a narrower addendum. When most of its content is broadly applicable, scope both its `load` trigger and the corresponding `AGENTS.md` reference to that broad use. Mark the sections clearly, such as “General guidance” followed by a topic-specific addendum, so `AGENTS.md` can also point readers to the narrower section for its specific task.

**Keep out:** Duplicate subsystem specifications, current project status, step-by-step project operations, and material unrelated to dependencies or recurring design choices. Link upstream-changing details to authoritative external references.

```yaml
---
description: Background on <technology or concept> used by the project
audience: [ai]
load: When work depends on understanding <technology or concept>
---
```

Link back to the related subsystem page for project-specific integration contracts.

## AGENTS.md and CLAUDE.md

AI agents and maintainers use these files to shape repository context. `AGENTS.md` is the concise, always-loaded instruction map; `CLAUDE.md` symlinks to it for Claude Code, so both entrypoints resolve to one source.

**Include in `AGENTS.md`:** Always-loaded file references; task triggers for conditional `CONTRIBUTING.md`, subsystem, and appendix pages; and brief guidance for recurring agent mistakes. Use supported import or reference syntax (for example, `@ARCHITECTURE.md`) for always-loaded files and explicit conditional triggers.

**Keep out:** Human-facing product guidance, copied architecture or contribution docs, generic advice, and Claude-only rules that can drift. Include AI-specific gotchas only when they do not belong in general human-facing project rules.

These files usually need no YAML frontmatter because the agent runtime loads the entrypoint directly. `AGENTS.md` routes agents to human-facing sources of truth; `CLAUDE.md` resolves to the same map, avoiding competing instructions.

## Cross-Reference and Loading Rules

- Give each fact one authoritative home; link elsewhere and summarize only needed rationale. For machine-defined facts, follow [Keep Source-Defined Facts in Their Source](#keep-source-defined-facts-in-their-source).
- Use relative repository links and section anchors when they save searching; use descriptive link text.
- Treat frontmatter as discovery metadata, not the only loading mechanism: the agent map must name each conditional document's trigger.
- Keep `description`, `audience`, and `load` concise and accurate. Set `load: always` only for nearly universal context; otherwise give a concrete trigger.
- When a document moves or changes scope, recheck links and loading triggers so needed constraints remain discoverable.
