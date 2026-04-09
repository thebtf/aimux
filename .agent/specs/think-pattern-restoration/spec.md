# Feature: Think Pattern Restoration — v2→v3 Port Completion

**Slug:** think-pattern-restoration
**Created:** 2026-04-09
**Status:** Draft
**Author:** AI Agent (reviewed by user)

> **Provenance:** Specified by claude-opus-4-6 on 2026-04-09.
> Evidence from: line-by-line comparison of 3 codebases (original thinking-patterns 16 patterns/3142 lines,
> v2 mcp-aimux 17 patterns/2546 lines, v3 aimux 23 patterns/4053 lines), constitution P17 (No Stubs).
> Confidence: VERIFIED (line counts from wc -l, code read for problem_decomposition).

## Overview

Restore computational logic lost during the v2→v3 Go rewrite of think patterns. v3 patterns
compile and pass tests but many are STUB-PASSTHROUGH (P17 violation) — they accept parameters,
count them, and return counts instead of performing the analysis their Description claims.
Additionally, add MCP sampling capability (EnableSampling) to enable patterns that need LLM
reasoning to actually reason.

## Context

**Problem:** 23 think patterns in v3 total 4053 lines of Go. But many lost real logic during port:

| Pattern | Original | v2 (TS) | v3 (Go) | Lost in v3 |
|---------|----------|---------|---------|------------|
| problem_decomposition | 283 (Redis sessions, revision history, progress tracking) | 165 (DAG: cycle detect, topo sort, orphans) | 73 (counts only) | DAG analysis, sessions |
| domain_modeling | 429 (entity graph, relationship matrix, 3NF analysis) | 112 (basic entity/relationship) | 78 (counts only) | Entity graph, 3NF |
| architecture_analysis | — | 210 (coupling detect, importance, layering) | 121 (coupling count only) | Layering analysis |
| scientific_method | 433 (stateful hypothesis lifecycle, experiment tracking) | 285 (full state machine) | 156 (basic state) | Experiment tracking depth |
| sequential_thinking | 385 (branch tracking, revision history, summarization) | 209 (thought chain) | 217 (thought chain) | Branch tracking |
| collaborative_reasoning | 332 (multi-persona, voting, consensus detect) | 218 (perspective tracking) | 134 (basic perspectives) | Voting, consensus |
| temporal_thinking | 218 (timeline construction, event ordering) | 110 (basic timeline) | 72 (field echo) | Timeline construction |
| stochastic_algorithm | 119 (EV calculation, variance) | 147 (full EV+variance+risk) | 84 (basic calc) | Risk assessment |

**v3-only patterns** (6, not in original or v2): experimental_loop, literature_review,
peer_review, replication_analysis, research_synthesis, source_comparison. These were added
in v3 Sprint 8 but are basic implementations.

**Root cause:** v3 rewrite focused on interface compliance (Validate/Handle/Name/Description)
rather than behavioral fidelity. Tests verified compilation and schema, not computational output.

**MCP schema defect:** 5 patterns require structured array inputs (criteria, options, components,
sources, findings) that the MCP schema couldn't express. Fixed in PR #25 by adding JSON-string
params. But the pattern validators now need to actually USE those inputs for real computation.

**EnableSampling opportunity:** mcp-go supports `mcpServer.EnableSampling()` which lets the
server request LLM calls from the client. This enables patterns like problem_decomposition to
actually decompose problems (via LLM) rather than just counting pre-decomposed inputs.

## Functional Requirements

### FR-1: Comparative Audit Matrix
Produce a machine-readable audit comparing all 23 v3 patterns against v2 and original.
For each pattern: lines, features present, features lost, STUB-PASSTHROUGH classification.
Store at `.agent/research/think-pattern-audit.md`.

### FR-2: Restore v2 Computational Logic
For each pattern where v2 had richer logic than v3, port the missing computation:
- problem_decomposition: DAG analysis (cycle detection DFS, topological sort Kahn's, orphan detection)
- domain_modeling: entity relationship graph, normalization analysis
- architecture_analysis: layering detection, importance ranking with fan-in/fan-out
- collaborative_reasoning: voting aggregation, consensus detection
- stochastic_algorithm: full expected value + variance + risk assessment
- temporal_thinking: timeline construction, event ordering, temporal gap detection
- sequential_thinking: branch tracking, revision history
- scientific_method: experiment lifecycle, hypothesis tracking depth

### FR-3: Restore Original Features Where v2 Also Lost Them
For patterns where original had features that v2 dropped:
- problem_decomposition: session management (if Redis available), revision history, progress tracking
- domain_modeling: 3NF analysis, relationship matrix
- sequential_thinking: branch navigation, summarization
- collaborative_reasoning: multi-persona orchestration

Note: Redis dependency is optional — graceful degradation to in-memory when unavailable.
In Go, use SQLite (already in project) instead of Redis for session persistence.

### FR-4: EnableSampling Integration
Add `mcpServer.EnableSampling()` to server initialization. Create a sampling-aware
pattern handler interface that can request LLM calls. Implement one proof-of-concept:
`problem_decomposition` that, when called with only `problem` (no subProblems), uses
sampling to ask the LLM to decompose it, then runs DAG analysis on the result.

### FR-5: MCP Schema Completeness
Verify that all 23 patterns can be successfully invoked via MCP with their required
parameters. The JSON-string params added in PR #25 must be tested end-to-end.
Add missing params for patterns not yet covered.

### FR-6: Anti-Stub Verification
For each restored pattern, verify STUB-PASSTHROUGH compliance:
1. Does every parameter influence the return value?
2. Would replacing the body with `return default` still pass all tests? If yes → tests are stubs too.
3. Does the function produce a real computed output, not just echoed input?

## Non-Functional Requirements

### NFR-1: Backward Compatibility
All existing tests must continue to pass. No breaking changes to PatternHandler interface.
New features are additive — patterns that previously accepted optional arrays still work
when those arrays are omitted.

### NFR-2: Performance
Pattern execution (without sampling) must remain < 10ms per call. Sampling-enabled patterns
will have LLM latency (~1-5s) but must not block non-sampling patterns.

### NFR-3: Test Coverage
Each restored pattern must have tests that:
- Verify computed output changes when input changes (anti-STUB-PASSTHROUGH)
- Cover the specific logic restored (DAG cycle detection, topo sort, etc.)
- Coverage > 80% for pkg/think/patterns/

## User Stories

### US1: Pattern Returns Real Analysis (P0)
**As an** AI agent calling `think(pattern="problem_decomposition", problem="...", subProblems=[...], dependencies=[...])`,
**I want** to receive DAG analysis (cycles, topological order, orphans),
**so that** I can reason about task ordering and dependency conflicts.

**Acceptance Criteria:**
- [ ] Cyclic dependencies detected and reported with cycle path
- [ ] Topological order computed for acyclic graphs
- [ ] Orphan sub-problems (not in dependency graph) identified
- [ ] Different inputs produce measurably different outputs (not just counts)

### US2: Sampling-Powered Decomposition (P1)
**As an** AI agent calling `think(pattern="problem_decomposition", problem="Design auth system")`,
**I want** the pattern to use LLM sampling to decompose the problem into sub-problems,
**so that** I get real analysis without having to pre-decompose manually.

**Acceptance Criteria:**
- [ ] EnableSampling() called during server init
- [ ] When subProblems not provided, RequestSampling generates them
- [ ] DAG analysis runs on generated sub-problems
- [ ] Graceful degradation if sampling unavailable (return basic result)

### US3: Audit Report (P0)
**As a** developer, **I want** a comprehensive audit matrix of all 23 patterns,
**so that** I can prioritize which patterns to restore first.

**Acceptance Criteria:**
- [ ] Matrix covers all 23 v3 patterns
- [ ] Each entry shows: v3 lines, v2 lines, original lines, features lost, STUB classification
- [ ] Prioritized restoration order based on usage frequency and logic complexity

## Edge Cases

- Pattern called with subProblems=[] (empty array) → should still work, just no DAG analysis
- Sampling timeout or failure → graceful degradation to non-sampling behavior
- Circular dependencies in DAG → detected, reported, no panic
- Pattern called via MCP with JSON-string param that's invalid JSON → clear error message

## Out of Scope

- Redis session management (use SQLite instead, already in project)
- Original-only features beyond v2 (revision history, progress tracking, rich metrics from original thinking-patterns) — deferred per C2
- New patterns beyond the existing 23
- UI or dashboard for pattern results
- Workflow loop safety (WTF-score) — separate feature

## Dependencies

- Existing `pkg/think/` package and PatternHandler interface
- `mcpServer.EnableSampling()` from mcp-go (verified available)
- SQLite (modernc.org/sqlite, already in go.mod) for optional session persistence
- PR #25 merged (JSON-string params for structured inputs) ✅

## Success Criteria

- [ ] Audit matrix produced for all 23 patterns
- [ ] At least 8 patterns restored with real computational logic
- [ ] EnableSampling PoC working for problem_decomposition
- [ ] All restored patterns pass anti-STUB-PASSTHROUGH verification
- [ ] go test ./pkg/think/... passes with 0 regressions
- [ ] Coverage > 80% for pkg/think/patterns/

## Clarifications

### Session 2026-04-09

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Constraints | Sampling opt-in vs automatic? | Automatic: inferred from input completeness. No subProblems → sampling. subProblems provided → DAG only. | 2026-04-09 |
| C2 | Data Lifecycle | Session persistence in scope? | Deferred. Define SessionStore interface only, no implementation. Focus on computational logic. | 2026-04-09 |
| C3 | Integration | Claude Code sampling support? | Add FR-4 AC: verify sampling, graceful degradation if unsupported. | 2026-04-09 |
| C4 | Domain/Data | Session data model? | Interface-only: SessionStore with Get/Set/Delete. Patterns use optionally. Implementation deferred. | 2026-04-09 |
| C5 | Constraints | Restoration priority? | By logic loss: (1) problem_decomposition, (2) domain_modeling, (3) architecture_analysis, (4) stochastic_algorithm, (5) temporal_thinking, (6) collaborative_reasoning, (7) sequential_thinking, (8) scientific_method | 2026-04-09 |

## Resolved Questions

1. **Sampling activation** — Automatic based on input completeness. No new parameter.
2. **Session persistence** — Deferred. Interface only in this feature.
3. **Priority order** — 8 patterns in severity order (see C5).
