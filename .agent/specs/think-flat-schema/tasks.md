# Tasks: Think Patterns Flat Schema

**Spec:** .agent/specs/think-flat-schema/spec.md
**Plan:** .agent/specs/think-flat-schema/plan.md
**Generated:** 2026-04-09

## Phase 0: Planning

- [x] P001 Assign executors
  AC: all tasks reviewed

## Phase 1: MCP Schema + Server Forwarding

- [x] T001 Add flat params to think tool MCP schema in `pkg/server/server.go`: hypothesis_text, confidence, findings_text, hypothesis_action, entry_type, entry_text, link_to, contribution_type, contribution_text, persona_id, contribution_confidence, argument_type, argument_text, supports_claim_id, step_number, next_step_needed
  AC: 16 new WithString/WithNumber/WithBool params added · each has instructional description · go build passes · existing tests pass
- [x] T002 [P] Add flat params to handleThink optionalStrings in `pkg/server/server.go`: hypothesis_text, confidence, findings_text, hypothesis_action, entry_type, entry_text, link_to, contribution_type, contribution_text, persona_id, argument_type, argument_text, supports_claim_id
  AC: params forwarded to pattern input map · old nested params still forwarded via forwardKeys · go build passes

- [x] G001 VERIFY Phase 1 (T001–T002) — BLOCKED until T001–T002 all [x]
  RUN: `go build ./...`. Verify schema has new params via `tools/list`.
  CHECK: 16 new params visible. Old params still work.
  RESOLVE: Fix ALL findings.

---

## Phase 2: debugging_approach + scientific_method (FR-1, FR-2, FR-6)

- [x] T003 Modify `pkg/think/patterns/debugging_approach.go` Validate+Handle: accept flat params (hypothesis_text, confidence enum, findings_text, hypothesis_action, step_number). Auto-generate hypothesis ID. Map flat → internal representation. Detect old nested format via type-switch. Step progression with evidence gates.
  AC: hypothesis_text="SQL injection" + confidence="medium" → hypothesis tracked in session · step_number=1 without findings → guidance "investigate first" · hypothesis_action="refute" → hypothesis status updated · old nested {id,text,confidence} map still works · 5 tests · swap body→return null ⇒ tests MUST fail
- [x] T004 [P] Modify `pkg/think/patterns/scientific_method.go` Validate+Handle: accept flat params (entry_type, entry_text, link_to). Auto-generate entry IDs. Auto-link by type sequence when link_to omitted. Step progression with lifecycle gates.
  AC: entry_type="hypothesis" + entry_text="..." → auto-ID assigned, stored · entry_type="prediction" without prior hypothesis → STOP · auto-link: prediction auto-links to last hypothesis when link_to omitted · old nested entry map still works · 4 tests · swap body→return null ⇒ tests MUST fail

- [x] G002 VERIFY Phase 2 (T003–T004) — BLOCKED until T003–T004 all [x]
  RUN: `go test ./pkg/think/... -v -count=1`. Smoke test debugging_approach 3-step flow via MCP.
  CHECK: Flat params work. Old format works. Evidence gates fire. Step progression tracks.
  ENFORCE: Zero regressions.
  RESOLVE: Fix ALL findings.

---

## Phase 3: collaborative + argumentation + sequential (FR-3, FR-4)

- [x] T005 [P] Modify `pkg/think/patterns/collaborative_reasoning.go` Validate+Handle: accept flat params (contribution_type, contribution_text, persona_id, contribution_confidence). Map flat → internal contribution representation.
  AC: contribution_type="insight" + contribution_text + persona_id → tracked in session · old nested contribution map still works · 2 tests · swap body→return null ⇒ tests MUST fail
- [x] T006 [P] Modify `pkg/think/patterns/structured_argumentation.go` Validate+Handle: accept flat params (argument_type, argument_text, supports_claim_id). Map flat → internal argument representation.
  AC: argument_type="evidence" + argument_text + supports_claim_id="c1" → tracked in session · old nested argument map still works · 2 tests · swap body→return null ⇒ tests MUST fail
- [x] T007 [P] Verify sequential_thinking works with current flat params (thought, branchId, revisesThought already flat). Add step_number forwarding if missing.
  AC: existing params verified working via MCP · step_number forwarded if provided · 1 test

- [x] G003 VERIFY Phase 3 (T005–T007) — BLOCKED until T005–T007 all [x]
  RUN: `go test ./pkg/think/... -v -count=1`.
  CHECK: All 5 patterns accept flat params. All backward compat.
  ENFORCE: Zero regressions.
  RESOLVE: Fix ALL findings.

---

## Phase 4: Smoke Test + Regression

- [x] T008 Build binary, smoke test all 5 stateful patterns via aimux-dev MCP with flat params
  AC: debugging_approach 3-step flow works · scientific_method hypothesis→prediction works · collaborative contribution tracked · results documented
- [x] T009 [P] Full regression: `go test ./... -timeout 300s` + `go vet ./...` + build binary
  AC: all tests pass · no vet warnings · binary builds

- [x] G004 VERIFY Phase 4 (T008–T009) — BLOCKED until T008–T009 all [x]
  RUN: Full test suite + smoke test via MCP.
  CHECK: Zero regressions. All 5 patterns usable via flat MCP params.
  RESOLVE: Fix ALL findings.

---

**Checkpoint:** All stateful patterns usable via flat MCP params with step progression.

## Dependencies

- G001 blocks Phase 2+ (schema must have params before patterns use them)
- Phase 2 and Phase 3 are independent
- G002 + G003 block Phase 4

## Execution Strategy

- **MVP:** Phase 0-2 (debugging_approach + scientific_method — most impactful)
- **Parallel:** T001||T002, T003||T004, T005||T006||T007
- **Commit:** one per phase
