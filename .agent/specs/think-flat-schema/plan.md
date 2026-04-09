# Implementation Plan: Think Patterns Flat Schema

**Spec:** .agent/specs/think-flat-schema/spec.md
**Created:** 2026-04-09
**Status:** Draft

> **Provenance:** Planned by claude-opus-4-6 on 2026-04-09.
> Evidence from: spec.md, pal-mcp-server debug tool, existing server.go forwarding logic.

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Flat params | Go strings + mcp.WithString | MCP-native, agent-visible |
| Confidence enum | Go string constants | Matches pal-mcp-server approach |
| State management | Existing pkg/think/session.go | No changes needed |

## Architecture

```
pkg/server/server.go
  └── handleThink()
      ├── optionalStrings: ADD hypothesis_text, confidence, findings_text,
      │   entry_type, entry_text, link_to, contribution_type, contribution_text,
      │   persona_id, argument_type, argument_text, supports_claim_id,
      │   hypothesis_action, step_number, next_step_needed
      └── forwardKeys: KEEP existing nested params for backward compat

pkg/think/patterns/
  ├── debugging_approach.go  — MODIFY Validate+Handle for flat params
  ├── scientific_method.go   — MODIFY Validate+Handle for flat params
  ├── collaborative_reasoning.go — MODIFY Validate+Handle for flat params
  ├── structured_argumentation.go — MODIFY Validate+Handle for flat params
  └── sequential_thinking.go — minimal changes (already mostly flat)
```

## Phases

### Phase 1: Schema + Server (FR-5, FR-7)
Add flat params to MCP schema. Update handleThink forwarding.

### Phase 2: debugging_approach + scientific_method (FR-1, FR-2, FR-6)
Redesign the two most complex stateful patterns with step progression.

### Phase 3: collaborative + argumentation + sequential (FR-3, FR-4)
Simpler patterns — flat params without step progression.

### Phase 4: Smoke Test
Test all 5 patterns via aimux-dev MCP with flat params.
