---
name: delegate
description: "Full delegation decision tree and QUICK format"
args:
  - name: task
    description: "Task to delegate"
related: [agent-exec, review]
---
# Delegation Protocol

## Live Status

- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
{{if .HasMultipleCLIs}}- **Multi-model consensus available** ({{.CLICount}} CLIs registered){{end}}
{{if .Args.task}}- **Task:** `{{.Args.task}}`{{end}}

---

{{template "delegation-tree" .}}

---

## Phase 1 — Classify

**Goal:** Determine the task's size, domain, and risk profile.

| Dimension | Questions to answer |
|-----------|---------------------|
| **Size** | Lines of code? Files touched? New module or patch? |
| **Domain** | Implementation? Review? Security? Testing? Planning? |
| **Risk** | Does it touch auth, data persistence, public API? |
| **Parallelism** | Can subtasks run concurrently? |

**GATE:** Classification must be explicit. Write: "This is a [size] [domain] task with [risk] risk."

---

## Phase 2 — Route

**Goal:** Assign a role based on classification.

### 9 Delegation Roles

| Role | exec role= | Best for |
|------|-----------|----------|
| **Implementor** | `coding` | New features, scaffolding, boilerplate |
| **Reviewer** | `codereview` | PR review, diff analysis, quality gate |
| **Analyzer** | `analyze` | Long-context research, comparison, survey |
| **Debugger** | `debug` | Bug reproduction, root-cause, fix |
| **Test Writer** | `testgen` | Unit/integration tests, coverage |
| **Investigator** | `analyze` | Domain investigation, unknown system |
| **Red Team** | `secaudit` | Security adversarial review |
| **Pair Programmer** | `coding` | Step-by-step implementation with feedback |
| **Planner** | `planner` | Architecture, roadmap, spec writing |

When {{.CLICount}} ≥ 2 and risk is high, use `consensus` for the Reviewer role.

---

## Phase 3 — Delegate

**Goal:** Issue the delegation with full QUICK format.

### QUICK Format (verbatim — do not abbreviate)

```
TASK: [one sentence — what to implement, not how]
CONTEXT: [files to read/modify, current state, relevant stack details]
CONSTRAINTS: [patterns to follow, what must not change, coding style rules]
MUST NOT: [fake backends, stubs, claim done without running, skip tests]
DONE WHEN: [verifiable outcome 1 — tool output, test passing, endpoint responding]
           [verifiable outcome 2 — no regressions, review passed]
```

Execute delegation:
```
exec(
  role="{{"{{chosen_role}}"}}" ,
  prompt="<QUICK format above>" ,
  async=true
)
```

Capture `job_id` for tracking.

For agent-first routing, try:
```
agent(
  agent="{{"{{best_agent}}"}}" ,
  prompt="<QUICK format above>"
)
```

---

## Phase 4 — Validate

**Goal:** 5-step post-delegation quality gate.

After the delegated session completes:

1. **Check DONE WHEN** — did the output satisfy every verifiable outcome in the QUICK format?
2. **Read changed files** — open each modified file and verify the implementation is real (not a stub).
3. **Run tests** — execute the relevant test suite. All tests must pass.
4. **Check scope** — did the implementation stay within CONSTRAINTS? Flag any overreach.
5. **Code-review lite** — run a quick review:
   ```
   exec(role="codereview", prompt="Lite review — scope check only: {{"{{job_id.content}}"}}")
   ```

**GATE:** All 5 steps must pass before marking the delegation complete. "It compiled" is not sufficient.

---

## Session Resume

For follow-up on a running or completed job:
```
exec(session_id="{{"{{job_id}}"}}" , prompt="<follow-up or correction prompt>")
```

---

## Acceptance Criteria

- [ ] Task classified by size, domain, and risk
- [ ] Role assigned from the 9-role table
- [ ] QUICK format written in full (no fields omitted)
- [ ] Delegation executed via exec or agent (not manual implementation)
- [ ] All 5 post-delegation validation steps completed
- [ ] Code-review lite passed with no P0/P1 findings

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** delegate → `review` (post-delegation quality gate for high-risk changes)
**Receives from:** `guide` (routing complex tasks), `agent-exec` (overflow to delegate when no agent matches), `workflow` (pipeline steps that require delegation)
