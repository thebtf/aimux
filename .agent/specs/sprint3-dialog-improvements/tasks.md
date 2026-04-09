# Tasks: Dialog Context Management + ConflictingAreas

**Spec:** .agent/specs/sprint3-dialog-improvements/spec.md
**Generated:** 2026-04-07

## Phase 1: Context Management Module

**Goal:** Budget calculation, compaction, summarization, context building.
**Independent Test:** `go test ./pkg/orchestrator/ -run Context` passes.

- [x] T001 Create `pkg/orchestrator/context.go` — ComputeDialogBudget, CompactTurnContent, ExtractSummary, BuildDialogContext, BuildSynthesisPrompt functions. Pure functions, no state.
- [x] T002 [P] Create `pkg/orchestrator/context_test.go` — Tests: budget calculation (single/multi participant), compaction (blank lines, whitespace, truncation), extractive summary (short/long content), context building (within/over budget), synthesis truncation

---

**Checkpoint:** Context management functions tested independently.

## Phase 2: Strategy Integration

**Goal:** Wire context management into existing strategies.
**Independent Test:** Existing strategy tests still pass + new integration tests.

- [x] T003 Update `pkg/orchestrator/dialog.go` (SequentialDialog) — apply CompactTurnContent to turn content, use BuildDialogContext for prompt history, add max response hint, return partial results on turn failure
- [x] T004 Update `pkg/orchestrator/consensus.go` (ParallelConsensus) — apply BuildSynthesisPrompt for synthesis step, CompactTurnContent for individual responses
- [x] T005 Update `pkg/orchestrator/debate.go` (StructuredDebate) — apply BuildDialogContext for turn history, BuildSynthesisPrompt for verdict synthesis, CompactTurnContent for turns
- [x] T006 [P] Add integration tests for context-aware strategies

---

**Checkpoint:** All 3 strategies use context management. Existing tests pass.

## Phase 3: ConflictingAreas Enhancement

**Goal:** Source-level conflicts, graduated scoring.
**Independent Test:** `go test ./pkg/investigate/ -run Conflict` passes.

- [x] T007 Update `pkg/investigate/assess.go` — add ConflictingArea struct (Area, Score, Findings), source-level conflict detection, graduated scoring (P0vsP3=3, P0vsP2=2, P1vsP2=1), maintain backward compat in AssessResult
- [x] T008 [P] Update `pkg/investigate/assess_test.go` — Tests: source-level conflict, graduated scoring, no-conflict case, backward compat of string array

---

**Checkpoint:** ConflictingAreas enhanced with richer data.

## Phase 4: Cleanup + Polish

- [x] T009 Full regression: `go build ./... && go vet ./... && go test ./... -timeout 300s`
- [x] T010 Update `.agent/CONTINUITY.md` with completed Sprint 3

## Dependencies

- T001 blocks T003, T004, T005 (context module before strategy integration)
- T002, T006, T008 independent [P]
- T007 independent of T001-T006
- T009-T010 require all phases complete

## Execution Strategy

- **Phase 1 + Phase 3 can run in parallel** (different packages)
- T001 → T003-T005 sequential
- T007 independent
- One commit per phase
