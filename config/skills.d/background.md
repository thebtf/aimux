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

## Phase 3 — Poll

**Goal:** Monitor job completion without blocking.

```
status(job_id="{{"{{job_id}}"}}")
```

| `status` value | Action |
|---------------|--------|
| `running` | Poll again in 10–30s |
| `done` | Read `content` field — task is complete |
| `error` | Read `error` field — escalate to `/debug` |
| `cancelled` | Restart with adjusted prompt if needed |

When done, the result is in `{{"{{job_id.content}}"}}`.

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
- [ ] `exec` called with `async=true` and correct role
- [ ] `job_id` captured from exec response
- [ ] `status` polled until `done` or `error`
- [ ] Result read from `content` field on completion

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** background → `guide` (when role selection is unclear or tool usage help needed)
**Receives from:** `delegate` (tasks routed for async execution)
