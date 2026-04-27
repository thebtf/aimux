---
name: tdd
description: "TDD workflow with RED/GREEN/IMPROVE gates"
args:
  - name: feature
    description: "Feature to implement with TDD"
related: [review, debug]
---
# TDD Workflow

{{if CallerHasSkill "tdd"}}
> **Note:** Your agent already has a TDD skill configured. Use it directly — this skill is supplementary
> and provides the canonical gate definitions and exec wiring for cases where the agent skill is incomplete.
{{end}}

## Live Status

- **Test CLI:** {{RoleFor "testgen"}} (role=testgen)
- **Coding CLI:** {{RoleFor "coding"}} (role=coding)
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
{{if .Args.feature}}- **Feature:** `{{.Args.feature}}`{{end}}

---

## The TDD Contract

> Write the test that proves your feature works **before** writing the feature.
> If the test passes before implementation, the test is wrong.
> If the implementation passes without changing behavior, the implementation is wrong.

Three laws:
1. You may not write production code unless it is to make a failing test pass.
2. You may not write more of a test than is sufficient to fail.
3. You may not write more production code than is sufficient to pass the test.

---

## Phase 1 — RED

**Goal:** Write a failing test that specifies the desired behavior of `{{.Args.feature}}`.

```
exec(
  role="testgen",
  prompt="Write a failing test for: {{.Args.feature}}\n\nRequirements:\n- Test must compile\n- Test must FAIL with a meaningful assertion error (not a compile error)\n- Test must describe the BEHAVIOR, not the implementation\n- No mocks unless the dependency is external (network, DB, filesystem)\n- Name the test file and function explicitly in the output"
)
```

**GATE: A test that has only been written but not compiled and executed does NOT count as RED.**

Before proceeding to Phase 2, you MUST confirm:
- [ ] Test file exists on disk (VERIFIED via file read or ls)
- [ ] Test compiles without errors (VERIFIED via build tool output)
- [ ] Test FAILS when run against current codebase (VERIFIED via test runner output showing FAIL)
- [ ] Failure message is a meaningful assertion error, not a compile error or panic

Classification: "Test compiled and failed with [exact error message]" = VERIFIED RED.
"Test should fail because the feature doesn't exist yet" = STALE. Not sufficient.

---

## Phase 2 — GREEN

**Goal:** Write the minimal implementation that makes the failing test pass.

```
exec(
  role="coding",
  prompt="Make this test pass with minimal implementation:\n\nTest file: {{"{{red_phase_test_file}}"}}\nTest output: {{"{{red_phase_output}}"}}\n\nFeature: {{.Args.feature}}\n\nRules:\n- Write the MINIMUM code to pass the test\n- Do NOT refactor yet\n- Do NOT add code for cases the test doesn't cover\n- Do NOT change the test"
)
```

**GATE: The previously failing test from Phase 1 now passes.**

Before proceeding to Phase 3, confirm:
- [ ] The exact test from Phase 1 now shows PASS (VERIFIED via test runner output)
- [ ] No other tests were modified
- [ ] The test runner shows no new failures

Do NOT proceed to Phase 3 with any failing tests.

---

## Phase 3 — IMPROVE

**Goal:** Refactor the implementation without changing behavior.

```
exec(
  role="coding",
  prompt="Refactor this implementation for clarity and correctness:\n\nImplementation: {{"{{green_phase_files}}"}}\nTest output (all passing): {{"{{green_phase_test_output}}"}}\n\nRefactor rules:\n- Remove duplication\n- Clarify naming\n- Extract functions if body exceeds 20 lines\n- Do NOT change observable behavior\n- Do NOT add features\n- All tests must still pass after refactor"
)
```

**GATE: All tests still pass after refactor.**

Before proceeding to Phase 4, confirm:
- [ ] Entire test suite passes (not just the Phase 1 test) — VERIFIED via full test run
- [ ] No new code added beyond the scope of the feature
- [ ] Implementation is readable by a reviewer unfamiliar with the task

---

## Phase 4 — Coverage

**Goal:** Verify coverage meets the 80% threshold for the feature module.

Run coverage on the affected package:
```
exec(
  role="testgen",
  prompt="Check coverage for the feature module and add missing tests if below 80%:\n\nFeature: {{.Args.feature}}\nImplementation files: {{"{{improve_phase_files}}"}}\n\nReport the coverage percentage. If below 80%, add tests for uncovered branches.\nDo NOT add tests for trivial getters/setters or language boilerplate."
)
```

{{template "verification-gate" .}}

---

## Post-TDD Escalation

After a complete TDD cycle, escalate to code review:

```
exec(
  role="codereview",
  prompt="Review TDD implementation:\n\nFeature: {{.Args.feature}}\nTest file: {{"{{red_phase_test_file}}"}}\nImplementation: {{"{{improve_phase_files}}"}}\nCoverage: {{"{{coverage_phase_output}}"}}\n\nFocus: Does the test actually test the behavior? Is the implementation correct? Any edge cases missing?"
)
```

{{if ge .CLICount 2}}
For critical features, use consensus review:
```
consensus(
  topic="TDD review for: {{.Args.feature}}\nTest: {{"{{red_phase_test_file}}"}}\nImpl: {{"{{improve_phase_files}}"}}"
)
```
{{end}}

---

## Acceptance Criteria

- [ ] Phase 1 (RED): Test compiles and fails with meaningful assertion error (VERIFIED this session)
- [ ] Phase 2 (GREEN): Previously failing test now passes (VERIFIED via test runner output)
- [ ] Phase 3 (IMPROVE): Full test suite passes after refactor (VERIFIED)
- [ ] Phase 4 (COVERAGE): Feature module coverage ≥ 80%
- [ ] Code review completed with no P0/P1 findings

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** tdd → `review` (post-TDD code review gate)
**Receives from:** `delegate` (implementation tasks routed to TDD workflow)
