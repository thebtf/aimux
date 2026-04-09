# Feature: Agent Tool v2 — CLI-Native Agent Architecture

**Slug:** agent-tool-v2
**Created:** 2026-04-07
**Status:** Draft
**Source:** claude-code AgentTool analysis (SocratiCode semantic mapping of 2148 files)

## Core Insight

> "claude-code launches its subprocess, aimux launches CLI — that's the only difference."

But the implications are deep:
- claude-code agent = forked LLM session with own tools, context, memory
- aimux agent = CLI in autonomous mode (codex --full-auto, claude -p, gemini -y)
- The CLI IS the agent. aimux orchestrates WHEN and HOW it runs, not WHAT it does.

## What claude-code Agent() Actually Does (from source analysis)

runAgent.ts (39 imports, 7 dependents):
1. **Select agent definition** — from built-in registry or .claude/agents/*.md
2. **Build system prompt** — agent-specific, with env details
3. **Resolve tools** — subset per agent definition (tools: ["Read", "Write", ...])
4. **Connect agent MCP servers** — additive to parent's
5. **Create isolated context** — own messages[], file state cache, transcript
6. **Run query() loop** — multi-turn with the SAME LLM until done
7. **Track progress** — async with task notifications
8. **Store transcript** — for resume
9. **Cleanup** — kill shell tasks, close MCP connections

## What aimux Agent Should Do (CLI-native equivalent)

| claude-code | aimux equivalent | Notes |
|---|---|---|
| Select agent definition | Load from .agent/ registry | Already works |
| Build system prompt | Inject agent.Content before user prompt | Already works (v1) |
| Resolve tools | **N/A** — CLI has its own tools | Not applicable |
| Connect MCP servers | **N/A** — CLI has its own MCP config | Not applicable |
| Create isolated context | **CWD isolation** + session tracking | CWD works, session needs enhancement |
| Run query() loop | **Single exec call** — CLI runs its own loop | The fundamental difference |
| Track progress | **Job system** with async + status polling | Already works |
| Store transcript | **Session persistence** — store prompt + response | Partially works |
| Cleanup | **Process kill** on cancel | Works via CancelJob |

## What's MISSING in current v1 agent

### M1: Agent Definition Enhancement
Current `Agent` struct has: Name, Description, Content, Source, Tools, MaxTurns, Meta.
Missing from claude-code AgentDefinition that ARE relevant for CLI agents:
- `model` — which model to use (passed to CLI via -m/--model flag)
- `role` — which CLI to route to (coding → codex, review → gemini)
- `effort` — reasoning effort (low/medium/high)
- `cwd` — working directory override
- `timeout` — per-agent timeout

### M2: Smart Prompt Engineering
Current v1: "You are {name}. {description}. {content}. TASK_COMPLETE. Task: {prompt}"
This is naive. claude-code builds env-aware system prompts with:
- Project context (git branch, modified files)
- Available tools listing
- Completion criteria
- Output format guidance

For aimux: inject the agent markdown content AS the system prompt, with task appended.
The agent .md file IS the system prompt — same as claude-code loads frontmatter + body.

### M3: Role-Based CLI Selection
Current v1: CLI hardcoded or from param.
Should: read `role` from agent definition → route via Router.Resolve().
Agent definition says `role: codereview` → Router picks gemini → gemini -y -p "prompt".

### M4: Model/Effort Passthrough
Agent definition can specify `model: gpt-5.4` or `effort: high`.
These should be passed through to CLI via profile flags.

### M5: MCP Enum Fix
6 research patterns missing from MCP tool enum. Bug identified, fix ready.

## Functional Requirements

### FR-1: Enhanced Agent Definition
Update `pkg/agents/registry.go` Agent struct:
```go
type Agent struct {
    Name        string            `json:"name"`
    Description string            `json:"description,omitempty"`
    Role        string            `json:"role,omitempty"`       // NEW: routing role
    Model       string            `json:"model,omitempty"`      // NEW: model override
    Effort      string            `json:"effort,omitempty"`     // NEW: reasoning effort
    Domain      string            `json:"domain,omitempty"`
    Source      string            `json:"source"`
    Content     string            `json:"content,omitempty"`
    Tools       []string          `json:"tools,omitempty"`
    MaxTurns    int               `json:"max_turns,omitempty"`
    Timeout     int               `json:"timeout,omitempty"`    // NEW: agent-specific timeout
    Meta        map[string]string `json:"meta,omitempty"`
}
```

Parse new fields from agent .md frontmatter:
```yaml
---
name: code-reviewer
description: Expert code review agent
role: codereview
model: gpt-5.4
effort: high
timeout: 600
---
```

### FR-2: Smart Prompt Construction
Replace naive string concatenation with structured prompt:
```
{agent.Content}

## Task
{user prompt}

## Context
Working directory: {cwd}
```

If agent.Content is empty, fall back to: "You are {name}. {description}."
Agent .md body IS the system prompt — don't wrap it in meta-text.

### FR-3: Role-Based CLI Resolution in Agent Tool
When CLI not explicitly specified:
1. Check agent.Role → Router.Resolve(role) → get CLI + model + effort
2. Check agent.Meta["cli"] → direct CLI name
3. Fallback to default CLI from config

### FR-4: Model/Effort Passthrough
Pass agent.Model and agent.Effort through to CLI via profile flags.
In `resolveArgs`: merge agent model/effort with profile flags.

### FR-5: Single-Turn Architecture (remove multi-turn loop)
For CLIs with autonomous mode (codex, claude, gemini, qwen, cline, continue, goose, crush):
- Single exec call IS a full agent run
- CLI reads files, runs commands, edits code ITSELF
- No need for multi-turn loop — remove it

Multi-turn reserved for future workflow tool integration.
Keep RunAgent function but default MaxTurns to 1.

### FR-6: MCP Schema Fix
Add 6 research patterns to think tool enum.
Add agent-specific params: artifact, claim, hypothesis, sources, findings.

## Non-Functional Requirements

### NFR-1: Backward Compatibility
- Existing agent .md files work without new fields (all new fields optional)
- Existing `agents list/run/info/find` actions unchanged
- MCP tool name stays `agent` (was renamed from `agent_run`)

### NFR-2: No New Dependencies
Pure Go stdlib.

## Success Criteria

- [ ] Agent struct has role, model, effort, timeout fields
- [ ] Frontmatter parser extracts new fields
- [ ] Prompt construction uses agent.Content as system prompt
- [ ] CLI resolution via role routing
- [ ] Model/effort passthrough to CLI flags
- [ ] Default MaxTurns = 1 (single autonomous run)
- [ ] MCP enum includes all 23 patterns
- [ ] All existing tests pass
