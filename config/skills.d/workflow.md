---
name: workflow
description: "Declarative multi-step pipeline builder"
args:
  - name: goal
    description: "What the workflow should accomplish"
related: [delegate, guide]
---
# Workflow Builder

## Live Status

- **Workflow CLI:** {{RoleFor "analyze"}} (role=analyze)
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
- **Think Patterns:** {{range .ThinkPatterns}}`{{.}}` {{end}}
{{if .Args.goal}}- **Goal:** `{{.Args.goal}}`{{end}}

---

## Step Schema

Each step in a pipeline is a JSON object with the following fields:

```json
{
  "id":        "step_id",
  "tool":      "exec | think | investigate | consensus | audit | agent",
  "params":    { "role": "coding", "prompt": "{{"{{input}}"}}", "async": false },
  "condition": "{{"{{prev_step.status}}"}}" == "ok",
  "on_error":  "stop | continue | retry"
}
```

### Template Variables Available Inside Step Params

| Variable | Resolves to |
|----------|-------------|
| `{{"{{input}}"}}` | Original workflow input (`.Args.goal`) |
| `{{"{{step_id.content}}"}}` | Output content of a prior step by id |
| `{{"{{step_id.status}}"}}` | Exit status of a prior step: `ok`, `error`, `skipped` |
| `{{"{{step_id.job_id}}"}}` | Async job_id returned by a step that used `async=true` |

---

## Phase 1 — Goal Analysis

**Goal:** Classify the goal and select the canonical pipeline template.

Keyword scan of `{{.Args.goal}}`:

| Keywords detected | Pipeline template |
|-------------------|-------------------|
| `security`, `vuln`, `owasp`, `CVE` | **Security audit pipeline** |
| `bug`, `error`, `crash`, `nil`, `panic` | **Debug pipeline** |
| `review`, `diff`, `PR`, `quality` | **Review pipeline** |
| `research`, `compare`, `evaluate` | **Research pipeline** |
| `refactor`, `clean`, `restructure` | **Refactor pipeline** |
| `test`, `coverage`, `TDD` | **TDD pipeline** |
| (none of the above) | **Generic pipeline** |

**GATE:** Goal must be classified before generating a pipeline. If ambiguous, ask: "Is this closer to investigation, implementation, or review?"

---

## Phase 2 — Generate Pipeline

**Goal:** Produce a ready-to-execute pipeline JSON.

```
think(pattern="problem_decomposition", issue="{{"{{.Args.goal}}"}}")
```

Use the output to construct the pipeline. Example — **Debug pipeline**:

```json
[
  {
    "id":       "reproduce",
    "tool":     "exec",
    "params":   { "role": "debug", "prompt": "Reproduce: {{"{{input}}"}}", "async": false },
    "on_error": "stop"
  },
  {
    "id":       "investigate",
    "tool":     "investigate",
    "params":   { "action": "start", "topic": "{{"{{input}}"}}", "domain": "debugging" },
    "on_error": "stop"
  },
  {
    "id":       "fix",
    "tool":     "exec",
    "params":   { "role": "debug", "prompt": "Fix root cause from: {{"{{investigate.content}}"}}", "async": true },
    "condition": "{{"{{investigate.status}}"}}" == "ok",
    "on_error": "stop"
  },
  {
    "id":       "verify",
    "tool":     "exec",
    "params":   { "role": "codereview", "prompt": "Review fix: {{"{{fix.content}}"}}" },
    "condition": "{{"{{fix.status}}"}}" == "ok",
    "on_error": "continue"
  }
]
```

**GATE:** Pipeline must have at minimum: one execution step, one verification step, `on_error` set on every step.

---

## Phase 3 — Execute

**Goal:** Run the generated pipeline.

```
workflow(steps="<pipeline JSON from Phase 2>", input="{{"{{.Args.goal}}"}}")
```

{{template "poll-wrapper-subagent" .}}

{{template "delegation-tree" .}}

---

## Available Tools for Steps

| Tool | Roles / Params | Use for |
|------|----------------|---------|
| `exec` | role=coding/debug/analyze/codereview/testgen/planner | Implementation, review, analysis |
| `think` | pattern=problem_decomposition/debugging_approach/... | Pure reasoning |
| `investigate` | action=start/finding/assess/report | Structured evidence gathering |
| `consensus` | topic, synthesize=true | Multi-model agreement |
| `audit` | cwd, mode=quick/standard/deep | Codebase-wide scan |
| `agent` | agent=<name>, prompt | Invoke a named project agent |

Think patterns available: {{range .ThinkPatterns}}`{{.}}` {{end}}

---

## Escalation

After pipeline completes, validate the outcome with audit:
```
audit(cwd=".", mode="quick")
```

Route audit findings to: **aimux-audit** → **aimux-debug** or **aimux-security**.

---

## Acceptance Criteria

- [ ] Goal classified into a named pipeline template
- [ ] Pipeline JSON generated with all required fields (`id`, `tool`, `params`, `on_error`)
- [ ] Every async step has a corresponding `status` poll
- [ ] Pipeline executed via `workflow` tool (not manual step-by-step)
- [ ] Post-pipeline audit run or explicitly waived with justification

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** workflow → `audit` (post-pipeline validation)
**Receives from:** `delegate` (complex multi-step tasks routed here)
