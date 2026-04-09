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

## Phase 2 — Dispatch + Delegate

**Goal:** Launch async job AND delegate monitoring to a background agent.

### Step 1: Create tasks for user visibility

Create tasks BEFORE dispatching so the user sees progress immediately:

```
TaskCreate(subject="[aimux] <role>: dispatch", activeForm="Dispatching to <cli>")
TaskCreate(subject="[aimux] <role>: executing", activeForm="CLI processing prompt")
TaskCreate(subject="[aimux] <role>: collect result", activeForm="Collecting output")
```

Mark task 1 as `in_progress` immediately.

### Step 2: Dispatch async job

```
exec(
  prompt="{{"{{.Args.task_description}}"}}",
  role="<recommended role from Phase 1>",
  async=true
)
```

Capture `job_id`. Mark task 1 `completed`, task 2 `in_progress`.

### Step 3: Spawn background monitor agent

```
Agent(
  model="sonnet",
  run_in_background=true,
  description="Monitor aimux job <job_id>",
  prompt="Poll mcp__aimux__status(job_id='<job_id>') every 10s.
    The 'progress' field contains live CLI output (lines produced so far).
    When status='completed': return the 'content' field.
    When status='failed': return the 'error' field.
    Report in under 200 words."
)
```

### Step 4: React to background agent notifications

On EACH `<task-notification>` from the monitor agent, update tasks:

| Monitor status | Action |
|---------------|--------|
| Agent still running | Task 2 stays `in_progress` |
| Agent completed with content | Task 2 `completed`, task 3 `in_progress` → parse result → task 3 `completed` |
| Agent completed with error | Task 2 `completed` with error note. Escalate to `/debug`. |

**IMPORTANT:** Main agent does NOT poll. Main agent reacts to `<task-notification>` events
from the background monitor. This frees the main agent to do other work while CLI executes.

### Sync mode alternative

For short tasks (<10s expected), sync mode with live progress bar is acceptable:

```
exec(prompt="...", role="...", async=false, timeout_seconds=30)
```

The MCP server sends `notifications/progress` during execution — Claude Code renders
a progress bar automatically. Use ONLY when task is known to be fast.

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
- [ ] 3 tasks created (dispatch, executing, collect) BEFORE exec call
- [ ] `exec` called with `async=true` and correct role
- [ ] `job_id` captured from exec response
- [ ] Background monitor agent spawned with job_id
- [ ] Main agent does NOT poll — reacts to `<task-notification>` only
- [ ] Tasks updated on each notification per phase table
- [ ] Result read from `content` field on completion
- [ ] All tasks marked completed with outcome summary

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** background → `guide` (when role selection is unclear or tool usage help needed)
**Receives from:** `delegate` (tasks routed for async execution)
