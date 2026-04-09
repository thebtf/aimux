# Roadmap: aimux v3 — Phase 2

## Sprint 7: Investigate Enhancements
**Status:** done
**Goal:** Persona auto-selection by topic (map investigation topic to domain expert persona). Cross-tool invocation (investigate assess → auto-dispatch think/exec as sub-investigation). Deeper integration between investigate and think tools.
**Depends:** none
**Acceptance:** investigate auto-selects domain based on topic keywords, assess auto-dispatches suggested_think_call, cross-tool results fed back into investigation state

## Sprint 8: Feynman Research Patterns
**Status:** done
**Goal:** Port research-oriented thinking patterns from D:\Dev\_EXTRAS_\feynman — deep research, literature review, source comparison, peer review, replication. Integrate as new think patterns and/or investigate domains.
**Depends:** Sprint 7
**Acceptance:** New think patterns registered, investigate has research domain with appropriate coverage areas

## Sprint 9: Agent Launch via CLI
**Status:** done
**Goal:** Port agent() launch pattern from D:\Dev\_EXTRAS_\claude-code — ability to spawn autonomous agent sessions through any CLI as driver. Agent = long-running task with own context, tools, and completion criteria.
**Depends:** none
**Acceptance:** New MCP tool or exec enhancement that launches agents with any CLI, tracks progress, returns results

## Sprint 10: Workflow Tool
**Status:** done
**Goal:** Declarative multi-step execution chains — "analyze → codereview → fix" as single MCP call. Template interpolation, conditional steps, error handling per step. Built-in workflow presets.
**Depends:** none
**Acceptance:** workflow MCP tool registered, step execution with templates, conditional branching, YAML presets

## Sprint 11: CLI Auto-Fallback
**Status:** done
**Goal:** When primary CLI fails (rate limit, timeout, auth), auto-retry with next capable CLI. Capability tags in profiles. Integration with circuit breakers and turn validator.
**Depends:** none
**Acceptance:** Transparent fallback on CLI failure, capability-aware routing, max 2 retries

## Sprint 12: Session Persistence + Deepresearch Integration
**Status:** done
**Goal:** Dialog/consensus/debate sessions resumable after restart. Investigate recall searches deepresearch cache. Think can request deepresearch context.
**Depends:** Sprint 7
**Acceptance:** Orchestrator sessions survive restart via SQLite, cross-tool search between investigate and deepresearch
