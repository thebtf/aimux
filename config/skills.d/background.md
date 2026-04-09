---
name: background
description: "Background execution protocol with role routing"
args:
  - name: task_description
    description: "Description of the task to execute"
related: [guide, delegate]
---
# Background Execution

## Live Status

- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
{{if .Args.task_description}}- **Task:** `{{.Args.task_description}}`{{end}}

---

## Phase 1 — Route

**Goal:** Map the task description to the correct execution role.

Keyword analysis of `{{.Args.task_description}}`:

| Keywords | Recommended Role | Rationale |
|----------|-----------------|-----------|
| `review`, `diff`, `PR`, `quality`, `lint` | `codereview` | Code quality evaluation |
| `security`, `vuln`, `OWASP`, `CVE`, `injection` | `secaudit` | Security analysis |
| `test`, `spec`, `coverage`, `TDD`, `assert` | `testgen` | Test generation |
| `bug`, `error`, `crash`, `nil`, `panic`, `trace` | `debug` | Debugging |
| `plan`, `roadmap`, `architecture`, `design` | `planner` | Planning and design |
| `research`, `compare`, `evaluate`, `survey` | `analyze` | Long-context analysis |
| (none of the above) | `coding` | General implementation |

**GATE:** Role must be confirmed before executing. If keywords overlap two categories, pick the higher-priority one: security > debug > review > test > plan > analyze > coding.

---

## Phase 2 — Execute (async)

**Goal:** Dispatch the task to the background.

```
exec(
  prompt="{{"{{.Args.task_description}}"}}" ,
  role="<recommended role from Phase 1>",
  async=true
)
```

Capture the `job_id` from the response. Example response:
```json
{ "job_id": "job_abc123", "status": "queued" }
```

---

## Phase 3 — Monitor with Task List

**Goal:** Give the user real-time visibility into job progress.

### Step 1: Create a task for the user

Immediately after dispatching the async job, create a task so the user can track it:

```
TaskCreate(description="[aimux] <role>: <short task summary>", status="in_progress")
```

### Step 2: Poll and update

```
status(job_id="{{"{{job_id}}"}}")
```

The `progress` field contains **live CLI output** — the actual lines the CLI has produced so far.
Parse it and update the task:

| `status` value | `progress` field | Action |
|---------------|-----------------|--------|
| `running` | Non-empty | Update task with latest progress summary. Poll again in 10–15s. |
| `running` | Empty | CLI hasn't produced output yet. Poll again in 5s. |
| `completed` | — | Read `content` field. Mark task completed. |
| `failed` | — | Read `error` field. Mark task completed with error note. Escalate to `/debug`. |
| `cancelled` | — | Mark task completed. Restart with adjusted prompt if needed. |

### Step 3: Report result

When job completes, update the task one final time with outcome summary, then report
the content to the user or consume it for the next workflow step.

**IMPORTANT:** Never use `async=false` — it blocks the MCP transport with no escape hatch.
Always `async=true` + task-based monitoring.

---

## Role Reference

| Role | CLI routed to | Best for |
|------|--------------|----------|
| `coding` | codex | Implementation, scaffolding |
| `codereview` | gemini | Code quality, diff review |
| `debug` | codex | Bug investigation and fixes |
| `secaudit` | codex | Security vulnerability analysis |
| `analyze` | gemini | Long-context research, comparison |
| `refactor` | codex | Structural cleanup |
| `testgen` | codex | Test generation, coverage |
| `planner` | codex | Architecture and roadmap |

If a CLI fails (rate limit, timeout), aimux auto-retries with the next capable CLI.

---

## Acceptance Criteria

- [ ] Task keyword-matched to a role from the reference table
- [ ] `exec` called with `async=true` (NEVER async=false) and correct role
- [ ] `job_id` captured from exec response
- [ ] TaskCreate called immediately with job description
- [ ] `status` polled — `progress` field parsed for live CLI output
- [ ] Task updated with progress summary on each poll
- [ ] Result read from `content` field on completion
- [ ] Task marked completed with outcome summary

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** background → `guide` (when role selection is unclear or tool usage help needed)
**Receives from:** `delegate` (tasks routed for async execution)
