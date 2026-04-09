# Feature: Workflow Tool + CLI Auto-Fallback

**Slug:** sprint7-workflow-fallback
**Created:** 2026-04-07
**Status:** Draft

## Overview

Two high-impact features that transform aimux from a tool collection into a production orchestration engine:

1. **Workflow Tool** — declarative multi-step execution chains with conditions, branching, and aggregation. Agents define "analyze → codereview → fix" pipelines as a single MCP call instead of 3 manual calls.

2. **CLI Auto-Fallback** — when a CLI fails (rate limit, timeout, auth error), automatically retry with the next best CLI from the role's candidates. Makes aimux resilient to individual CLI failures.

## Context

### Workflow — Current State
No workflow tool exists. Agents must manually chain exec calls:
```
exec(role="analyze") → read result → exec(role="codereview") → read result → exec(role="coding")
```
This is 3 MCP round-trips, and the agent must handle errors/branching at each step.

V2 had a basic workflow module (`src/workflow/`) but it was only 1 file and rarely used.

### CLI Auto-Fallback — Current State
- `pkg/routing/routing.go` resolves role → single CLI. If that CLI fails, the call fails.
- `pkg/executor/breaker.go` has circuit breakers per CLI (open after N failures).
- `BreakerRegistry.AvailableCLIs()` filters by breaker state.
- But: the **server handler** does NOT retry with another CLI when the primary fails.
- Turn validator catches rate limits and auth errors but just returns the error.

### What We Have to Build On
- Role routing already maps roles to CLIs
- Circuit breakers already track CLI health
- Turn validator already classifies errors
- Multiple CLIs often share capabilities (codex, claude, gemini all do codereview)

## Functional Requirements

### FR-1: Workflow Definition
A workflow is a sequence of steps, each step being an aimux tool call:

```json
{
  "name": "review-and-fix",
  "steps": [
    {"id": "analyze", "tool": "exec", "params": {"role": "analyze", "prompt": "{{input}}"}},
    {"id": "review", "tool": "exec", "params": {"role": "codereview", "prompt": "Review this: {{analyze.content}}"}},
    {"id": "fix", "tool": "exec", "params": {"role": "coding", "prompt": "Fix issues: {{review.content}}"}, "condition": "{{review.content contains 'FINDING'}}"}
  ]
}
```

Key features:
- **Template interpolation**: `{{input}}` = workflow input, `{{step_id.content}}` = previous step output
- **Conditional steps**: `condition` field — step skipped if condition is false
- **Error handling**: `on_error: "skip" | "stop" | "retry"` per step (default: stop)

### FR-2: Workflow Execution Engine
New `pkg/orchestrator/workflow.go`:
- `WorkflowStrategy` implementing `types.Strategy`
- Sequential execution: each step waits for previous
- Template interpolation from step results
- Condition evaluation (simple string contains/equals/empty checks)
- Aggregated result: all step outputs combined

### FR-3: Workflow MCP Tool
New `workflow` tool in server.go (12th MCP tool):
- `name` — workflow name (for logging/recall)
- `steps` — JSON array of step definitions
- `input` — initial prompt/data
- `async` — background execution
Returns: aggregated result with per-step content and status.

### FR-4: Built-in Workflow Presets
Pre-defined workflows in `config/workflows.d/`:
- `review-and-fix.yaml` — analyze → codereview → fix findings
- `deep-debug.yaml` — investigate start → think(debugging_approach) → exec(debug)
- `security-audit.yaml` — exec(secaudit) → investigate findings → report

### FR-5: CLI Auto-Fallback in Role Routing
Update `pkg/routing/routing.go`:
- `ResolveWithFallback(role) []RolePreference` — returns ordered list of candidates
- Primary: configured CLI for role
- Fallback 1: other enabled CLIs with same capability
- Fallback 2: any enabled CLI (last resort)

### FR-6: Retry with Fallback in Server Handler
Update `pkg/server/server.go:executeJob`:
- On CLI failure (rate limit, timeout, auth error from turn validator):
  1. Record failure in circuit breaker
  2. Get next fallback CLI from routing
  3. Retry with fallback CLI
  4. Max 2 fallback attempts
  5. Log: "CLI X failed, falling back to Y"

### FR-7: CLI Capability Tags
Add `capabilities` field to CLI profiles:
```yaml
capabilities: [coding, review, analysis, debugging]
```
This enables intelligent fallback — when gemini fails for `codereview`, fall back to claude (which also has `review` capability), not to aider (which is `coding` only).

## Non-Functional Requirements

### NFR-1: Backward Compatibility
- All existing tools unchanged
- workflow is a new 12th tool
- Fallback is transparent — callers don't see it (just get a successful response from a different CLI)

### NFR-2: Zero Dependencies
Pure Go stdlib.

### NFR-3: Performance
- Fallback adds one retry latency, not blocking
- Workflow steps are sequential (parallel steps = future work)

## Edge Cases

- Workflow with 0 steps → error
- Step references nonexistent previous step → error
- All fallback CLIs fail → return error from last attempt
- Circular workflow steps → detected by ID uniqueness check
- Condition syntax error → treat as true (fail-open)
- Template references nonexistent step → empty string substitution

## Success Criteria

- [ ] Workflow tool registered as 12th MCP tool
- [ ] Step execution with template interpolation
- [ ] Conditional steps (skip when condition false)
- [ ] Built-in workflow presets loadable from YAML
- [ ] CLI auto-fallback on rate limit / timeout / auth error
- [ ] Capability-aware fallback ordering
- [ ] Circuit breaker integration (broken CLIs skipped)
- [ ] All existing tests pass
- [ ] New unit tests for workflow engine + fallback routing
