# Feature: Skill Engine — Deep Workflow System for aimux

**Slug:** skill-engine
**Created:** 2026-04-08
**Status:** Draft
**Author:** AI Agent (reviewed by user)

> **Provenance:** Specified by claude-opus-4-6 on 2026-04-08.
> Evidence from: 72 patterns across 5 codebases (nvmd-ai-kit, ECC, claude-octopus, feynman, pr-review-mcp),
> 9 research agents (3 passes), existing aimux codebase (13 tools, 23 think patterns, 10 MCP prompts).
> Confidence: VERIFIED (research data) / INFERRED (architecture decisions).

## Overview

Replace shallow MCP prompt handlers with a skill engine that loads deep workflow templates
from `config/skills.d/`, renders them with live system data, and delivers them via MCP prompts.
Skills are not instructions to "call tool X" — they are full orchestration workflows where each
phase feeds output into the next through session state, file handoffs, template variables,
and cross-skill references.

## Context

**Problem:** Current 10 MCP prompts are 50-80 line Go string builders. They show what tools exist
but don't teach agents HOW to orchestrate multi-step workflows. Research found 72 patterns from
production skill systems — delegation trees, evidence schemas, WTF-scores, integrity commandments,
GAN evaluation — all of which lose value when summarized to "call think(pattern=X)".

**What exists today:**
- `pkg/server/server.go` — 2800+ lines, 10 prompt handlers as Go functions
- `pkg/prompt/` — prompt engine loading role prompts from `config/prompts.d/`
- `config/cli.d/` — CLI profiles as YAML (precedent for config-as-files pattern)
- `pkg/orchestrator/workflow.go` — workflow tool with step conditions, template vars `{{step.content}}`
- `pkg/investigate/` — session-based investigation with convergence tracking, cross-tool dispatch
- `pkg/think/` — 23 patterns, some stateful (session_id), cross-tool dispatch from investigate

**What's missing:**
- Skills as separate files (not Go strings)
- Dynamic data injection into skill templates
- Cross-skill references (debug → security, audit → investigate)
- Conditional sections based on runtime state (available CLIs, past reports, failed attempts)
- Loop safety (WTF-score, cycle detection) in workflow tool

## Functional Requirements

### FR-1: Skill Template Engine
System loads markdown templates from `config/skills.d/*.md`, parses YAML frontmatter for
metadata, and renders Go `text/template` expressions with live system data.

**Frontmatter schema:**
```yaml
# Required
name: review              # slug, used as MCP prompt name (aimux-{name})
description: "Code review workflow with multi-phase gates"

# Optional (defaults shown)
prompt: true              # register as MCP prompt (false = internal/fragment only)
args:                     # MCP prompt arguments
  - name: scope
    description: "What to review: staged, branch, last-commit, or file paths"
related: [security, audit]  # cross-references (bidirectional auto-computed)
tags: [review, quality]     # for search/filter (not used in v1, reserved)
```

### FR-2: Dynamic Data Injection
Every skill template has access to a `SkillData` struct containing: enabled CLIs (names + count),
role routing (which CLI handles which role), all fields from `pkg/metrics/MetricsSnapshot`
(TotalRequests, ErrorRate, PerCLI stats), active sessions, past investigation reports,
available agents, think patterns, caller's discovered skills, and request-specific arguments
from the MCP prompt call.

### FR-3: Conditional Workflow Sections
Templates use Go template conditionals (`{{if}}`, `{{range}}`, `{{with}}`) to include/exclude
sections based on runtime state. Example: consensus section only appears when 2+ CLIs available.
deepresearch section only when Gemini is enabled.

### FR-4: Cross-Skill References
Each skill declares `related: [skill-name, ...]` in frontmatter. The engine resolves these
to actual skill metadata and injects navigation hints into rendered output:
"See also: aimux-security (security audit), aimux-investigate (deep analysis)".

### FR-5: MCP Prompt Auto-Registration
Skills with `prompt: true` in frontmatter are automatically registered as MCP prompts on
server startup. Prompt name = `aimux-{skill-slug}`. Arguments from frontmatter `args:` list.
No Go code changes needed to add new skills.

### FR-6: Interconnection Primitives
The engine injects live data into templates. Skills use that data to generate instructions
for the agent. The engine does NOT execute tools. The interconnection mechanisms are
expressed as text in skill templates, not as engine logic:
- **Session continuity:** `session_id="<from Phase N>"` carries state between investigate/think phases
- **File handoffs:** `investigate(action="report") → path` becomes input to next phase via `<report path>`
- **Template vars in workflows:** `{{step_id.content}}` carries step output forward
- **Cross-skill invocation:** "Invoke: `aimux-security(scope=<finding>)`" as text instruction
- **Loop detection hint:** "If 2+ failed attempts on same error, HALT and invoke aimux-consensus"

### FR-7: Skill Validation
On load, engine validates: frontmatter present, required fields (name, description), template
parses without error, related skills exist. Invalid skills logged as warnings, not loaded.

### FR-8: User-Extensible
Users drop `.md` files into `config/skills.d/` — aimux auto-discovers and registers them.
Built-in skills have no priority over user skills. User skills can override built-in skills
by using the same slug.

### FR-9: Shared Template Fragments
Skills compose from reusable fragments via Go `{{template "fragment-name" .}}`. Fragments
live in `config/skills.d/_fragments/*.md` (underscore prefix = not registered as prompts).
Engine loads fragments via `ParseGlob` alongside skill templates. Shared fragments include:
evidence-table, verification-gate, delegation-tree, priority-scoring, integrity-commandments.

### FR-10: Bidirectional Skill Graph
Engine computes a reverse adjacency map from `related:` frontmatter on load. If skill A
declares `related: [B]`, skill B automatically gets A in its RelatedSkills. Rendered output
includes "See also:" section with bidirectional links. No manual maintenance of reverse refs.

### FR-11: Think Tool Schema Fix
Add 5 missing structured parameters to MCP think tool schema: `criteria` (JSON string,
array of {name, weight}), `options` (JSON string, array of {name, scores}), `components`
(JSON string, array of {name, description, dependencies}), `sources` (JSON string, array),
`findings` (JSON string, array). Pattern validators parse JSON internally. This unblocks
decision_framework, architecture_analysis, source_comparison, research_synthesis patterns
that are currently unusable via MCP.

### FR-12: Skill Graph Map (prerequisite for authoring)
Before writing any skill templates, build a complete graph map of all skills, tools,
fragments, and their interconnections. The map is the source of truth — skills are
authored FROM the map, not improvised.

**Map artifact:** `config/skills.d/_map.yaml` — machine-readable skill graph.
```yaml
skills:
  debug:
    tools: [investigate, think, exec]
    phases: [reproduce, investigate, root-cause, fix, verify]
    related: [security, audit, review]
    fragments: [evidence-table, verification-gate]
    escalates_to: [consensus]
    receives_from: [audit]  # audit routes P0 bugs here
  review:
    tools: [exec, think, consensus]
    phases: [gather-diff, review, critique, fix]
    related: [security, audit, debug]
    fragments: [evidence-table, priority-scoring]
    escalates_to: [consensus, debate]
    receives_from: [audit]
  # ... all skills

fragments:
  evidence-table:
    used_by: [debug, review, audit, investigate]
  verification-gate:
    used_by: [debug, audit, review]
  # ...

tool_usage:
  investigate: [debug, audit, research]
  think: [debug, review, research, consensus]
  exec: [debug, review, audit, research]
  consensus: [consensus, review, debug]  # escalation target
  # ...
```

**Rules:**
- Map MUST be created before any skill .md is authored
- Map MUST be updated when skills change (add/remove/modify phases or tool usage)
- Engine validates at load time: every skill in _map.yaml has a corresponding .md file
- Engine validates: every `related:` reference in .md matches `related:` in map
- Skill validation (FR-7) checks consistency between map and actual frontmatter
- The map drives: cross-skill graph (FR-10), fragment dependency tracking (FR-9),
  tool coverage analysis ("which tools have no skill?")

## Non-Functional Requirements

### NFR-1: Performance
Template rendering < 5ms per skill. Skill loading at startup < 100ms for 50 skills.
No runtime file I/O per prompt request — templates cached at startup, re-parsed on SIGHUP.

### NFR-2: Modularity
Skill engine is a separate package `pkg/skills/`. No direct dependency on `pkg/server/`.
Server imports skills engine and calls `Render(name, data)`. Template files are separate
from Go code. Adding a skill requires zero Go changes.

### NFR-3: Resilience
Template engine uses `Option("missingkey=zero")` — missing fields render as zero values
(empty string, 0, false, nil slice) instead of panicking. Skills authored with `{{if .Field}}`
guards for optional data. Engine wraps `Execute()` in `recover()` — a panicking template
returns graceful error message, never crashes the server. Degraded output > no output.

### NFR-4: Backwards Compatibility
Existing 4 prompt handlers (guide, investigate, workflow, background) continue to work.
They can coexist with skill-based prompts. Migration path: move handler logic to .md template,
replace Go handler with 5-line skill renderer.

### NFR-5: Binary Size
Skill templates embedded via `//go:embed config/skills.d/*.md` for single-binary distribution.
Runtime `config/skills.d/` override if directory exists on disk.

**Load priority (highest wins):**
1. Disk `config/skills.d/` — user overrides
2. Embedded (go:embed) — built-in defaults
Same slug in higher layer replaces lower. Matches `config/cli.d/` precedent.

### NFR-6: Caller Skill Awareness
aimux is called FROM AI agents (Claude Code, Codex, OpenClaw) that have their own skills,
agents, and commands. The skill engine must be aware of the caller's capabilities:
- **Discovery:** On connect, scan caller's CWD for `.claude/skills/`, `.claude/agents/`,
  `AGENTS.md`, `.agents/skills/` (Codex), `.openclaw/skills/` — build a caller capability map.
- **Deduplication:** If caller already has a "code-review" skill, aimux's review workflow
  should reference it (`use your code-review skill`) rather than duplicating instructions.
- **Composition:** Skills can reference caller capabilities: "If your agent has a TDD skill,
  invoke it. Otherwise, follow this embedded TDD workflow: ..."
- **Dynamic sections:** `{{if .CallerHasSkill "tdd"}}` enables conditional content based on
  what the calling agent already knows how to do.

This addresses the P1 inbox item: "Auto-discover calling agent's available agents."

**Two tiers of skills:**
- **Built-in skills** (config/skills.d/) = deeply choreographed workflows that virtuosically
  use all aimux tools, sessions, cross-tool dispatch, interconnections. They ARE the product.
- **Discovered skills** (from caller) = generic capability map for adaptation. Skills use
  `{{if .CallerHasSkill "tdd"}}` to defer to caller's existing capability rather than
  duplicating it. Discovered skills extend reach, built-in skills provide depth.

## User Stories

### US1: Agent Receives Deep Debug Workflow (P0)
**As an** AI agent connected to aimux via MCP, **I want** to receive a complete debug workflow
when I call the aimux-debug prompt, **so that** I can systematically investigate a bug using
investigate sessions, think patterns, and exec fixes in a connected pipeline.

**Acceptance Criteria:**
- [ ] aimux-debug prompt returns 100+ line workflow with 5 phases
- [ ] Phases reference aimux tools with exact parameters
- [ ] Phase 2 (investigate) carries session_id to Phase 3 (think)
- [ ] Phase 4 (fix) receives report path from Phase 2
- [ ] Conditional: escalation section appears only after 2+ failed attempts
- [ ] Dynamic: shows actual debug CLI name (not hardcoded "codex")

### US2: Skill Cross-References Guide Agent Between Workflows (P0)
**As an** AI agent following an audit workflow, **I want** the audit skill to tell me which
other aimux skills to invoke for specific finding types, **so that** I don't treat audit as
an isolated step but route P0 findings to debug, security, or refactor workflows.

**Acceptance Criteria:**
- [ ] Audit skill output includes "For P0 security findings: invoke aimux-security(scope=...)"
- [ ] Audit skill output includes "For P0 bugs: invoke aimux-debug(error=...)"
- [ ] Related skills section lists all connected skills with 1-line descriptions
- [ ] Cross-references use actual prompt names that the agent can call

### US3: Dynamic Routing Based on Available CLIs (P1)
**As an** AI agent on a system with only 1 CLI installed, **I want** skill workflows to
adapt their recommendations, **so that** I don't try to run consensus with 1 CLI.

**Acceptance Criteria:**
- [ ] With 1 CLI: consensus/debate sections hidden, replaced with think(peer_review)
- [ ] With 2+ CLIs: consensus sections shown
- [ ] With 3+ CLIs: debate sections shown with recommended max_turns
- [ ] Actual CLI names shown (not generic "a CLI")

### US4: User Adds Custom Skill Without Go Changes (P1)
**As a** user, **I want** to create a `config/skills.d/my-workflow.md` file and have it
appear as an MCP prompt, **so that** I can extend aimux with domain-specific workflows.

**Acceptance Criteria:**
- [ ] .md file with valid frontmatter auto-registers as MCP prompt on restart
- [ ] Template variables ({{.EnabledCLIs}}, etc.) work in user skills
- [ ] Invalid templates produce warning log, not crash
- [ ] User skill with same slug as built-in overrides built-in

### ~~US5: Workflow Tool Loop Safety~~ — DEFERRED
Moved to separate PR. Workflow skill template includes escalation text hints per P20
(interconnection) but WTF-score engine logic is out of scope for this feature.

## Edge Cases

- Skill template has syntax error → logged as warning, skill excluded, server starts normally
- Related skill references non-existent skill → warning in log, reference omitted from output
- No CLIs enabled → all skill templates degrade gracefully (show "no CLIs" message, not empty sections)
- Skill file changes on disk while server running → not hot-reloaded (by design — restart or SIGHUP)
- Two skills with same slug → last-loaded wins (user skills load after built-in)
- Template renders to >50KB → truncated with "Full workflow available at: config/skills.d/{name}.md"

## Out of Scope

- **Hot reloading** — restart or SIGHUP is sufficient for skill changes
- **Skill marketplace / remote fetch** — skills are local files only
- **Tool execution from skills** — skills generate INSTRUCTIONS, not execute tools
- **Workflow loop safety (WTF-score)** — separate PR, not blocking skill engine

## Dependencies

- `text/template` — Go standard library (no external deps)
- `embed` — Go standard library for binary embedding
- Existing `pkg/prompt/` pattern for config file loading
- Existing `config/cli.d/` pattern for config directory structure

## Success Criteria

- [ ] At least 10 skills loaded from config/skills.d/ on startup
- [ ] All 10 current MCP prompts migrated to skill templates
- [ ] go build / go test pass with zero regressions
- [ ] Real test: agent calls aimux-debug, receives 100+ line connected workflow, successfully follows it
- [ ] server.go prompt handlers reduced from ~600 lines to ~100 lines (thin renderers)

## Glossary

| Term | Definition | Example |
|------|-----------|---------|
| **Skill** | The .md template file in config/skills.d/ — source artifact | `config/skills.d/debug.md` |
| **Prompt** | MCP protocol object registered on server — delivery mechanism | `aimux-debug` MCP prompt |
| **Workflow** | Multi-phase content inside a skill — the instructions agent follows | "5-phase debug workflow with hard gates" |
| **Command** | User-invocable entry point via slash syntax | `/aimux:investigate` |
| **Tool** | aimux MCP tool that performs actions | `exec`, `think`, `investigate`, `consensus` |

A skill *contains* a workflow, *is delivered as* a prompt, *may be invoked via* a command, and *orchestrates* tools.

## Clarifications

### Session 2026-04-08

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Domain/Data | Frontmatter schema? | Required: name, description. Optional: prompt (bool), args (list), related (list), tags (list) | 2026-04-08 |
| C2 | Data Lifecycle | Embed vs disk priority? | Disk overrides embedded. Load: embedded first, disk overlays. Same slug = disk wins | 2026-04-08 |
| C3 | Reliability | Missing template data? | missingkey=zero + recover(). Degraded output > crash | 2026-04-08 |
| C4 | Security | Built-in override risk? | Overridable with WARN log. User is admin, local system | 2026-04-08 |
| C5 | Terminology | Skill vs prompt vs workflow? | 5-term glossary: skill, prompt, workflow, command, tool | 2026-04-08 |

## Resolved Questions

1. **Includes** — YES. Skills support `{{template "fragment-name" .}}` for shared fragments.
   Fragments live in `config/skills.d/_fragments/` (underscore prefix = not registered as prompt).
   Go `text/template` supports this natively via `ParseGlob`. Shared fragments: evidence-table,
   verification-gate, delegation-tree, priority-scoring, integrity-commandments.
   Rationale: DRY. Evidence table appears in audit, review, debug. Copy-paste = drift.

2. **Bidirectional links** — YES. Engine computes reverse graph from `related:` frontmatter.
   If debug declares `related: [security]`, security auto-gets debug in its "See also" section.
   Implementation: on Load(), build adjacency map, merge forward + reverse into each skill's
   RelatedSkills field. No manual maintenance.
   Rationale: One-directional links break when skills grow. Auto-bidirectional keeps graph consistent.

3. **Think tool schema fix** — IN THIS PR. Add `criteria`, `options`, `components`, `sources`,
   `findings` as JSON-string parameters to MCP schema. Patterns parse JSON internally.
   This unblocks 5 of 23 patterns currently unusable via MCP.

## Open Questions

None.
