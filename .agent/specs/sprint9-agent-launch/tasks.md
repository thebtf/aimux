# Tasks: Agent Launch via CLI (Sprint 9)

**Generated:** 2026-04-07

## Phase 1: Agent Runner

- [x] T001 Create `pkg/agents/runner.go` — AgentRunner struct with: Run(ctx, agentDef, cli, prompt, cwd) that executes multi-turn agent session. Injects agent system prompt before user prompt. Tracks turns. Detects completion (agent says "done" or max turns reached). Returns aggregated content.
- [x] T002 Add `agent_run` MCP tool to server.go — params: agent (name), prompt (task), cli (override), max_turns (default 10), async (bool). Resolves agent from registry, creates session, runs AgentRunner.
- [x] T003 [P] Tests for AgentRunner: single-turn completion, multi-turn, max turns limit, agent not found error

---

## Phase 2: Agent Prompt Engineering

- [x] T004 Update agent system prompt injection — prepend agent definition content as system context before user prompt. Format: "You are {agent.name}. {agent.content}\n\nTask: {prompt}"
- [x] T005 Add completion detection — scan response for completion signals: "TASK COMPLETE", "DONE", empty response after non-empty. Configurable via agent definition.

---

## Phase 3: Polish

- [x] T006 Full regression
- [x] T007 Update CONTINUITY.md

## Dependencies
- T001 blocks T002-T005
