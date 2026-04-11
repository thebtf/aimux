---
name: agent-exec
description: "Agent-first execution — match task to agents, exec as fallback"
args:
  - name: task
    description: "Task description to match against available agents"
related: [delegate, guide]
---
# Agent-First Execution

## Live Status

- **Registered Agents:** {{len .Agents}}
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
{{if .Args.task}}- **Task:** `{{.Args.task}}`{{end}}

---

> **HARD GATE: Always try `agent` tool FIRST. `exec` is the fallback, not the default.**
>
> Agents are project-scoped, context-aware, and purpose-built. A generic exec with role=coding
> has none of that context. Use agents when they match — they will outperform exec on their domain.

---

## Phase 1 — Discover

**Goal:** Enumerate available agents and their capabilities.

```
agents(action="list")
```

Available agents this session:

{{range .Agents}}
- **{{.Name}}** — {{.Description}} *(role: {{.Role}})*
{{end}}

{{if eq (len .Agents) 0}}
No agents registered. Proceeding to exec fallback (Phase 3).
{{end}}

---

## Phase 2 — Match

**Goal:** Find the best-fit agent for `{{.Args.task}}`.

Matching strategy — keyword relevance scan:

```
agents(action="find", query="{{"{{.Args.task}}"}}")
```

| Signal | Match confidence |
|--------|-----------------|
| Agent description contains 3+ keywords from task | **High** — use this agent |
| Agent description contains 1–2 keywords | **Medium** — use if no better match |
| No keyword overlap | **Low** — skip, use exec fallback |

**GATE:** If `agents(action="find")` returns a match with confidence ≥ Medium, you MUST use that agent. Skipping to exec without attempting agent first = protocol violation.

---

## Phase 3 — Execute (Agent Path)

**Goal:** Dispatch to the matched agent in async mode and let a wrapper subagent poll.

```
agent(
  agent="{{"{{matched_agent_name}}"}}" ,
  prompt="{{"{{.Args.task}}"}}",
  async=true
)
```

Capture `job_id` and immediately hand off to the polling wrapper subagent per the
mandatory rule — see Phase 5 below. Do NOT poll `status(job_id=...)` yourself.

---

## Phase 4 — Execute (Exec Fallback)

**Goal:** When no agent matches, fall back to role-based exec.

Only reach this phase if:
- `agents(action="list")` returned 0 agents, OR
- `agents(action="find")` returned no matches with confidence ≥ Medium

```
exec(
  role="{{"{{inferred_role}}"}}" ,
  prompt="{{"{{.Args.task}}"}}" ,
  async=true
)
```

Same rule applies: capture `job_id` and proceed to Phase 5 for the wrapper handoff.

---

## Phase 5 — Hand off polling to a subagent wrapper (MANDATORY)

Whether Phase 3 (agent path) or Phase 4 (exec fallback) produced the `job_id`, you
MUST NOT call `status(job_id=...)` from this turn. Spawn a Sonnet polling wrapper via
your Task/Agent tool and receive the final content from it.

{{template "poll-wrapper-subagent" .}}

Role inference from task keywords (same table as `/background`):

| Keywords | Role |
|----------|------|
| `review`, `diff`, `PR` | `codereview` |
| `security`, `vuln` | `secaudit` |
| `test`, `coverage` | `testgen` |
| `bug`, `error`, `crash` | `debug` |
| `plan`, `architecture` | `planner` |
| `research`, `compare` | `analyze` |
| (default) | `coding` |

---

{{template "delegation-tree" .}}

---

## Session Resume

For multi-turn follow-up on an exec job:
```
exec(session_id="{{"{{job_id}}"}}" , prompt="<follow-up prompt>")
```

---

## Acceptance Criteria

- [ ] `agents(action="list")` called before any exec
- [ ] If agents found, `agents(action="find")` used for keyword matching
- [ ] Agent path attempted before exec fallback
- [ ] exec fallback only used when agent match confidence is Low or no agents registered
- [ ] Async exec jobs polled via `status(job_id=...)`

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** agent-exec → `delegate` (when task exceeds agent scope or requires QUICK format)
**Receives from:** `guide` (tool routing), `workflow` (agent steps in pipelines)
