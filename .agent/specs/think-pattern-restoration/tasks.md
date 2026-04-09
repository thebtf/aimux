# Tasks: Think Pattern Restoration

**Spec:** .agent/specs/think-pattern-restoration/spec.md
**Plan:** .agent/specs/think-pattern-restoration/plan.md
**Generated:** 2026-04-09

**Phase mapping (plan → tasks):** Plan Phase 1 = Tasks Phase 1, Plan Phase 2 = Tasks Phase 2-3, Plan Phase 3 = Tasks Phase 4, Plan Phase 4 = Tasks Phase 5, Plan Phase 5 = Tasks Phase 6.

## Phase 0: Planning

- [x] P001 Analyze tasks, assign executors (MAIN for audit/research, codex for Go code)
  AC: all tasks reviewed · executor assigned per task · no unassigned tasks remain

## Phase 1: Audit Matrix (US3)

**Goal:** Produce comprehensive comparative audit of all 23 v3 patterns vs v2 and original.
**Independent Test:** Audit report exists at .agent/research/think-pattern-audit.md with 23 entries.

- [x] T001 [US3] Read all 23 v3 pattern files in `pkg/think/patterns/`, extract: line count, required fields, computed outputs, STUB classification
  AC: 23 entries collected · each has line count + field list + output fields · stored in working notes
- [x] T002 [P] [US3] Read all 17 v2 pattern files in `D:\Dev\mcp-aimux\src\think\patterns\`, extract: line count, features (DAG, graph, state machine, etc.)
  AC: 17 entries collected · features described per pattern · stored in working notes
- [x] T003 [P] [US3] Read all 16 original pattern files in `D:\Dev\_EXTRAS_\thinking-patterns\src\servers\`, extract: line count, features (Redis, sessions, metrics)
  AC: 16 entries collected · features described per pattern · stored in working notes
- [x] T004 [US3] Produce comparative matrix in `.agent/research/think-pattern-audit.md`: pattern name, v3/v2/orig lines, features lost v2→v3, features lost orig→v2, STUB-PASSTHROUGH classification
  AC: file exists · 23 rows (all v3 patterns) · columns: pattern, v3_lines, v2_lines, orig_lines, lost_v2_to_v3, lost_orig_to_v2, stub_class · prioritized restoration order

- [x] G001 VERIFY Phase 1 (T001–T004) — BLOCKED until T001–T004 all [x]
  RUN: Read .agent/research/think-pattern-audit.md. Verify 23 entries present.
  CHECK: Every pattern classified. Priority order matches spec C5. No patterns missed.
  ENFORCE: Audit is COMPLETE — no "TODO" or "need to check" entries.
  RESOLVE: Fix gaps before marking [x].

---

**Checkpoint:** Audit matrix complete. Restoration priority established.

## Phase 2: Restore Top 4 Patterns (US1, priority 1-4)

**Goal:** Restore v2 computational logic to problem_decomposition, domain_modeling, architecture_analysis, stochastic_algorithm.
**Independent Test:** `go test ./pkg/think/patterns/ -run "TestProblemDecomp|TestDomainModel|TestArchAnalysis|TestStochastic" -v` passes with logic-verifying tests.

- [x] T005 [US1] Restore DAG analysis in `pkg/think/patterns/problem_decomposition.go`: port DFS cycle detection, Kahn's topological sort, orphan detection from v2 (`D:\Dev\mcp-aimux\src\think\patterns\problem-decomposition.ts` lines 63-164)
  AC: extractDagDependencies parses [{from,to}] edges · analyzeDag detects cycles via DFS · topological sort via Kahn's · orphans identified · Handle returns hasCycle/cyclePath/topologicalOrder/orphanSubProblems · 5 tests: acyclic→topo order, cyclic→cycle path, orphans detected, empty deps→no DAG, mixed→correct · swap body→return null ⇒ tests MUST fail
- [x] T006 [P] [US1] Restore entity graph in `pkg/think/patterns/domain_modeling.go`: port entity relationship graph, adjacency matrix, connected components from v2 (`D:\Dev\mcp-aimux\src\think\patterns\domain-modeling.ts`)
  AC: builds adjacency matrix from entities+relationships · counts connected components · detects isolated entities · computes relationship density · 4 tests: connected graph, disconnected, single entity, complex relationships · swap body→return null ⇒ tests MUST fail
- [x] T007 [P] [US1] Restore layering analysis in `pkg/think/patterns/architecture_analysis.go`: port fan-in/fan-out importance ranking, layering detection from v2 (`D:\Dev\mcp-aimux\src\think\patterns\architecture-analysis.ts`)
  AC: computes fan-in (how many depend on X) AND fan-out (how many X depends on) · importance = fan-in × weight · detects layering violations (lower layer depends on upper) · 4 tests: clean layers, violation detected, fan-in ranking, isolated component · swap body→return null ⇒ tests MUST fail
- [x] T008 [P] [US1] Restore risk assessment in `pkg/think/patterns/stochastic_algorithm.go`: port full EV calculation, variance, risk scoring from v2 (`D:\Dev\mcp-aimux\src\think\patterns\stochastic-algorithm.ts`)
  AC: expectedValue = sum(probability × outcome) · variance = sum(probability × (outcome-EV)²) · riskScore = variance / |EV| · 3 tests: known EV, variance computation, zero-EV edge case · swap body→return null ⇒ tests MUST fail

- [x] G002 VERIFY Phase 2 (T005–T008) — BLOCKED until T005–T008 all [x]
  RUN: `go test ./pkg/think/patterns/ -v -run "TestProblemDecomp|TestDomainModel|TestArchAnalysis|TestStochastic" -count=1`. `go vet ./pkg/think/...`.
  CHECK: Anti-STUB: for each pattern, different inputs produce DIFFERENT outputs. No pattern returns only counts.
  ENFORCE: Every restored function has ≥3 tests. Coverage >80% for modified files.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Top 4 patterns restored with real computational logic.

## Phase 3: Restore Bottom 4 Patterns (US1, priority 5-8)

**Goal:** Restore v2 logic to temporal_thinking, collaborative_reasoning, sequential_thinking, scientific_method.
**Independent Test:** `go test ./pkg/think/patterns/ -run "TestTemporal|TestCollab|TestSequential|TestScientific" -v` passes.

- [x] T009 [P] [US1] Restore timeline construction in `pkg/think/patterns/temporal_thinking.go`: port event ordering, timeline gaps, temporal density from v2 (`D:\Dev\mcp-aimux\src\think\patterns\temporal-thinking.ts`)
  AC: events sorted chronologically · gaps between events detected · temporal density computed (events/timespan) · 3 tests: ordered events, gap detection, overlapping events · swap body→return null ⇒ tests MUST fail
- [x] T010 [P] [US1] Restore voting/consensus in `pkg/think/patterns/collaborative_reasoning.go`: port perspective aggregation, agreement detection, conflict identification from v2 (`D:\Dev\mcp-aimux\src\think\patterns\collaborative-reasoning.ts`)
  AC: perspectives aggregated with weights · agreement score computed · conflicts identified (opposing positions) · consensus threshold detection · 3 tests: full agreement, partial conflict, no consensus · swap body→return null ⇒ tests MUST fail
- [x] T011 [P] [US1] Restore branch tracking in `pkg/think/patterns/sequential_thinking.go`: port thought chain navigation, branch detection, revision tracking from v2 (`D:\Dev\mcp-aimux\src\think\patterns\sequential-thinking.ts`)
  AC: thought chains tracked with sequence numbers · branch points detected (thought revises earlier thought) · total chain length computed · 3 tests: linear chain, branching, revision · swap body→return null ⇒ tests MUST fail
- [x] T012 [P] [US1] Restore experiment depth in `pkg/think/patterns/scientific_method.go`: port hypothesis lifecycle tracking, confidence progression from v2 (`D:\Dev\mcp-aimux\src\think\patterns\scientific-method.ts`)
  AC: hypothesis state tracked (proposed→tested→confirmed/refuted) · confidence changes per experiment · experiment count per hypothesis · 3 tests: confirmed hypothesis, refuted, multiple experiments · swap body→return null ⇒ tests MUST fail

- [x] G003 VERIFY Phase 3 (T009–T012) — BLOCKED until T009–T012 all [x]
  RUN: `go test ./pkg/think/patterns/ -v -count=1`. `go vet ./pkg/think/...`.
  CHECK: Anti-STUB for all 8 restored patterns. Different inputs → different outputs.
  ENFORCE: Zero regressions on unmodified patterns. Coverage >80%.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** All 8 priority patterns restored.

## Phase 4: EnableSampling PoC (US2)

**Goal:** Add MCP sampling capability so patterns can request LLM calls. PoC: problem_decomposition auto-decomposes when subProblems not provided.
**Independent Test:** `go test ./pkg/think/ -run TestSampling -v` passes. Server starts with sampling enabled.

- [x] T013 [US2] Create `pkg/think/sampling.go`: SamplingProvider interface (RequestSampling method), SamplingAwareHandler interface extending PatternHandler
  AC: SamplingProvider interface with RequestSampling(ctx, messages, maxTokens) (string, error) · SamplingAwareHandler with SetSampling(SamplingProvider) · compiles · swap body→return null ⇒ tests MUST fail
- [x] T014 [US2] Add `EnableSampling()` call in `pkg/server/server.go` New() function. Wire sampling provider from mcp-go into think patterns via SamplingAwareHandler interface
  AC: mcpServer.EnableSampling() called · SamplingProvider adapter wraps mcpServer.RequestSampling · patterns implementing SamplingAwareHandler receive the provider · go build passes · 1 test: server starts with sampling enabled
- [x] T015 [US2] Implement sampling-powered decomposition in `pkg/think/patterns/problem_decomposition.go`: when subProblems empty AND sampling available, use RequestSampling to generate decomposition, then run DAG analysis on result
  AC: with subProblems provided → DAG analysis as before (no sampling) · without subProblems + sampling → LLM generates decomposition → DAG runs on generated data · without subProblems + no sampling → returns basic result (graceful degrade) · 3 tests · swap body→return null ⇒ tests MUST fail

- [x] G004 VERIFY Phase 4 (T013–T015) — BLOCKED until T013–T015 all [x]
  RUN: `go build ./...`. `go test ./pkg/think/ -v -count=1`. `go test ./pkg/server/ -count=1`.
  CHECK: EnableSampling called. problem_decomposition works in all 3 modes.
  ENFORCE: Zero regressions. Graceful degradation when sampling unavailable.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Sampling PoC working. Patterns can request LLM reasoning.

## Phase 5: Schema Verification + Anti-Stub Gate (FR-5, FR-6)

**Goal:** End-to-end verification that all patterns work via MCP with correct params. Anti-STUB pass on all 8 restored patterns.
**Independent Test:** `go test ./pkg/think/... -cover` shows >80%. All 23 patterns callable via MCP schema.

- [x] T016 Verify all 23 patterns callable via MCP schema: for each pattern, construct valid MCP call with all required params (including JSON-string params for structured inputs). Document any pattern that fails.
  AC: 23 patterns tested · 0 failures · JSON-string params (criteria, options, components, sources, findings) parsed correctly · results documented
- [x] T017 [P] Anti-STUB-PASSTHROUGH verification for all 8 restored patterns: for each, write a test that calls with input A, then input B, and asserts outputs differ meaningfully (not just counts)
  AC: 8 patterns verified · each has anti-stub test · different inputs produce different computed fields (not just echoed input) · swap body→return null ⇒ tests MUST fail
- [x] T018 [P] Coverage gate: `go test ./pkg/think/... -cover` must show >80% for patterns/
  AC: coverage >80% · any uncovered paths identified and tested

- [x] G005 VERIFY Phase 5 (T016–T018) — BLOCKED until T016–T018 all [x]
  RUN: `go test ./pkg/think/... -v -cover -count=1`. `go vet ./pkg/think/...`.
  CHECK: 23/23 patterns callable. 8/8 anti-stub verified. Coverage >80%.
  ENFORCE: Zero regressions. No STUB-PASSTHROUGH remaining.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** All patterns verified. Anti-stub gate passed.

## Phase 6: Polish

- [x] T019 Update `.agent/research/think-pattern-audit.md` with post-restoration status (mark restored patterns)
  AC: audit matrix updated · restored patterns marked with new line counts · remaining gaps documented
- [x] T020 [P] Run full regression: `go test ./... -timeout 300s` + `go vet ./...` + build binary
  AC: all tests pass · no vet warnings · binary builds

- [x] G006 VERIFY Phase 6 (T019–T020) — BLOCKED until T019–T020 all [x]
  RUN: `go test ./... -timeout 300s`. `go vet ./...`. `go build ./cmd/aimux/`.
  CHECK: Zero regressions across entire codebase. Audit report current.
  ENFORCE: No dead code. No unused imports.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Core restoration done. Remaining gaps below.

## Phase 7: Remaining Patterns (P4-P8 from audit)

- [x] T021 [P] Restore visual_reasoning in `pkg/think/patterns/visual_reasoning.go`: port element histogram, isolated elements, graph density from v2
  AC: elementsByType histogram computed · isolated elements detected · density = relationships/(n*(n-1)/2) · 3 tests · swap body→return null ⇒ tests MUST fail
- [x] T022 [P] Restore metacognitive_monitoring in `pkg/think/patterns/metacognitive_monitoring.go`: port confidence calibration formula from v2
  AC: calibratedConfidence = clamp(raw - uncertaintyPenalty - biasPenalty, 0, 1) · overconfident detection (claims<3 AND calibrated>0.8) · 3 tests · swap body→return null ⇒ tests MUST fail
- [x] T023 [P] Restore mental_model in `pkg/think/patterns/mental_model.go`: port completeness/clarity/coherence scoring from original
  AC: completenessScore from text length · clarityScore from step count · complexity classification (low/medium/high) · 3 tests · swap body→return null ⇒ tests MUST fail
- [x] T024 [P] Restore recursive_thinking in `pkg/think/patterns/recursive_thinking.go`: port depth arithmetic from v2
  AC: depthRemaining = max(0, maxDepth-currentDepth) · depthPercentage = (currentDepth/maxDepth)*100 · convergenceWarning if depth>3 · 3 tests · swap body→return null ⇒ tests MUST fail

- [x] G007 VERIFY Phase 7 (T021–T024) — BLOCKED until T021–T024 all [x]
  RUN: `go test ./pkg/think/patterns/ -v -count=1`. Anti-stub: different inputs → different outputs.
  ENFORCE: Zero regressions. All 12 priority patterns now REAL.
  RESOLVE: Fix ALL findings before marking [x].

---

## Phase 8: Bug Fixes + Integration Verification

- [x] T025 Fix collaborative_reasoning silent persona detection: accept `personas` list in input, pre-seed count map, detect silents correctly
  AC: input with personas=["alice","bob"] + contributions only from alice → silentPersonas=["bob"] · test verifies detection · swap body→return null ⇒ tests MUST fail
- [x] T026 Verify EnableSampling with real MCP client: build aimux binary, connect via stdio, call think(pattern="problem_decomposition", problem="...") without subProblems, confirm sampling request sent
  AC: binary starts · sampling capability advertised in init response · test documented even if client doesn't support sampling yet (graceful degradation verified)

- [x] G008 VERIFY Phase 8 (T025–T026) — BLOCKED until T025–T026 all [x]
  RUN: `go test ./pkg/think/... -v -count=1`. Build binary. Manual verification.
  ENFORCE: collaborative_reasoning silent detection works. Sampling graceful degradation confirmed.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Feature complete. All gaps closed. Ready for PR merge.

## Dependencies

- G001 (Phase 1) blocks Phase 2+ (audit needed before restoration)
- G002 (Phase 2) and G003 (Phase 3) are independent — can parallelize
- G002 + G003 block Phase 4 (sampling needs restored patterns)
- G004 (Phase 4) blocks Phase 5 (verification needs all features)
- G005 blocks Phase 6 (polish after verification)
- Within Phase 2: T005-T008 are [P] (parallel — different files)
- Within Phase 3: T009-T012 are [P] (parallel — different files)

## Execution Strategy

- **MVP scope:** Phase 0-3 (audit + 8 pattern restorations)
- **Parallel opportunities:** T002||T003, T005-T008 (all 4 top patterns), T009-T012 (all 4 bottom patterns)
- **Commit strategy:** One commit per pattern restored. One PR for Phase 2, one for Phase 3.
- **Agent delegation:** T005-T012 (8 pattern restorations) are ideal for codex via aimux — each reads v2 source and ports to Go.
