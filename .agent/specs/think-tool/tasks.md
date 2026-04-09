# Tasks: Think Tool — Full Pattern System

**Spec:** .agent/specs/think-tool/spec.md
**Plan:** .agent/specs/think-tool/plan.md
**Generated:** 2026-04-07

## Phase 1: Foundation — Types, Registry, Session, Complexity

**Goal:** Core infrastructure for pattern system. All types, registry lookup, session management, complexity scoring.
**Independent Test:** `go test ./pkg/think/` passes with registry operations, session CRUD, complexity scoring.

- [x] T001 Create `pkg/think/types.go` — ThinkResult struct, PatternHandler interface, ThinkSession struct, ComplexityScore struct, DialogConfig struct, DialogParticipant struct, MakeThinkResult helper function
- [x] T002 Create `pkg/think/registry.go` — Map-based pattern registry: RegisterPattern, GetPattern, GetAllPatterns, ClearPatterns. Duplicate registration panics.
- [x] T003 Create `pkg/think/session.go` — In-memory session store with sync.RWMutex: GetOrCreateSession, GetSession, UpdateSessionState, DeleteSession, ClearSessions, GetSessionCount. Immutable updates (new copy each time).
- [x] T004 Create `pkg/think/complexity.go` — CalculateComplexity function with 4 components: textLength (0.3), subItemCount (0.3), structuralDepth (0.2), patternBias (0.2). Returns ComplexityScore with recommendation.
- [x] T005 Create `pkg/think/dialog_config.go` — GetDialogConfig(pattern) returning config for 12 patterns, BuildDialogTopic and BuildPatternDialogPrompt template interpolation, GetDialogPatterns list.
- [x] T006 [P] Create `pkg/think/registry_test.go` — Tests: register, get, get missing, list all, clear, duplicate panics
- [x] T007 [P] Create `pkg/think/session_test.go` — Tests: getOrCreate new, getOrCreate existing, get missing, update state, delete, clear, count, immutability verification
- [x] T008 [P] Create `pkg/think/complexity_test.go` — Tests: empty input (0 score), text length scoring, sub-item count scoring, structural depth scoring, pattern bias effect, threshold comparison, solo vs consensus recommendation
- [x] T009 [P] Create `pkg/think/dialog_config_test.go` — Tests: get config for known pattern, get nil for unknown, template interpolation, list dialog patterns

---

**Checkpoint:** `go test ./pkg/think/` passes. Foundation fully tested.

## Phase 2: Stateless Patterns (11 handlers)

**Goal:** All 11 stateless patterns implemented with validation and handling.
**Independent Test:** Each pattern validates required fields and returns computed data.

- [x] T010 Create `pkg/think/patterns/think.go` — Base "think" pattern: requires thought string, returns thought + thoughtLength
- [x] T011 Create `pkg/think/patterns/critical_thinking.go` — Bias detection: requires issue string, BIAS_CATALOGS with trigger phrases (confirmation_bias, anchoring, sunk_cost, availability_heuristic + more), returns analysis with detected biases
- [x] T012 Create `pkg/think/patterns/decision_framework.go` — Weighted scoring: requires decision + criteria + options, computeWeightedScores with normalization, ranking with tie detection
- [x] T013 Create `pkg/think/patterns/problem_decomposition.go` — Problem breakdown: requires problem string, optional methodology/subProblems/dependencies/risks/stakeholders
- [x] T014 Create `pkg/think/patterns/mental_model.go` — 15 model catalog: requires modelName + problem, known models with descriptions, unknown models accepted
- [x] T015 Create `pkg/think/patterns/metacognitive_monitoring.go` — Overconfidence detection: requires task, optional claims/cognitiveProcesses/biases/uncertainties/confidence, flags confidence > 0.8 with < 3 claims
- [x] T016 Create `pkg/think/patterns/recursive_thinking.go` — Recursion analysis: requires problem, optional baseCase/recursiveCase/currentDepth/maxDepth/convergenceCheck
- [x] T017 Create `pkg/think/patterns/domain_modeling.go` — Entity/relationship: requires domainName, optional entities/relationships/rules/constraints/description
- [x] T018 Create `pkg/think/patterns/architecture_analysis.go` — ATAM-lite: requires components (string[] or []Component), coupling detection, importance levels (H/M/L), normalizeComponents helper
- [x] T019 Create `pkg/think/patterns/stochastic_algorithm.go` — Algorithm analysis: requires algorithmType (mdp/mcts/bandit/bayesian/hmm) + problemDefinition, optional parameters/iterations/result
- [x] T020 Create `pkg/think/patterns/temporal_thinking.go` — Temporal analysis: requires timeFrame, optional states/events/transitions/constraints/analysis
- [x] T021 Create `pkg/think/patterns/visual_reasoning.go` — Spatial analysis: requires operation, optional diagramType/elements/relationships/transformations/description
- [x] T022 [P] Create `pkg/think/patterns/stateless_test.go` — Tests for all 11 stateless patterns: validation success/failure, handle returns correct data, no echo strings

---

**Checkpoint:** All 11 stateless patterns pass tests. Each returns computed data.

## Phase 3: Stateful Patterns (6 handlers)

**Goal:** All 6 stateful patterns with session management.
**Independent Test:** Each pattern maintains state across calls, returns different data per call.

- [x] T023 Create `pkg/think/patterns/sequential_thinking.go` — Thought history with branches: requires thought, optional thoughtNumber/totalThoughts/isRevision/revisesThought/branchFromThought/branchId. Jaccard word similarity helper. Session state: thought entries + branches.
- [x] T024 Create `pkg/think/patterns/scientific_method.go` — Hypothesis lifecycle: requires stage (7 valid stages), supports lifecycle entries (hypothesis/prediction/experiment/result) with linking validation. Session state: stageHistory + hypothesesHistory + entries.
- [x] T025 Create `pkg/think/patterns/debugging_approach.go` — 18 methods catalog + hypothesis tracking: requires issue + approachName, hypothesis lifecycle (untested→tested→confirmed/refuted). Session state: hypotheses list.
- [x] T026 Create `pkg/think/patterns/structured_argumentation.go` — Argument graph: requires topic, optional argument (claim/evidence/rebuttal with supportsClaimId). Session state: accumulated arguments with linking validation.
- [x] T027 Create `pkg/think/patterns/collaborative_reasoning.go` — Multi-stage reasoning: requires topic, 6 stages + 7 contribution types. Session state: contributions + stage progress.
- [x] T028 [P] Create `pkg/think/patterns/stateful_test.go` — Tests for all 6 stateful patterns: session creation, state accumulation, cross-call persistence, validation of links/references, branch handling

---

**Checkpoint:** All 17 patterns fully tested. Stateful patterns maintain state correctly.

## Phase 4: Server Wiring + Integration

**Goal:** handleThink dispatches to real patterns. MCP schema updated. All tests pass.
**Independent Test:** Full lifecycle test: think(pattern=X, input=Y) → real computed result via handler.

- [x] T029 Update `pkg/server/server.go` — Rewrite handleThink: extract all params into input map, registry lookup, validate, handle, compute complexity, return ThinkResult as JSON. Import think package. Add pattern initialization in New().
- [x] T030 Update `pkg/server/server.go` — Update MCP tool registration for think: add params for thought, decision, criteria, options, session_id, mode, etc.
- [x] T031 [P] Update handler tests in `pkg/server/handler_test.go` — Update existing think tests to match new response format (ThinkResult with data, not echo string)
- [x] T032 Create `pkg/think/patterns/init.go` — RegisterAll() function that registers all 17 patterns. Called from server.New().

---

**Checkpoint:** handleThink returns real pattern results. All 280+ tests pass.

## Phase 5: Cleanup + Polish

- [x] T033 Full regression: `go build ./... && go vet ./... && go test ./... -timeout 300s`
- [x] T034 Update `.agent/CONTINUITY.md` with completed Sprint 1

## Dependencies

- T001 blocks T002, T003, T004, T005 (types before everything)
- T002 blocks T010-T021, T023-T027 (registry before patterns)
- T003 blocks T023-T027 (session before stateful patterns)
- T004 blocks T029 (complexity before server wiring)
- T005 blocks T004 (dialog config provides pattern bias for complexity)
- T010-T021 block T029 (patterns before server wiring)
- T023-T027 block T029 (all patterns before server wiring)
- T032 blocks T029 (init before server wiring)
- T006-T009 independent [P] (tests parallel with Phase 2)
- T022, T028, T031 independent [P] (tests parallel with next phase)
- T033-T034 require Phase 4 complete

## Execution Strategy

- **Phase 1:** T001 → T002-T005 (sequential) + T006-T009 (parallel tests)
- **Phase 2:** T010-T021 can be batched (stateless, independent). T022 parallel.
- **Phase 3:** T023-T027 sequential (shared session dependency). T028 parallel.
- **Phase 4:** T032 → T029-T031 (sequential)
- **Commit strategy:** One commit per phase (5 total)
