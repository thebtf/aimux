# Tasks: Hooks + Validation (Sprint 4)

**Generated:** 2026-04-07

## Phase 1: Turn Validator

**Goal:** Post-execution content quality checks — catches garbage output before returning to caller.

- [x] T001 Create `pkg/executor/validate.go` — TurnValidation struct (Valid bool, Warnings []string, Errors []string), ValidateTurnContent(content, stderr string, exitCode int) TurnValidation. Checks: empty output (exit 0 but empty → invalid), too short (<10 chars → warning), error patterns in stderr (rate_limit, quota_exceeded, 429, auth_failure, connection_refused, ECONNREFUSED, ETIMEDOUT, ENOTFOUND → invalid), refusal patterns in first 200 chars ("I cannot", "I can't", "I am unable", "I don't have access" → warning).
- [x] T002 [P] Create `pkg/executor/validate_test.go` — Tests: valid content, empty content, short content, rate limit error, auth failure, connection error, refusal detection, stderr error with valid stdout
- [x] T003 Wire ValidateTurnContent into `pkg/server/server.go:executeJob` — after executor.Run, validate result, add warnings to response metadata, return error for invalid results

---

## Phase 2: Quality Gate

**Goal:** Orchestrator-level retry/escalate/halt logic based on turn validation.

- [x] T004 Create `pkg/orchestrator/quality_gate.go` — QualityGate struct with maxRetries (default 2), per-participant retry tracking. Evaluate(cli, content, stderr, exitCode) returns action: "continue", "retry", "escalate", "halt". Empty/error → retry, refusal → escalate, max retries → halt. Uses ValidateTurnContent.
- [x] T005 [P] Create `pkg/orchestrator/quality_gate_test.go` — Tests: continue on valid, retry on empty, retry on error, escalate on refusal, halt on max retries
- [x] T006 Integrate QualityGate into SequentialDialog.Execute — wrap executor.Run with quality gate retry loop
- [x] T007 Integrate QualityGate into ParallelConsensus.Execute — evaluate each response, retry failed participants
- [x] T008 Integrate QualityGate into StructuredDebate.Execute — wrap with quality gate

---

## Phase 3: Hooks System

**Goal:** Before/after execution pipeline for cross-cutting concerns.

- [x] T009 Create `pkg/hooks/types.go` — BeforeHookContext, AfterHookContext, BeforeHookResult (proceed/block/skip), AfterHookResult (accept/annotate/reject), HookFn types
- [x] T010 Create `pkg/hooks/registry.go` — HookRegistry with RegisterBefore, RegisterAfter, Remove, RunBefore (sequential pipeline with short-circuit on block/skip), RunAfter (sequential with short-circuit on reject), timeout protection per hook (default 5s)
- [x] T011 Create `pkg/hooks/builtins.go` — builtin:telemetry after hook (logs cli, exitCode, durationMs to logger)
- [x] T012 [P] Create `pkg/hooks/registry_test.go` — Tests: register+run before proceed, before block short-circuits, before skip with synthetic, after accept, after reject, after annotate, timeout protection, remove hook
- [x] T013 Integrate hooks into Server — add HookRegistry to Server struct, run before hooks pre-exec, run after hooks post-exec in handleExec

---

## Phase 4: Cleanup + Polish

- [x] T014 Full regression: `go build ./... && go vet ./... && go test ./... -timeout 300s`
- [x] T015 Update `.agent/CONTINUITY.md` with completed Sprint 4

## Dependencies

- T001 blocks T003, T004 (validator before wiring and quality gate)
- T004 blocks T006-T008 (quality gate before strategy integration)
- T009 blocks T010-T013 (types before registry and integration)
- Phases 1-3 can partially overlap (hooks are independent of validator)
