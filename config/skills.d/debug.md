---
name: debug
description: "Structured debug workflow — reproduce, investigate, root-cause, fix, verify"
args:
  - name: error
    description: "Error message, symptom, or bug description"
related: [security, audit, review]
---
# Debug Workflow

## Live Status

- **Debug CLI:** {{RoleFor "debug"}} (role=debug)
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
- **Past Reports:** {{len .PastReports}} session(s)
{{if .Args.error}}- **Target Error:** `{{.Args.error}}`{{end}}

---

## Scale Decision

Before running any phase, classify the bug:

| Signal | Mode | Approach |
|--------|------|----------|
| Known error with clear stack trace | **Quick** | Phase 1 → Phase 3 (root cause) → Phase 4, skip deep investigate session |
| Intermittent or hard to reproduce | **Standard** | All 5 phases, full investigate session |
| Systemic / affects multiple modules | **Deep** | All 5 phases + consensus if {{.CLICount}} ≥ 2 |

---

## Phase 1 — Reproduce

**Goal:** Produce a deterministic reproduction of the error.

Steps:
1. Read error logs and identify the file, line, and call chain.
2. Write a minimal reproduction case (test, command, curl, etc.).
3. Run the reproduction. Capture the exact output.

**GATE:** You MUST have a VERIFIED reproduction before proceeding to Phase 2.
- Classification: VERIFIED means tool output shows the failure this session.
- "It probably fails" or "it failed yesterday" = STALE. Not sufficient. Reproduce it.

---

## Phase 2 — Investigate

**Goal:** Gather structured evidence across the codebase.

```
investigate(action="start", topic="{{"{{.Args.error}}"}}", domain="debugging")
```

After `investigate` returns a `session_id`, add findings:
```
investigate(action="finding", session_id="{{"{{session_id}}"}}", finding="<what you observed>", source="<file:line or tool output>")
```

Assess when evidence converges:
```
investigate(action="assess", session_id="{{"{{session_id}}"}}")
```

**GATE:** Do NOT proceed to Phase 3 until `assess` returns a working hypothesis.
State the hypothesis explicitly: "The root cause is X, evidenced by Y."

{{template "evidence-table" .}}

---

## Phase 3 — Root Cause

**Goal:** Confirm the hypothesis with structured reasoning.

```
think(pattern="debugging_approach", issue="{{"{{assess_summary}}"}}", session_id="{{"{{session_id}}"}}")
```

Use the SAME `session_id` from Phase 2 to maintain investigation continuity.

The `think` output must answer:
- Why does the bug occur? (mechanism)
- What invariant is violated? (contract broken)
- What is the minimal change that fixes it? (scope)

**GATE:** Root cause must be stated as a falsifiable claim before proceeding.
Example: "The nil pointer occurs because `cfg.Client` is never initialized when `SKIP_AUTH=true`."

{{template "verification-gate" .}}

---

## Phase 4 — Fix

**Goal:** Implement and code-review the fix.

```
exec(role="debug", prompt="Fix root cause: {{"{{phase3_root_cause}}"}}\n\nContext:\n- Error: {{"{{.Args.error}}"}}\n- Files: {{"{{affected_files}}"}}\n- Hypothesis: {{"{{phase3_output}}}"}}")
```

After implementation, immediately run code review:
```
exec(role="codereview", prompt="Review fix for: {{"{{.Args.error}}"}}\nDiff: {{"{{git_diff}}}"}}")
```

Address all P0/P1 findings from the review before proceeding.

{{if ge .CLICount 2}}
### Multi-Model Escalation

When the root cause is ambiguous or the fix touches critical paths, escalate:
```
consensus(topic="Root cause and fix for: {{"{{.Args.error}}"}}\nHypothesis: {{"{{phase3_root_cause}}"}}\nProposed fix: {{"{{fix_summary}}}"}}")
```
{{end}}

---

## Phase 5 — Verify

**Goal:** Confirm the original reproduction now passes.

1. Run the exact reproduction from Phase 1.
2. Run the full test suite for the affected module.
3. Check for regressions in adjacent modules.

**GATE:** Phase 5 is complete only when the Phase 1 reproduction passes AND no new failures appear.

If Phase 5 fails → return to Phase 2 with the SAME `session_id`:
```
investigate(action="finding", session_id="{{"{{session_id}}"}}", finding="Fix did not resolve: {{"{{new_output}}"}}", source="Phase 5 verification")
```

---

## Acceptance Criteria

- [ ] Deterministic reproduction exists and was confirmed this session (VERIFIED)
- [ ] Root cause stated as a falsifiable claim with file/line evidence
- [ ] Fix addresses the root cause, not the symptom
- [ ] Code review passed (no P0/P1 findings outstanding)
- [ ] Original reproduction now passes
- [ ] No new test failures introduced

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** debug → `consensus` (when fix is ambiguous or high-risk)
**Receives from:** `audit` (P0 bug findings routed here), `investigate` (when investigation overflows)
