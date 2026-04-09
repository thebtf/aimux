# Implementation Plan: Think Patterns Intelligence Tiers

**Spec:** .agent/specs/think-intelligence-tiers/spec.md
**Created:** 2026-04-09
**Status:** Draft

> **Provenance:** Planned by claude-opus-4-6 on 2026-04-09.
> Evidence from: spec.md (8 FR, 4 NFR), pal-mcp-server research, graceful intelligence PR #27.
> Confidence: VERIFIED (existing code, pal-mcp-server patterns) / INFERRED (tier implementation).

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Text analysis | Go regex + strings | No external NLP deps. <10ms. Sufficient for Tier 2. |
| Gap detection | Go map lookup | Domain template expected entities vs detected entities |
| Forced reflection | Go structs in response | Advisory — same mechanism as guidance field |
| Sampling | mcp-go EnableSampling | Already integrated (PR #26), needs production verification |
| Prompt templates | Go string templates | Per-pattern sampling prompts, compile-time |

## Architecture

```
pkg/think/patterns/
├── textanalysis.go       # NEW: AnalyzeText, entity/gap/complexity detection
├── textanalysis_test.go  # NEW
├── reflection.go         # NEW: ReflectionDirective, step validation
├── reflection_test.go    # NEW
├── templates.go          # EXISTS: domain templates (add expected entities for gaps)
├── autoanalysis.go       # EXISTS: keyword extraction (enhance with text analysis)
├── sampling_prompts.go   # NEW: per-pattern sampling prompt templates
│
├── problem_decomposition.go  # MODIFY: integrate Tier 2 + 3
├── debugging_approach.go     # MODIFY: add forced reflection protocol
├── decision_framework.go     # MODIFY: integrate Tier 2 + 3
├── critical_thinking.go      # MODIFY: integrate Tier 2 text analysis
├── peer_review.go            # MODIFY: integrate Tier 3 sampling
└── ... (other patterns — add Tier 2 text analysis)
```

## Phases

### Phase 1: Text Analysis Engine (Tier 2A — FR-1, FR-2, FR-3)
Build AnalyzeText + gap detection. Integrate into all patterns.
- textanalysis.go: entity extraction, relationship inference, negation/question detection, complexity estimation
- Enhance templates.go: add ExpectedEntities per domain template for gap detection
- Integrate AnalyzeText into Handle() for all 23 patterns

### Phase 2: Forced Reflection Protocol (Tier 2B — FR-4)
Add multi-step reasoning scaffolding to stateful patterns.
- reflection.go: ReflectionDirective, step validation logic
- Modify debugging_approach: evidence gate (≥3 findings before hypothesis)
- Modify scientific_method: lifecycle enforcement (hypothesis before experiment)
- Modify sequential_thinking: confidence tracking per step
- Enhance MCP schema descriptions to be instructional (schema-as-scaffold)

### Phase 3: Sampling Production Verification (Tier 3 — FR-5)
Verify EnableSampling works with real Claude Code client.
- Build binary with sampling, connect via Claude Code
- Test problem_decomposition sampling path
- Document results and limitations

### Phase 4: Sampling-Powered Patterns (Tier 3 — FR-6)
Enhance patterns that benefit most from LLM reasoning.
- problem_decomposition: real decomposition for non-template domains
- peer_review: LLM-generated objections
- decision_framework: LLM-suggested criteria for unknown domains
- critical_thinking: LLM bias detection beyond trigger phrases

### Phase 5: Pattern Composition + Tier Selection (FR-7, FR-8)
- Formalize output→input compatibility between patterns
- Auto-select tier based on input complexity
- Smoke test entire chain

## Constitution Compliance

| Principle | Compliance |
|-----------|-----------|
| P3: Correct Over Simple | ✅ Multi-tier analysis, not keyword-only |
| P17: No Stubs | ✅ Each tier produces computed output |
| P18: Skills = Deep Workflows | ✅ Forced reflection = phased workflow in pattern |
