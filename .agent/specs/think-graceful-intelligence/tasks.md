# Tasks: Think Patterns — Graceful Intelligence

**Spec:** .agent/specs/think-graceful-intelligence/spec.md
**Plan:** .agent/specs/think-graceful-intelligence/plan.md
**Generated:** 2026-04-09

## Phase 0: Planning

- [x] P001 Analyze tasks and assign executors
  AC: all tasks reviewed · executor assigned per task · no unassigned tasks remain

## Phase 1: Foundation

- [x] T001 Create `pkg/think/patterns/templates.go` with domain template registry: 10+ templates (auth, api, database, deploy, test, frontend, backend, security, monitoring, data-pipeline)
  AC: 10+ templates defined as Go map literals · each has Keywords, SubProblems, Entities, Components, Dependencies · compiles · swap body→return null ⇒ tests MUST fail
- [x] T002 [P] Create `pkg/think/patterns/autoanalysis.go` with keyword extraction + domain matching: ExtractKeywords(text) []string, MatchDomain(keywords) *DomainTemplate, BuildGuidance(currentDepth, enrichments) Guidance
  AC: ExtractKeywords("Design authentication system") returns ["design","authentication","system"] · MatchDomain with auth keywords returns auth template · BuildGuidance returns struct with example call · 5 tests · swap body→return null ⇒ tests MUST fail

- [x] G001 VERIFY Phase 1 (T001–T002) — BLOCKED until T001–T002 all [x]
  RUN: `go test ./pkg/think/patterns/ -run "TestTemplate|TestAutoAnalysis|TestKeyword|TestDomain" -v`. `go build ./pkg/think/patterns/`.
  CHECK: 10+ templates. Keyword extraction works. Domain matching works.
  ENFORCE: Zero stubs. Templates have real sub-problems, not placeholders.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Foundation ready. Auto-analysis available for all patterns.

## Phase 2: Core 5 Patterns (US1 + US2 + US3 + US4)

**Goal:** Modify the 5 most visibly broken patterns to use auto-analysis when called with minimal input.
**Independent Test:** `think(problem_decomposition, problem="Design auth")` returns suggestedSubProblems.

- [x] T003 [US1] Modify `pkg/think/patterns/problem_decomposition.go` Handle(): when subProblems empty, call ExtractKeywords → MatchDomain → generate suggestedSubProblems + suggestedDependencies → run DAG on generated data → add guidance field
  AC: problem="Design auth system" → suggestedSubProblems has 4+ items · suggestedDependencies has 3+ items · dag analysis runs on generated data · guidance.currentDepth="basic" · existing tests still pass · swap body→return null ⇒ tests MUST fail
- [x] T004 [P] [US2] Modify `pkg/think/patterns/decision_framework.go` Validate()+Handle(): when criteria/options missing, don't error — suggest criteria from decision keywords, return template options, add guidance
  AC: decision="Choose database" without criteria → suggestedCriteria has 3+ items · suggestedOptions template returned · NO validation error · guidance has enriched call example · existing weighted scoring still works with full input · swap body→return null ⇒ tests MUST fail
- [x] T005 [P] [US3] Modify `pkg/think/patterns/domain_modeling.go` Handle(): when entities empty, call MatchDomain → suggest entities + relationships → run consistency analysis → add guidance
  AC: domainName="e-commerce" → suggestedEntities has 4+ items · suggestedRelationships has 3+ items · consistency analysis runs · guidance field present · existing tests pass · swap body→return null ⇒ tests MUST fail
- [x] T006 [P] [US4] Modify `pkg/think/patterns/architecture_analysis.go` Handle(): when 0-1 components, suggest additional components from keywords → add guidance
  AC: single component "API" → suggestedComponents has 3+ items (DB, Cache, Auth) · metrics computed for provided component · guidance shows enrichment options · existing tests pass · swap body→return null ⇒ tests MUST fail
- [x] T007 [P] Modify `pkg/think/patterns/temporal_thinking.go` Handle(): when events empty, extract time-related concepts from timeFrame text → add guidance
  AC: timeFrame="Q1 2026 migration plan" → suggestedPhases has 3+ items · guidance field present · existing timeline tests pass · swap body→return null ⇒ tests MUST fail

- [x] G002 VERIFY Phase 2 (T003–T007) — BLOCKED until T003–T007 all [x]
  RUN: `go test ./pkg/think/patterns/ -v -count=1`. Smoke test each pattern with minimal input.
  CHECK: All 5 return useful output with primary field only. No zeros. No empty arrays.
  ENFORCE: suggestedX fields have 3+ items. guidance field present. Existing tests pass.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Top 5 patterns return useful output with minimal input.

## Phase 3: Remaining 8 Patterns

- [x] T008 [P] Modify visual_reasoning Handle(): suggest elements from operation description
  AC: operation="layout" → suggestedElements returned · guidance present · existing tests pass
- [x] T009 [P] Modify stochastic_algorithm Handle(): suggest outcomes for bandit/bayesian when parameters.outcomes missing
  AC: bandit without outcomes → suggestedOutcomes template · guidance present · existing tests pass
- [x] T010 [P] Modify think.go Handle(): add keyword analysis to base thought
  AC: thought="How to scale API" → keywords extracted · suggestedPattern recommended · guidance present
- [x] T011 [P] Modify mental_model Handle(): add analysis output beyond model description lookup
  AC: modelName="first_principles" problem="reduce costs" → analysisSteps generated · guidance present
- [x] T012 [P] Modify recursive_thinking Handle(): add structure detection from problem text
  AC: problem="parse nested JSON" → recursiveStructure detected · guidance present
- [x] T013 [P] Modify metacognitive_monitoring Handle(): add self-assessment guidance when minimal input
  AC: task="evaluate options" → assessmentFramework suggested · guidance present
- [x] T014 Add guidance field to ALL stateful pattern first-call responses (collaborative_reasoning, sequential_thinking, scientific_method, structured_argumentation, experimental_loop, debugging_approach)
  AC: 6 patterns return guidance on first call · guidance explains how to add contributions/hypotheses/thoughts
- [x] T015 [P] Add guidance field to ALL already-REAL patterns (critical_thinking, peer_review, literature_review, research_synthesis, source_comparison, replication_analysis)
  AC: 6 patterns return guidance field · guidance shows available enrichments

- [x] G003 VERIFY Phase 3 (T008–T015) — BLOCKED until T008–T015 all [x]
  RUN: `go test ./pkg/think/... -v -count=1 -cover`.
  CHECK: All 23 patterns return guidance. No pattern returns empty output with valid primary input.
  ENFORCE: Coverage > 80%. Zero regressions.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** All 23 patterns gracefully intelligent.

## Phase 4: Verification + Smoke Test

- [x] T016 Smoke test all 23 patterns via aimux-dev MCP with minimal input — document results
  AC: 23/23 return useful output · zero empty/zero results · results documented in .agent/research/
- [x] T017 [P] Full regression: `go test ./... -timeout 300s` + `go vet ./...` + build binary
  AC: all tests pass · no vet warnings · binary builds · binary starts

- [x] G004 VERIFY Phase 4 (T016–T017) — BLOCKED until T016–T017 all [x]
  RUN: Full test suite + smoke test.
  CHECK: Zero regressions. All 23 patterns verified via MCP.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Feature complete. Verified via smoke test. Ready for PR.

## Dependencies

- G001 blocks Phase 2+ (templates needed before patterns can use them)
- Phase 2 and Phase 3 are independent (different patterns, different files)
- G002 + G003 block Phase 4 (verification after all patterns updated)
- Within Phase 2: T003-T007 are [P] (parallel — different files)
- Within Phase 3: T008-T015 are [P] (parallel — different files)

## Execution Strategy

- **MVP scope:** Phase 0-2 (foundation + top 5 patterns)
- **Parallel:** T003-T007 (5 core patterns), T008-T015 (8 remaining)
- **Commit strategy:** One commit per phase
- **Agent delegation:** T003-T015 ideal for parallel sonnet subagents
