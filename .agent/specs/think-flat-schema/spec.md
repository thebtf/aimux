# Feature: Think Patterns Flat Schema + Step Progression

**Slug:** think-flat-schema
**Created:** 2026-04-09
**Status:** Draft
**Author:** AI Agent (reviewed by user)

> **Provenance:** Specified by claude-opus-4-6 on 2026-04-09.
> Evidence from: MCP smoke tests (nested params not passable), pal-mcp-server
> architecture (flat fields + step progression proven in production),
> thinking-patterns original (Zod schemas with nested objects).
> Confidence: VERIFIED (smoke test failure on hypothesis param).

## Overview

Redesign 5 stateful think patterns from nested-object params to flat MCP-compatible
fields with pal-mcp-server-style step progression. Agent interacts through multiple
calls with flat params (hypothesis_text, confidence, step_number), and the pattern
manages state progression with evidence gates and forced reflection pauses.

## Context

**Problem:** 5 stateful patterns expect nested object params that MCP can't express:
- `debugging_approach`: `hypothesis: {id, text, confidence}`, `hypothesisUpdate: {id, status}`
- `scientific_method`: `entry: {type, text, linkedTo}`
- `collaborative_reasoning`: `contribution: {type, text, persona, confidence}`
- `structured_argumentation`: `argument: {type, text, supportsClaimId}`
- `sequential_thinking`: partially flat (thought, branchId) but `revisesThought` needs context

Agent sees `hypothesis: object` in schema with no clue what fields to put inside.
Result: patterns unusable via MCP for their core stateful features.

**Solution proven by pal-mcp-server:** Flat fields + step_number + forced pauses.
Agent fills `hypothesis_text: string` + `confidence: enum` + `step_number: int`.
Pattern tracks state internally, returns "STOP: collect more evidence" between steps.

**What pal-mcp-server debug tool uses (flat, working):**
```
step: string           // what agent did this step
step_number: int       // 1, 2, 3...
total_steps: int       // estimated total
next_step_required: bool // continue or done
findings: string       // discoveries so far
files_checked: string  // examined files
relevant_files: string // key files
hypothesis: string     // root cause theory (flat text!)
confidence: enum       // exploring/low/medium/high/certain
```

## Functional Requirements

### FR-1: Flat Params for debugging_approach
Replace nested `hypothesis: {id, text, confidence}` and `hypothesisUpdate: {id, status}`
with flat MCP params:
- `hypothesis_text: string` — "Concrete root cause theory based on evidence"
- `confidence: string` — enum: exploring/low/medium/high/very_high/certain
- `hypothesis_action: string` — enum: propose/confirm/refute (replaces hypothesisUpdate)
- `findings_text: string` — evidence collected this step
- `step_number: int` — current investigation step
- `next_step_needed: bool` — agent plans to continue

Pattern internally: auto-generates hypothesis IDs, tracks step history in session,
applies evidence gates, returns forced reflection when needed.

### FR-2: Flat Params for scientific_method
Replace nested `entry: {type, text, linkedTo}` with:
- `entry_type: string` — enum: observation/hypothesis/prediction/experiment/analysis/conclusion
- `entry_text: string` — content of the entry
- `link_to: string` — ID of entry this links to (optional, pattern auto-links by type sequence)
- `step_number: int`
- `next_step_needed: bool`

Pattern internally: auto-generates entry IDs, enforces lifecycle chain
(hypothesis→prediction→experiment), returns STOP if lifecycle violated.

### FR-3: Flat Params for collaborative_reasoning
Replace nested `contribution: {type, text, persona, confidence}` with:
- `contribution_type: string` — enum: observation/question/insight/concern/suggestion/challenge/synthesis
- `contribution_text: string`
- `persona_id: string` — who is contributing
- `contribution_confidence: number` — 0-1

### FR-4: Flat Params for structured_argumentation
Replace nested `argument: {type, text, supportsClaimId}` with:
- `argument_type: string` — enum: claim/evidence/rebuttal
- `argument_text: string`
- `supports_claim_id: string` — which claim this evidence/rebuttal targets

### FR-5: MCP Schema Update
Add all new flat params to think tool registration in server.go.
Remove or deprecate nested object forwarding for replaced params.
Keep backward compat: if old nested format received, parse it (transition period).

### FR-6: Step Progression Protocol
For debugging_approach and scientific_method (the most complex workflows):
- Track step_number in session state
- Return `next_steps` guidance with each response (what to do before calling again)
- Evidence gate: if step_number > 1 and no findings_text provided → return STOP directive
- Confidence gate: if confidence="certain" but step_number < 3 → return VERIFY warning

### FR-7: Instructional Field Descriptions
Every new flat param has an instructional description in MCP schema:
- `hypothesis_text`: "Your root cause theory. Be specific: what exactly is broken and why. Base on evidence, not assumptions."
- `confidence`: "Your confidence level: exploring (just started), low (early idea), medium (some evidence), high (strong evidence), certain (confirmed with test)."
- `findings_text`: "What you discovered this step. Include: file paths, line numbers, error messages, observations."

## Non-Functional Requirements

### NFR-1: Backward Compatibility
Old nested format `hypothesis: {id: "h1", text: "..."}` still accepted during
transition period. Pattern detects format and handles both. New flat format preferred.

### NFR-2: Zero Breaking Changes for Non-Stateful Patterns
18 stateless/simple patterns unchanged. Only 5 stateful patterns modified.

### NFR-3: Session State Unchanged
Internal session storage format unchanged. Flat params converted to internal
representation on input, internal state converted to flat response on output.

## User Stories

### US1: Agent Debugs via Step Progression (P0)
**As an** AI agent debugging a bug, **I want** to call debugging_approach
step-by-step with flat params (hypothesis_text, confidence, step_number),
**so that** I can follow a structured investigation without knowing nested JSON schemas.

**Acceptance Criteria:**
- [ ] Step 1: issue only → returns guidance "investigate before hypothesizing"
- [ ] Step 2: issue + findings_text → returns "ready for hypothesis"
- [ ] Step 3: hypothesis_text + confidence="medium" → accepted, tracked
- [ ] Step 4: hypothesis_text + confidence="certain" + step_number=2 → VERIFY warning
- [ ] All via flat MCP params (no nested objects)

### US2: Agent Uses Scientific Method via Flat Entries (P0)
**As an** AI agent following scientific method, **I want** to submit entries
as entry_type + entry_text with auto-linking,
**so that** I don't need to manually manage entry IDs and linkedTo references.

**Acceptance Criteria:**
- [ ] entry_type="hypothesis", entry_text="..." → auto-assigned ID, stored in session
- [ ] entry_type="prediction", entry_text="..." → auto-linked to last hypothesis
- [ ] entry_type="prediction" without prior hypothesis → STOP directive
- [ ] All via flat MCP params

### US3: Agent Contributes to Collaborative Reasoning (P1)
**As an** AI agent, **I want** to add contributions via flat params
(contribution_type, contribution_text, persona_id),
**so that** I can participate without constructing nested JSON.

**Acceptance Criteria:**
- [ ] contribution_type="insight" + contribution_text + persona_id → tracked in session
- [ ] participation balance computed from flat contributions
- [ ] All via flat MCP params

## Edge Cases

- Old nested format and new flat format sent simultaneously → flat takes precedence
- step_number sent without session_id → new session auto-created
- confidence sent as float 0.7 instead of enum → map to nearest enum level
- Empty hypothesis_text → validation error (not silent accept)

## Out of Scope

- Changing think tool into multiple MCP tools (stay as single tool + pattern routing)
- Modifying stateless patterns
- Redis/SQLite session backend changes

## Dependencies

- PR #28 merged (intelligence tiers) ✅
- Existing session management in pkg/think/session.go
- MCP tool registration in pkg/server/server.go

## Success Criteria

- [ ] All 5 stateful patterns usable via flat MCP params
- [ ] Smoke test: debugging_approach 3-step investigation via aimux-dev
- [ ] Smoke test: scientific_method hypothesis→prediction→experiment via flat params
- [ ] All existing tests pass (backward compat)
- [ ] Evidence gates and forced reflection work with new params

## Clarifications

### Session 2026-04-09

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Data Model | Confidence enum type? | Go string constants, not custom type. MCP schema = string with enum in description. | 2026-04-09 |
| C2 | Data Lifecycle | Backward compat duration? | Permanent dual support. Type-switch detects format (map vs string). No removal date. | 2026-04-09 |
| C3 | Integration | handleThink forwarding? | New flat params → optionalStrings. Old nested → forwardKeys. Both forwarded. Pattern detects. | 2026-04-09 |

## Resolved Questions

None remaining.
