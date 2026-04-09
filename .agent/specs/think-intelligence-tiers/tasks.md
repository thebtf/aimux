# Tasks: Think Patterns Intelligence Tiers

**Spec:** .agent/specs/think-intelligence-tiers/spec.md
**Plan:** .agent/specs/think-intelligence-tiers/plan.md
**Generated:** 2026-04-09

## Phase 0: Planning

- [x] P001 Analyze tasks, assign executors
  AC: all tasks reviewed · executor assigned

## Phase 1: Text Analysis Engine (Tier 2A)

- [x] T001 Create `pkg/think/patterns/textanalysis.go`: AnalyzeText(text) returning TextAnalysis{Entities, Relationships, Gaps, Negations, Questions, Complexity}
  AC: extracts "OAuth2" from "Design auth with OAuth2" · detects "without" as negation · detects "?" as question · complexity="low" for <2 sentences, "high" for >5 · 6 tests · swap body→return null ⇒ tests MUST fail
- [x] T002 [P] Enhance `pkg/think/patterns/templates.go`: add ExpectedEntities field to DomainTemplate for gap detection
  AC: auth template has ExpectedEntities: [registration, login, tokens, sessions, logout, roles, password-reset] · all 10 templates have ExpectedEntities · compiles
- [x] T003 [P] Implement gap detection in textanalysis.go: DetectGaps(entities []string, domain *DomainTemplate) []Gap
  AC: "Design auth with login" + auth template → gaps include [tokens, sessions, logout, roles] · each gap has Why field · 3 tests · swap body→return null ⇒ tests MUST fail
- [x] T004 Integrate AnalyzeText into all 23 pattern Handle() functions: add textAnalysis field to output data
  AC: every pattern returns textAnalysis when primary text field provided · entities extracted · gaps shown for known domains · complexity estimated · 0 regressions on existing tests

- [x] G001 VERIFY Phase 1 (T001–T004) — BLOCKED until T001–T004 all [x]
  RUN: `go test ./pkg/think/... -v -count=1 -cover`. Smoke test: problem_decomposition("Design auth with login") returns textAnalysis.gaps.
  CHECK: Gaps detected. Entities extracted. Complexity estimated. Coverage >80%.
  ENFORCE: Zero regressions.
  RESOLVE: Fix ALL findings.

---

## Phase 2: Forced Reflection Protocol (Tier 2B)

- [x] T005 Create `pkg/think/patterns/reflection.go`: ReflectionDirective struct, ValidateStep(pattern, step, data) function
  AC: ReflectionDirective{Directive, Checklist, Reason} struct · ValidateStep returns "STOP" when debugging_approach has <3 evidence items before hypothesis · 3 tests · swap body→return null ⇒ tests MUST fail
- [x] T006 [P] Modify debugging_approach Handle(): add evidence gate — if hypothesis submitted with <3 findings in session, return STOP directive
  AC: hypothesis with 2 findings → reflection.directive="STOP" + checklist=["Collect more evidence"] · hypothesis with 4 findings → no STOP · confidence >0.8 with <5 findings → overconfidence warning · 3 tests
- [x] T007 [P] Modify scientific_method Handle(): add lifecycle enforcement — prediction requires linked hypothesis, experiment requires linked prediction
  AC: prediction without hypothesis → reflection.directive="STOP" + reason="No hypothesis to predict from" · correct chain → no STOP · 2 tests
- [x] T008 [P] Enhance MCP think tool schema descriptions in server.go: make field descriptions instructional (schema-as-scaffold from pal-mcp-server)
  AC: hypothesis field description includes "Concrete root cause theory based on evidence. Rate confidence 0-1." · issue field includes "Describe the problem, symptoms, and what you've already tried." · 5+ fields enhanced · go build passes

- [x] G002 VERIFY Phase 2 (T005–T008) — BLOCKED until T005–T008 all [x]
  RUN: `go test ./pkg/think/... -v -count=1`. Smoke test: debugging_approach with premature hypothesis returns STOP.
  CHECK: Evidence gates work. Schema descriptions instructional.
  ENFORCE: Zero regressions.
  RESOLVE: Fix ALL findings.

---

## Phase 3: Sampling Verification (Tier 3)

- [x] T009 Build aimux binary with sampling, connect via Claude Code, test problem_decomposition without subProblems — document whether sampling request reaches client
  AC: binary starts with sampling capability · test documented in .agent/research/ · graceful degradation verified if client doesn't support sampling
- [x] T010 [P] Create `pkg/think/patterns/sampling_prompts.go`: per-pattern prompt templates for sampling
  AC: problem_decomposition has decompose prompt template · peer_review has review rubric template · decision_framework has criteria suggestion template · 4+ templates · compiles

- [x] G003 VERIFY Phase 3 (T009–T010) — BLOCKED until T009–T010 all [x]
  RUN: `go build ./...`. Documentation reviewed.
  CHECK: Sampling tested with real client (or documented as blocked). Prompt templates ready.
  RESOLVE: Fix ALL findings.

---

## Phase 4: Sampling-Powered Patterns (Tier 3)

- [x] T011 [P] Enhance problem_decomposition: when sampling available + no template match, use sampling prompt to generate decomposition
  AC: novel domain ("quantum error correction") + sampling → real sub-problems returned · graceful degrade without sampling · source="sampling" tagged · 2 tests
- [x] T012 [P] Enhance peer_review: use sampling to generate real objections instead of keyword-matched categories
  AC: artifact with sampling → domain-specific objections · without sampling → existing keyword behavior · source tagged · 2 tests
- [x] T013 [P] Enhance decision_framework: use sampling to suggest criteria for unknown domains
  AC: novel decision + sampling → relevant criteria suggested · without sampling → generic fallback · 2 tests
- [x] T014 [P] Enhance critical_thinking: use sampling for nuanced bias detection beyond trigger phrases
  AC: "everyone is using it" + sampling → bandwagon detected · without sampling → existing trigger match · 2 tests

- [x] G004 VERIFY Phase 4 (T011–T014) — BLOCKED until T011–T014 all [x]
  RUN: `go test ./pkg/think/... -v -count=1 -cover`.
  CHECK: Sampling patterns produce richer output than Tier 1-2 alone. Graceful degradation works.
  ENFORCE: Zero regressions. Source provenance on all generated content.
  RESOLVE: Fix ALL findings.

---

## Phase 5: Composition + Tier Selection

- [x] T015 Formalize pattern composition: output of pattern A compatible as input to suggested pattern B. Test 3-pattern chain.
  AC: critical_thinking output → feeds decision_framework input via suggestedNextPattern · problem_decomposition → architecture_analysis with components from sub-problems · at least 1 chain tested end-to-end · 2 tests
- [x] T016 [P] Implement complexity-gated tier selection: auto-choose tier based on input analysis
  AC: short known-domain text → Tier 1 (<5ms) · longer text with gaps → Tier 2 (<10ms) · novel domain no template → Tier 3 if sampling available · `depth` param override works · 3 tests
- [x] T017 Full regression + smoke test all 23 patterns via aimux-dev MCP
  AC: all tests pass · binary builds · 23/23 patterns return useful output · smoke test documented

- [x] G005 VERIFY Phase 5 (T015–T017) — BLOCKED until T015–T017 all [x]
  RUN: Full test suite + smoke test.
  CHECK: Chain works. Tier selection correct. All patterns verified.
  RESOLVE: Fix ALL findings.

---

**Checkpoint:** All tiers implemented. Verified. Ready for PR.

## Dependencies

- G001 blocks Phase 2+ (text analysis needed for reflection)
- G001 blocks Phase 4 (gap detection informs sampling prompts)
- G002 independent of G003 (forced reflection doesn't need sampling)
- G003 blocks Phase 4 (sampling must be verified before using in patterns)
- G004 + G002 block Phase 5 (composition needs all tiers)
- Within phases: T001→T003→T004 sequential; T005-T008 parallel; T011-T014 parallel

## Execution Strategy

- **MVP:** Phase 0-2 (text analysis + forced reflection = Tier 2 complete)
- **Parallel:** T002||T003, T006||T007||T008, T011-T014
- **Commit:** one per phase, PR after Phase 2 and Phase 5
- **Delegation:** T001-T004 to codex (Go code), T005-T008 to codex, T009 manual
