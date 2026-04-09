# Tasks: Investigate Tool — Full Port + Enhancements

**Spec:** .agent/specs/investigate-enhancements/spec.md
**Plan:** .agent/specs/investigate-enhancements/plan.md
**Generated:** 2026-04-06

## Phase 1: Types + State — Foundation

**Goal:** Investigation state management in pure Go. Findings with confidence, corrections, domain-aware coverage.
**Independent Test:** `go test ./pkg/investigate/` passes with state creation, finding add, dedup, correction chain.

- [x] T001 Create `pkg/investigate/types.go` — Finding (with Confidence field), Correction, InvestigationState, AssessResult, DomainAlgorithm, Severity/Confidence type aliases
- [x] T002 Create `pkg/investigate/domains.go` — GenericDomain + DebuggingDomain structs with coverageAreas, methods, angles, patterns, antiPatterns. GetDomain(name) registry function.
- [x] T003 Create `pkg/investigate/state.go` — In-memory map with RWMutex: CreateInvestigation, GetInvestigation, ListInvestigations, AddFinding (with dedup + correction chain), NextIteration, DeleteInvestigation
- [x] T004 [P] Create `pkg/investigate/state_test.go` — Tests: create, get, addFinding, dedup, correction, coverage update, nextIteration, list, delete

---

**Checkpoint:** `go test ./pkg/investigate/` passes. State management works with findings, corrections, coverage tracking.

## Phase 2: Assess + Report — Core Logic

**Goal:** Convergence computation, gap tracking, markdown report with all enhanced sections.
**Independent Test:** assess returns correct convergence/coverage/recommendation. Report generates valid markdown with skepticism + takeaways.

- [x] T005 Create `pkg/investigate/assess.go` — ComputeConvergence, ComputeCoverage, Assess function. Returns: convergenceScore, coverageScore, recommendation (CONTINUE/MAY_STOP/COMPLETE), uncheckedAreas, weakAreas (NEW), conflictingAreas (NEW), angle rotation, think suggestions, antiPatternWarnings, patternHints. Adversarial prompt when MAY_STOP + P0.
- [x] T006 [P] Create `pkg/investigate/assess_test.go` — Tests: convergence calc (0 findings, first iter, with corrections, no corrections), coverage calc, recommendation logic, weakAreas detection, conflictingAreas detection, angle rotation, adversarial suggestion
- [x] T007 Create `pkg/investigate/report.go` — GenerateReport: header (metadata: timestamp, model, session, coverage, confidence aggregate), findings table (with Confidence column), corrections, "What to Be Skeptical Of" (auto from INFERRED + low coverage), coverage map, convergence history, "Key Takeaways" (auto: root cause, recommendation, assumption). Completeness warning at <80%. SaveReport to .agent/reports/.
- [x] T008 [P] Create `pkg/investigate/report_test.go` — Tests: report with findings, report with corrections, skepticism section present, takeaways section present, metadata present, completeness warning, empty investigation report

---

**Checkpoint:** assess + report fully tested. Markdown output correct.

## Phase 3: Server Wiring + Schema — Integration

**Goal:** handleInvestigate dispatches all 7 actions. MCP schema updated with finding params.
**Independent Test:** Full lifecycle test: start → finding → finding → assess → report via handler.

- [x] T009 Update `pkg/server/server.go` — Rewrite handleInvestigate: dispatch start/finding/assess/report/status/list/recall to pkg/investigate functions. Import investigate package. Add investigate state to Server struct initialization.
- [x] T010 Update `pkg/server/server.go` — Update MCP tool registration for investigate: add params for description, source, severity, confidence, corrects, coverageArea, cwd
- [x] T011 [P] Add handler tests in `pkg/server/handler_test.go` — Tests: investigate start, finding, finding with confidence, assess, report, status, list, recall, finding without session_id error

---

**Checkpoint:** All 7 actions work via MCP protocol. Handler tests pass.

## Phase 4: Cleanup + Polish

- [x] T012 Delete `scripts/check-parity.sh` and `scripts/side-by-side.sh` (legacy v2 scripts)
- [x] T013 Full regression: `go build ./... && go vet ./... && go test ./... -timeout 300s`
- [x] T014 Update `.agent/CONTINUITY.md` with completed feature

## Dependencies

- T001 blocks T002, T003 (types before state + domains)
- T003 blocks T005 (state before assess)
- T005 blocks T007 (assess before report — report uses assess data)
- T003, T005, T007 block T009 (all logic before server wiring)
- T009 blocks T010 (handler before schema)
- T004, T006, T008 independent [P] (tests parallel with next phase)
- T012-T014 require Phase 3 complete

## Execution Strategy

- **MVP scope:** Phase 1-3 (T001-T011) — full investigate port + enhancements
- **Parallel opportunities:** T004||T005, T006||T007, T008||T009, T011 parallel with T010
- **Commit strategy:** One commit per phase (4 total)
