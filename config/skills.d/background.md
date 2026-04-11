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

### Step 3: Hand off to a polling wrapper subagent (MANDATORY)

**GATE:** You may not poll `status(job_id=...)` from your own context for any job
dispatched in this skill. Every async dispatch MUST be followed by a Task/Agent call
that wraps polling in a Sonnet subagent. No exceptions for coding, research, audit,
debug, analyze, or plan work.

{{template "poll-wrapper-subagent" .}}

Pick the sync variant unless you genuinely need to parallel-work while the job runs.
For this skill (background execution of a single task dispatched via `exec`), sync is
almost always correct: your main turn wants the final content, not live progress.

### Step 4: Update tasks on wrapper return (sync variant)

When the Task/Agent call returns:

| Wrapper output | Action |
|----------------|--------|
| Final content | Task 2 `completed`, task 3 `in_progress` → parse result → task 3 `completed` |
| Error message | Task 2 `completed` with error note → escalate to `/debug` |
| TIMEOUT line | Task 2 `completed` with timeout note → investigate why the CLI stalled (issue #8, P26 hard_stall) |

**IMPORTANT:** Main agent does NOT poll directly. The wrapper subagent polls in its own
context. Main agent sees one clean tool result, not a polling loop.

### Step 4b: React to notifications (async variant)

If you chose `run_in_background=true` for the wrapper, react to each `<task-notification>`:

| Wrapper status | Action |
|---------------|--------|
| Wrapper still running | Task 2 stays `in_progress` |
| Wrapper completed with content | Task 2 `completed`, task 3 `in_progress` → parse → task 3 `completed` |
| Wrapper completed with error | Task 2 `completed` with error note |

### Sync mode alternative (direct, no wrapper)

For short tasks (<30s expected), direct sync exec is acceptable and simpler:

```
exec(prompt="...", role="...", async=false, timeout_seconds=30)
```

The MCP server sends `notifications/progress` during execution — Claude Code renders
a progress bar automatically. Use ONLY when task is known to be fast; for anything
longer, prefer the polling wrapper subagent.

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
- [ ] **Polling wrapper subagent spawned via Task/Agent** with the captured `job_id`
- [ ] **Main agent does NOT call `status(job_id=...)` at any point** — only the wrapper does
- [ ] Wrapper used `subagent_type="general-purpose"` and `model="sonnet"` (no other types)
- [ ] On wrapper return, tasks updated per phase table
- [ ] Result read from the wrapper's final content (sync) or from `<task-notification>` (async)
- [ ] All tasks marked completed with outcome summary

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** background → `guide` (when role selection is unclear or tool usage help needed)
**Receives from:** `delegate` (tasks routed for async execution)
