---
name: review
description: "Code review with CLI-adaptive multi-phase gates"
args:
  - name: scope
    description: "What to review: staged, branch, last-commit, or file paths"
related: [security, audit, debug]
---
# Review Workflow

## Live Status

- **Review CLI:** {{RoleFor "codereview"}} (role=codereview)
- **Coding CLI:** {{RoleFor "coding"}} (role=coding)
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
- **Scope:** `{{.Args.scope}}`

---

## Scale Decision

| Signal | Mode | Approach |
|--------|------|----------|
| 1-3 files changed | **Quick** | Phase 1 → Phase 2 only, think fallback if single model |
| 4-10 files changed | **Standard** | All 4 phases |
| 10+ files changed | **Deep** | All 4 phases + consensus or debate |

---

## Phase 1 — Gather Diff

**Goal:** Read the actual diff. No review without a diff in hand.

For `staged`:
```
exec(role="coding", prompt="Run: git diff --cached\nReturn the full diff output.")
```

For `branch`:
```
exec(role="coding", prompt="Run: git diff main...HEAD\nReturn the full diff output.")
```

For `last-commit`:
```
exec(role="coding", prompt="Run: git diff HEAD~1 HEAD\nReturn the full diff output.")
```

For specific file paths (`{{.Args.scope}}`):
```
exec(role="coding", prompt="Run: git diff -- {{"{{.Args.scope}}"}}\nReturn the full diff output.")
```

**GATE:** The diff MUST be in hand before Phase 2. "I'll review the code from memory" is NOT acceptable.
- Classification: VERIFIED = diff output captured this session via tool.
- STALE = "I saw this code earlier" → re-read it now.

{{template "evidence-table" .}}

---

## Phase 2 — Review

**Goal:** Structured code review across all quality dimensions.

```
exec(role="codereview", prompt="Review the following changes:\n\nScope: {{"{{.Args.scope}}"}}\n\nDiff:\n{{"{{diff_output}}"}}\n\nCheck for:\n1. Correctness — logic errors, off-by-one, null dereferences\n2. Security — injection, secrets, auth bypass (flag for aimux-security if P0)\n3. Performance — N+1 queries, unnecessary allocations, blocking I/O\n4. Maintainability — naming, cohesion, abstraction level\n5. Test coverage — are critical paths tested?\n6. Error handling — all errors handled, no silent swallowing\n\nOutput each finding with: file:line, category, severity (P0-P3), description.")
```

**GATE:** Review output must contain explicit findings per category (even "no issues found").

{{template "priority-scoring" .}}

---

## Phase 3 — Critical Thinking

**Goal:** Apply adversarial scrutiny to the review findings.

{{if .HasMultipleCLIs}}
### Multi-Model Path ({{.CLICount}} CLIs available)

Challenge the review with a second model:
```
consensus(topic="Code review findings for: {{"{{.Args.scope}}"}}\n\nFindings from Phase 2:\n{{"{{review_output}}"}}\n\nQuestion: Are any findings wrong or missing? What would a staff engineer add?")
```

Or for high-stakes changes, run a debate:
```
debate(topic="Should these changes be merged?\n\nChanges: {{"{{.Args.scope}}"}}\nCurrent findings: {{"{{review_output}}"}}", max_turns=3)
```

{{else}}
### Single-Model Path ({{.CLICount}} CLI available)

Use structured self-critique:
```
think(pattern="peer_review", artifact="{{"{{review_output}}"}}")
```

This simulates a second reviewer by applying adversarial questioning patterns:
- "What could go wrong at scale?"
- "What edge case did we miss?"
- "What would break this in production?"

{{end}}

**GATE:** Phase 3 output must either confirm Phase 2 findings or add/remove items from the list.

{{template "verification-gate" .}}

---

## Phase 4 — Fix

**Goal:** Implement fixes for all P0/P1 findings. Document P2/P3 as follow-ups.

For each P0/P1 finding:
```
exec(role="coding", prompt="Fix review finding:\nFile: {{"{{file_path}}"}}\nFinding: {{"{{finding_description}}"}}\nSeverity: {{"{{severity}}"}}\nDiff context:\n{{"{{diff_context}}}"}}")
```

After all fixes, run a final confirmation review:
```
exec(role="codereview", prompt="Confirm fixes are correct and complete for:\n{{"{{fixed_findings_list}}"}}\n\nReview the updated diff:\n{{"{{updated_diff}}}"}}")
```

**GATE:** No P0/P1 findings may remain unaddressed. P2/P3 may be deferred with explicit documentation.

---

## Acceptance Criteria

- [ ] Actual diff captured this session (VERIFIED, not from memory)
- [ ] All 6 quality dimensions assessed (correctness, security, performance, maintainability, tests, error handling)
- [ ] All findings prioritized P0-P3 with file:line references
- [ ] Critical thinking phase completed (consensus, debate, or think peer_review)
- [ ] All P0/P1 findings fixed and re-reviewed
- [ ] P2/P3 findings documented as explicit follow-ups

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** review → `consensus` (high-stakes changes), `aimux-security` (P0 security findings), `aimux-debug` (P0 bugs found during review)
**Receives from:** `audit` (P1 quality findings routed here), `delegate` (pre-merge review gate)
