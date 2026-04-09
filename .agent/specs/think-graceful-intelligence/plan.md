# Implementation Plan: Think Patterns — Graceful Intelligence

**Spec:** .agent/specs/think-graceful-intelligence/spec.md
**Created:** 2026-04-09
**Status:** Draft

> **Provenance:** Planned by claude-opus-4-6 on 2026-04-09.
> Evidence from: spec.md (6 FR, 3 NFR), think-pattern-audit.md, smoke test results.
> Confidence: VERIFIED (existing code structure) / INFERRED (template content).

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Domain templates | Go map literals in `templates.go` | <50 templates, no external deps |
| Keyword extraction | strings.Fields + strings.ToLower | Stdlib, <1ms |
| All pattern changes | In-place Go edits | Same files, same interfaces |

## Architecture

```
pkg/think/patterns/
├── templates.go          # NEW: domain template registry (10+ templates)
├── autoanalysis.go       # NEW: keyword extraction + template matching
├── problem_decomposition.go  # MODIFY: add auto-analysis when subProblems empty
├── domain_modeling.go        # MODIFY: add auto-analysis when entities empty
├── architecture_analysis.go  # MODIFY: add auto-analysis when 0-1 components
├── decision_framework.go     # MODIFY: suggest criteria when missing
├── temporal_thinking.go      # MODIFY: keyword-based event extraction
├── visual_reasoning.go       # MODIFY: keyword-based element suggestion
├── stochastic_algorithm.go   # MODIFY: suggest outcomes for bandit/bayesian
├── think.go                  # MODIFY: add keyword extraction to base pattern
├── mental_model.go           # MODIFY: add analysis to model lookup
├── recursive_thinking.go     # MODIFY: add problem structure detection
├── metacognitive_monitoring.go # MODIFY: add self-analysis guidance
└── ... (other patterns — add guidance field)
```

## Data Model

### Guidance (new field in every pattern response)
```go
type Guidance struct {
    CurrentDepth   string   // "basic", "enriched", "full"
    NextLevel      string   // what providing more data would unlock
    Example        string   // copy-pasteable enriched call
    Enrichments    []string // available optional fields
}
```

### AutoAnalysis result
```go
type AutoAnalysisResult struct {
    Keywords      []string            // extracted from input text
    Domain        string              // matched domain or "generic"
    Suggestions   map[string][]any    // suggested sub-problems, entities, etc.
    Source        string              // "domain-template" or "keyword-analysis"
}
```

### DomainTemplate
```go
type DomainTemplate struct {
    Keywords     []string           // trigger keywords
    SubProblems  []string           // suggested decomposition
    Entities     []string           // suggested entities (for domain_modeling)
    Components   []string           // suggested components (for architecture_analysis)
    Criteria     []string           // suggested criteria (for decision_framework)
    Dependencies []map[string]string // suggested [{from, to}] deps
}
```

## Phases

### Phase 1: Foundation (templates.go + autoanalysis.go)
- Create domain template registry with 10+ templates
- Create keyword extraction + domain matching function
- Create Guidance struct and helper
- Unit tests for template matching

### Phase 2: Core Patterns (top 5 most impactful)
Modify Handle() for 5 patterns that are most visibly broken:
1. problem_decomposition — auto-suggest subProblems + run DAG
2. decision_framework — suggest criteria + template options
3. domain_modeling — suggest entities + relationships
4. architecture_analysis — suggest components from keywords
5. temporal_thinking — extract time-related concepts

### Phase 3: Remaining Patterns (8 patterns)
6. visual_reasoning — suggest elements from description
7. stochastic_algorithm — suggest outcomes
8. think — add keyword analysis to base pattern
9. mental_model — add analysis beyond lookup
10. recursive_thinking — add structure detection
11. metacognitive_monitoring — add self-assessment guidance
12. All STATEFUL patterns — add guidance field to first-call response
13. All REAL patterns that already work — add guidance field

### Phase 4: Verification
- Smoke test every pattern with minimal input
- Anti-empty verification: no pattern returns zeros
- Backward compatibility: existing tests still pass
- Coverage > 80%

## Library Decisions

| Component | Library | Rationale |
|-----------|---------|-----------|
| All | Go stdlib | String matching, maps. No external deps needed. |

## Constitution Compliance

| Principle | Compliance |
|-----------|-----------|
| P3: Correct Over Simple | ✅ Real analysis, not placeholder text |
| P17: No Stubs | ✅ Auto-generated output is computed, not hardcoded strings |
| P22: Config-as-Files | N/A (Go code for <50 templates, per C1) |
