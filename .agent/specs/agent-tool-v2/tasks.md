# Tasks: Agent Tool v2

**Spec:** .agent/specs/agent-tool-v2/spec.md
**Generated:** 2026-04-07

## Phase 1: Agent Definition Enhancement

- [x] T001 Update Agent struct in pkg/agents/registry.go — add Role, Model, Effort, Timeout fields with yaml/json tags
- [x] T002 Update frontmatter parser in pkg/agents/registry.go — extract role, model, effort, timeout from agent .md frontmatter
- [x] T003 [P] Tests: parse agent with new fields, parse agent without new fields (backward compat)

## Phase 2: Smart Prompt + CLI Resolution

- [x] T004 Rewrite buildSystemPrompt in pkg/agents/runner.go — use agent.Content as system prompt body, append task section with cwd context
- [x] T005 Update RunAgent to use role-based CLI resolution — if cfg.CLI empty: check agent.Role → Router.Resolve, then agent.Meta["cli"], then default
- [x] T006 Pass agent.Model and agent.Effort through to resolveArgs — merge with CLI profile flags
- [x] T007 Set default MaxTurns to 1 (single autonomous run) — document multi-turn as opt-in

## Phase 3: Server Wiring + MCP Fix

- [x] T008 Update handleAgentRun in server.go — pass role/model/effort from agent definition to RunConfig, use Router for CLI resolution
- [x] T009 Fix MCP enum — add 6 research patterns + agent-specific params (artifact, claim, hypothesis, sources, findings)
- [x] T010 [P] Tests: agent run with role routing, agent run with model passthrough

## Phase 4: Polish

- [x] T011 Full regression
- [x] T012 Update CONTINUITY.md
