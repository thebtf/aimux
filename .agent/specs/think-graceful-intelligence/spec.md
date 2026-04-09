# Feature: Think Patterns — Graceful Intelligence

**Slug:** think-graceful-intelligence
**Created:** 2026-04-09
**Status:** Draft
**Author:** AI Agent (reviewed by user)

> **Provenance:** Specified by claude-opus-4-6 on 2026-04-09.
> Evidence from: smoke test results (problem_decomposition returned zeros with minimal input),
> audit matrix (.agent/research/think-pattern-audit.md), user directive ("aimux = universal
> problem solver, not calculator requiring pre-computed data").
> Confidence: VERIFIED (smoke test observed behavior).

## Overview

Every think pattern must return actionable, useful output even when called with only the
primary required field. No pattern should return empty arrays, zeros, or "please provide more
data" responses. Instead: keyword analysis, domain templates, suggested decomposition,
progressive enrichment, and concrete next-action guidance.

## Context

**Problem:** Smoke test revealed `think(pattern="problem_decomposition", problem="Design
authentication system")` returns `{subProblemCount: 0, totalComponents: 0}` — completely
useless. The pattern has DAG analysis capability but demands the agent pre-decompose the
problem before it will analyze anything. This defeats the purpose.

**Architectural principle (user directive):**
> "aimux = universal problem solver. он сам должен вести агента 'за ручку' к правильному
> решению, с каким бы вопросом агент к нему не пришел."

**Current behavior across all 23 patterns:**

| Tier | Patterns | Behavior with minimal input |
|------|----------|---------------------------|
| **Good** | critical_thinking, peer_review, literature_review, research_synthesis, source_comparison, replication_analysis | Already useful with primary field only |
| **Partial** | debugging_approach, sequential_thinking, scientific_method, collaborative_reasoning, structured_argumentation, experimental_loop | Session-stateful — first call sets up, subsequent calls add value |
| **Bad** | problem_decomposition, domain_modeling, temporal_thinking, visual_reasoning, stochastic_algorithm, architecture_analysis | Return counts/zeros without enrichment arrays |
| **Echo** | think, mental_model, recursive_thinking, metacognitive_monitoring | Return input back with minimal processing |
| **Gated** | decision_framework | Requires criteria+options arrays to function — unusable without them |

**Target:** All 23 patterns in "Good" or "Partial" tier. Zero patterns in "Bad" or "Echo".

## Functional Requirements

### FR-1: Keyword-Based Auto-Analysis
When a pattern receives only its primary text field (problem, issue, topic, etc.) and no
enrichment arrays (subProblems, components, entities, events), it MUST extract keywords
and generate a basic analysis from the text itself.

Example for problem_decomposition with `problem="Design authentication system"`:
- Extract keywords: "authentication", "system", "design"
- Apply domain template: auth → [user-registration, login, token-management, session-handling, role-based-access]
- Generate suggested dependencies: login→registration, tokens→login, roles→tokens
- Run DAG analysis on auto-generated structure
- Return: suggestedSubProblems, suggestedDependencies, dag analysis, AND a guidance message

### FR-2: Domain Templates
Maintain a registry of common problem domains with pre-built decomposition templates.
When keyword analysis detects a known domain, apply the template as a starting point.

| Domain Keywords | Template Sub-Problems |
|----------------|----------------------|
| auth, login, authentication | user-registration, login-flow, token-management, session-handling, rbac |
| api, endpoint, rest | schema-design, routing, validation, error-handling, authentication, rate-limiting |
| database, schema, migration | entity-modeling, relationships, indexes, migrations, seeding |
| deploy, ci/cd, infrastructure | build-pipeline, container, orchestration, monitoring, rollback |
| test, coverage, quality | unit-tests, integration-tests, e2e-tests, coverage-gates, fixtures |

At least 10 domain templates. Templates are suggestive, not prescriptive — output marks
them as `"source": "template"` vs `"source": "provided"`.

### FR-3: Progressive Enrichment Response
Every pattern output MUST include a `guidance` field explaining how to get richer results:
- What additional fields can be provided
- What the pattern would do with them
- Concrete example of an enriched call

Example guidance for problem_decomposition:
```
"guidance": {
  "current_depth": "basic (keyword analysis + domain template)",
  "next_level": "Provide subProblems array for custom decomposition with DAG analysis",
  "example": "think(pattern='problem_decomposition', problem='...', subProblems=['sp1','sp2'], dependencies=[{from:'sp1',to:'sp2'}])",
  "available_enrichments": ["subProblems", "dependencies", "risks", "stakeholders"]
}
```

### FR-4: Suggested Next Pattern
Every pattern result MUST include `suggestedNextPattern` with the most logical follow-up
pattern based on the analysis results. Already partially implemented but inconsistent.

### FR-5: Never Return Empty
No pattern may return zero-value arrays, empty result sets, or count-only outputs when
called with valid primary input. The minimum useful response includes:
- Text analysis of the input (keywords, entities, categories detected)
- At least one computed insight (not just echoed input)
- Guidance for enrichment
- Suggested next pattern

### FR-6: Decision Framework Fallback
When decision_framework is called with `decision` but without `criteria` and `options`,
it MUST return:
- Suggested criteria based on decision keywords (e.g., "choose database" → performance, cost, scalability, ease_of_use)
- Suggested option template with the criteria pre-filled
- A copy-pasteable enriched call example
- NOT an error message

## Non-Functional Requirements

### NFR-1: Backward Compatibility
Existing callers providing full arrays MUST get identical results. Progressive enrichment
is additive — new fields (`guidance`, `suggestedSubProblems`, `autoAnalysis`) appear
alongside existing fields, never replacing them.

### NFR-2: Performance
Keyword analysis and domain template lookup MUST complete in < 5ms (string matching only,
no external calls). No pattern execution time increases by more than 2ms.

### NFR-3: No Hallucination
Auto-generated sub-problems, components, and suggestions MUST be marked with
`"source": "auto-generated"` or `"source": "domain-template"` to distinguish from
user-provided data marked `"source": "provided"`. The agent must know which parts are
computed suggestions vs verified input.

## User Stories

### US1: Problem Decomposition with Only Problem Text (P0)
**As an** AI agent calling `think(pattern="problem_decomposition", problem="Design auth system")`,
**I want** to receive suggested sub-problems, a tentative dependency graph, and DAG analysis,
**so that** I have a starting point for decomposition without pre-computing everything myself.

**Acceptance Criteria:**
- [ ] Returns suggestedSubProblems (3-7 items) from domain template or keyword analysis
- [ ] Returns suggestedDependencies between suggested sub-problems
- [ ] Runs DAG analysis on suggested structure (topological order, no cycles)
- [ ] Returns guidance field with enrichment instructions
- [ ] All auto-generated items marked source: "auto-generated" or "domain-template"
- [ ] Returns suggestedNextPattern: "architecture_analysis"

### US2: Decision Framework without Criteria (P0)
**As an** AI agent calling `think(pattern="decision_framework", decision="Choose between PostgreSQL and MongoDB")`,
**I want** to receive suggested evaluation criteria and a template for scoring,
**so that** I can immediately start evaluating instead of getting an error.

**Acceptance Criteria:**
- [ ] Returns suggestedCriteria based on decision keywords (database→performance, scalability, cost, ease_of_use)
- [ ] Returns template options with criteria pre-filled for user to score
- [ ] Returns copy-pasteable enriched call example
- [ ] Does NOT return a validation error

### US3: Domain Modeling with Only Name (P1)
**As an** AI agent calling `think(pattern="domain_modeling", domainName="e-commerce")`,
**I want** to receive suggested entities and relationships from a domain template,
**so that** I have a starting point for data modeling.

**Acceptance Criteria:**
- [ ] Returns suggestedEntities from domain template (e-commerce→Product, Order, Customer, Cart, Payment)
- [ ] Returns suggestedRelationships between entities
- [ ] Runs consistency analysis on suggested structure
- [ ] Returns guidance for enrichment

### US4: Architecture Analysis with Minimal Components (P1)
**As an** AI agent calling `think(pattern="architecture_analysis", components='[{"name":"API"}]')`,
**I want** to receive meaningful analysis even with a single component,
**so that** I'm not required to pre-map the entire architecture.

**Acceptance Criteria:**
- [ ] Single component → returns metrics (Ca=0, Ce=0, instability=0) plus guidance
- [ ] Does NOT return useless empty arrays
- [ ] Returns suggestedComponents based on "API" keyword (→ add DB, Cache, Auth)

## Edge Cases

- Pattern called with empty string primary field → validation error (existing behavior, correct)
- Domain template has no match for keywords → fallback to generic keyword extraction
- Auto-generated sub-problems duplicate user-provided ones → dedup, mark source correctly
- Very long input text (>10000 chars) → keyword extraction truncates to first 2000 chars

## Out of Scope

- LLM-powered decomposition via sampling (separate feature, already has PoC)
- New patterns beyond existing 23
- Domain template marketplace or user-defined templates (future)
- Changing the PatternHandler interface signature

## Dependencies

- Existing `pkg/think/patterns/` implementations (restored in PR #26)
- Constitution P17 (No Stubs) — auto-generated content is real analysis, not stubs
- PR #26 merged with pattern restorations

## Success Criteria

- [ ] All 23 patterns return useful output with minimal input (primary field only)
- [ ] Zero patterns return empty arrays or count-only results when called minimally
- [ ] At least 10 domain templates registered
- [ ] All existing tests pass (backward compatibility)
- [ ] New tests verify minimal-input behavior for each pattern
- [ ] Smoke test: `think(problem_decomposition, problem="Design auth")` returns sub-problems

## Glossary

| Term | Definition |
|------|-----------|
| **Auto-analysis** | Keyword extraction + domain template matching performed in-process (<5ms) |
| **Guidance** | The enrichment instructions field in every pattern response |
| **Progressive enrichment** | The overall pattern: minimal input → basic analysis → enriched input → full analysis |
| **Domain template** | Pre-built sub-problem/entity/component structure for a known domain |

## Clarifications

### Session 2026-04-09

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Constraints | Templates in Go or config? | Go code (map literals in templates.go). <50 templates = code. >50 → migrate to YAML. | 2026-04-09 |
| C2 | Integration | Auto-analysis vs SamplingAwareHandler? | Orthogonal. Auto-analysis = in-process <5ms. Sampling = LLM 1-5s. Both coexist, output merges with source tags. | 2026-04-09 |
| C3 | Terminology | Naming conventions? | "auto-analysis" for feature, "guidance" for field, "progressive enrichment" for pattern | 2026-04-09 |

## Resolved Questions

None remaining.
