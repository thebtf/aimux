---
name: guide
description: "Complete guide to aimux tools, roles, and patterns"
related: [background, delegate]
---
# aimux — Tool Reference Guide

## Live Status
- **CLIs ({{.CLICount}}):** {{JoinCLIs}}
- **Total Requests:** {{.TotalRequests}}
- **Error Rate:** {{.ErrorRate}}

---

## Tool Selection

| Tool | When to use | Key params |
|------|-------------|------------|
| `exec` | Run a prompt on a specific role or CLI | `prompt`, `role`, `cli`, `async` |
| `consensus` | Get agreement from multiple models on a factual or risk question | `topic`, `synthesize` |
| `debate` | Have models argue opposing positions on a decision | `topic`, `max_turns` |
| `dialog` | Multi-turn discussion between CLIs | `prompt`, `max_turns` |
| `think` | Structured single-model reasoning (23 patterns) | `pattern`, `topic`, `artifact` |
| `investigate` | Deep investigation with session tracking and convergence | `action`, `topic`, `domain` |
| `audit` | Full codebase audit across security, quality, and design | `cwd`, `mode` |
| `agent` | Execute a named project agent | `agent`, `prompt` |
| `workflow` | Chain multiple tool calls declaratively | `steps`, `input` |
| `status` | Check async job status | `job_id` |
| `sessions` | List, health-check, gc, or cancel sessions | `action` |
| `agents` | Discover available named agents | `action` |
| `deepresearch` | Deep research via Gemini long-context | `topic` |

---

## Using the agents Tool

The `agents` tool discovers named agents and runs them. Use it instead of `exec` when a
task maps cleanly to a specialist role (researcher, reviewer, debugger, implementer, or a
project-defined agent).

**Discovery flow:**

1. Find a matching agent by keyword:
   ```
   agents(action="find", prompt="review code security")
   ```
   Returns a list of candidates with `name`, `role`, and `when` guidance.

2. Run the chosen agent:
   ```
   agents(action="run", agent="reviewer", prompt="Review pkg/auth for security issues")
   ```
   If you omit `agent=`, aimux returns a candidate list — pick one and call again with
   `agent=` set.

3. List all available agents (project + user + builtin):
   ```
   agents(action="list")
   ```

**When to prefer `agents` over `exec`:**
- You need a specialist with a predefined persona and constraints (e.g., "reviewer" always
  provides file:line references; "implementer" always writes tests).
- The project defines custom agents in `.aimux/agents/`, `.claude/agents/`, `.codex/agents/`,
  or `.claw/agents/` — these are surfaced only via `agents`, not `exec`.
- Use `exec(role=...)` as the fallback when no named agent matches the task.

---

## Available CLIs

{{range .EnabledCLIs}}- **{{.Name}}** — {{.Description}} (roles: {{range .Roles}}`{{.}}` {{end}})
{{end}}

---

## Role Routing

Do NOT pick a CLI manually unless you have a specific reason. Use `role=` and let aimux route:

| Role | Routes to | Use for |
|------|-----------|---------|
| `coding` | {{RoleFor "coding"}} | Implementation, refactoring, test generation |
| `codereview` | {{RoleFor "codereview"}} | Code review, quality analysis |
| `debug` | {{RoleFor "debug"}} | Bug investigation, error analysis |
| `secaudit` | {{RoleFor "secaudit"}} | Security audit, CVE analysis |
| `analyze` | {{RoleFor "analyze"}} | Architecture analysis, broad codebase scan |
| `refactor` | {{RoleFor "refactor"}} | Safe refactoring, rename, extract |
| `testgen` | {{RoleFor "testgen"}} | Test generation, coverage improvement |
| `planner` | {{RoleFor "planner"}} | Task breakdown, spec writing |
| `thinkdeep` | {{RoleFor "thinkdeep"}} | Deep reasoning, architectural decisions |

If a CLI fails (rate limit, timeout), aimux auto-retries with the next capable CLI.

---

## Think Patterns (23)

Use `think(pattern="...", ...)` for structured reasoning without running a full CLI session.

{{range .ThinkPatterns}}- `{{.}}`
{{end}}

Quick picks:
- Exploring a problem → `think(pattern="think")`
- Challenging an assumption → `think(pattern="critical_thinking")`
- Reviewing code → `think(pattern="peer_review")`
- Synthesizing findings → `think(pattern="research_synthesis")`
- Literature review → `think(pattern="literature_review")`
- Comparing sources → `think(pattern="source_comparison")`

---

## Reasoning Tool Decision Table

Choose the right tool before committing to an approach:

| Need | Tool | Why |
|------|------|-----|
| Scratchpad / chain-of-thought | `think` | Structural, local, zero tokens spent on LLM calls |
| Find cognitive bias in your own reasoning | `think(pattern="critical_thinking")` | Structural trigger-phrase scan |
| Security / design / spec review of an artifact | `critique(lens="security"\|"api-design"\|"spec-compliance"\|"adversarial")` | Calls a real LLM; produces structured findings |
| Devil's advocate stress-test | `critique(lens="adversarial")` | Single LLM pass; cheaper than full debate |
| Get ground truth from multiple models | `consensus` | Parallel quorum; use when you need corroboration |
| Explore two opposing positions | `debate` | Multi-turn adversarial dialogue |
| Multi-turn collaborative dialogue | `dialog` | Sequential back-and-forth; lower overhead than debate |
| Deep investigation with finding chain | `investigate` | Session-based; tracks findings, drives to convergence |
| Single prompt → one model | `exec` | Lightweight; no orchestration overhead |

**Rule of thumb:**
- No LLM call needed → `think` (any of 23 structural patterns)
- LLM call, single artifact → `critique`
- LLM call, factual quorum → `consensus`
- LLM call, adversarial → `debate`
- LLM call, multi-turn → `dialog`
- LLM call, deep session → `investigate`
- LLM call, ad-hoc → `exec`

---

## Investigation Flow

The `investigate` tool tracks findings across a session and drives toward convergence:

```
1. investigate(action="start",   topic="...", domain="auto")         → session_id
2. investigate(action="finding", session_id="...", description="...",
               source="file:line", severity="P1", confidence="VERIFIED")
3. investigate(action="assess",  session_id="...")                   → CONTINUE | COMPLETE
4. investigate(action="report",  session_id="...", cwd="...")        → report file
5. investigate(action="recall",  topic="...")                        → past reports
```

Domains: `debugging`, `security`, `performance`, `testing`, `database`, `infrastructure`, `general`

---

## Workflow Example

Chain tools declaratively — avoid manual sequencing:

```
workflow(steps=[
  {tool: "think",       params: {pattern: "literature_review", topic: "{{"{{input.topic}}"}}"  }},
  {tool: "investigate", params: {action: "start",              topic: "{{"{{input.topic}}"}}", domain: "auto"}},
  {tool: "consensus",   params: {topic: "{{"{{input.topic}}"}}", synthesize: true}},
  {tool: "think",       params: {pattern: "research_synthesis", topic: "{{"{{input.topic}}"}}"  }},
], input={topic: "..."})
```

---

## Anti-Patterns

| Anti-pattern | Correct approach |
|---|---|
| Picking CLI by name without reason | Use `role=` — let routing handle it |
| Running consensus on a factual lookup | Use `exec(role="analyze")` — faster, cheaper |
| Starting investigation without `recall` | Always check past reports first |
| Trusting consensus output without critic pass | Run `think(pattern="critical_thinking")` after every consensus |
| Looping exec calls on the same failing prompt | After 2 failures → delegate to different role or use `debate` |
| Using `async=true` then ignoring the job_id | Always call `status(job_id=...)` to collect output |

---

{{template "delegation-tree" .}}

---

## See Also
{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}
