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
