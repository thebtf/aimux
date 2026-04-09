# Implementation Plan: Think Tool — Full Pattern System

**Spec:** .agent/specs/think-tool/spec.md
**Created:** 2026-04-07
**Status:** Draft

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| State management | `pkg/think/` | Port from v2; same pattern as pkg/investigate/ |
| Session persistence | In-memory map + sync.RWMutex | Matches v2 behavior; no SQLite needed |
| Pattern handlers | Interface-based registry | Port v2 Map<string, PatternHandler> pattern |
| Complexity scoring | Pure computation | Port v2 formula exactly (4 components, weighted) |
| Dialog config | Go structs (frozen data) | Port v2 dialog-config.ts as Go maps |
| All | stdlib | Zero new dependencies |

## Architecture

```
MCP Client → think tool → server.handleThink
                                  ↓
                         pkg/think/registry.go (get pattern handler)
                         pkg/think/complexity.go (compute mode: solo/consensus)
                                  ↓
                         handler.Validate(input) → handler.Handle(input, sessionID)
                                  ↓ (if stateful)
                         pkg/think/session.go (get/create/update session)
                                  ↓
                         ThinkResult → JSON → MCP response
```

**Data flow:**
1. handleThink extracts params from MCP request → builds input map
2. Registry lookup by pattern name → get PatternHandler
3. Validate(input) → cleaned input or error
4. Handle(validInput, sessionID) → ThinkResult
5. Complexity scoring (optional, for mode recommendation)
6. Return ThinkResult as JSON

## Data Model

### ThinkResult
| Field | Type | Notes |
|-------|------|-------|
| Pattern | string | Which pattern produced it |
| Status | string | "success" or "failed" |
| Timestamp | string | ISO 8601 |
| Data | map[string]any | Pattern-specific payload (immutable) |
| SessionID | string | Optional, for stateful patterns |
| Metadata | map[string]any | Optional extras |
| SuggestedNextPattern | string | Optional chaining hint |
| ComputedFields | []string | Optional, lists auto-computed field names |

### ThinkSession
| Field | Type | Notes |
|-------|------|-------|
| ID | string | Session identifier |
| Pattern | string | Which pattern owns it |
| CreatedAt | string | ISO timestamp |
| LastAccessedAt | string | ISO timestamp, updated on each access |
| State | map[string]any | Pattern-specific state blob |

### ComplexityScore
| Field | Type | Notes |
|-------|------|-------|
| Total | int | 0-100 |
| TextLength | int | 0-100 component |
| SubItemCount | int | 0-100 component |
| StructuralDepth | int | 0-100 component |
| PatternBias | int | -50 to +50 |
| Recommendation | string | "solo" or "consensus" |
| Threshold | int | Default 60 |

### DialogConfig
| Field | Type | Notes |
|-------|------|-------|
| Participants | []DialogParticipant | CLI + Role pairs |
| TopicTemplate | string | {key} interpolation |
| PromptTemplate | string | Full prompt template |
| MaxTurns | int | Default 4 |
| Mode | string | sequential/parallel_compare/parallel/consensus |
| Synthesize | bool | |
| ComplexityBias | int | -50 to +50 |

## File Structure

```
pkg/
  think/
    types.go           ← ThinkResult, PatternHandler interface, ThinkSession, ComplexityScore, DialogConfig
    registry.go        ← Pattern registry: register, get, list, clear
    session.go         ← Session store: getOrCreate, get, update, delete, clear, count
    complexity.go      ← Complexity scoring: calculateComplexity (4-component formula)
    dialog_config.go   ← Per-pattern dialog configs (12 entries) + template interpolation
    types_test.go      ← ThinkResult construction tests
    registry_test.go   ← Registry tests
    session_test.go    ← Session management tests
    complexity_test.go ← Complexity scoring tests
    patterns/
      think.go                   ← Base "think" pattern (scratchpad)
      critical_thinking.go       ← Bias detection with keyword catalogs
      sequential_thinking.go     ← Multi-step reasoning with branches (stateful)
      scientific_method.go       ← Hypothesis lifecycle (stateful)
      decision_framework.go      ← Weighted scoring + ranking
      problem_decomposition.go   ← Problem breakdown
      debugging_approach.go      ← 18 methods + hypothesis tracking (stateful)
      mental_model.go            ← 15 model catalog
      metacognitive_monitoring.go ← Overconfidence detection
      structured_argumentation.go ← Argument graph (stateful)
      collaborative_reasoning.go  ← Multi-stage reasoning (stateful)
      recursive_thinking.go      ← Recursion analysis
      domain_modeling.go         ← Entity/relationship modeling
      architecture_analysis.go   ← ATAM-lite analysis
      stochastic_algorithm.go    ← Algorithm analysis (MDP, MCTS, etc.)
      temporal_thinking.go       ← Temporal analysis
      visual_reasoning.go        ← Spatial/visual analysis
      patterns_test.go           ← Tests for all 17 patterns
  server/
    server.go          ← handleThink rewrite (~60 lines → ~80 lines)
```

## Phases

### Phase 1: Foundation (types + registry + session + complexity)
1. Create `pkg/think/types.go` — ThinkResult, PatternHandler interface, ThinkSession, ComplexityScore, DialogConfig, DialogParticipant, MakeThinkResult helper
2. Create `pkg/think/registry.go` — Map-based pattern registry
3. Create `pkg/think/session.go` — In-memory session store with sync.RWMutex
4. Create `pkg/think/complexity.go` — 4-component complexity scoring
5. Create `pkg/think/dialog_config.go` — 12 dialog configs + template interpolation
6. Unit tests for all foundation files

### Phase 2: Stateless Patterns (11 handlers)
1. think (base), critical_thinking, decision_framework, problem_decomposition
2. mental_model, metacognitive_monitoring, recursive_thinking
3. domain_modeling, architecture_analysis, stochastic_algorithm
4. temporal_thinking, visual_reasoning
5. Unit tests for all 11 patterns

### Phase 3: Stateful Patterns (6 handlers)
1. sequential_thinking (thought history, branches, similarity)
2. scientific_method (stage lifecycle, hypothesis evolution, entry linking)
3. debugging_approach (18 methods, hypothesis tracking)
4. structured_argumentation (argument graph, claim/evidence/rebuttal linking)
5. collaborative_reasoning (6 stages, contribution types)
6. Unit tests for all 6 stateful patterns

### Phase 4: Server Wiring + Integration
1. Rewrite handleThink — dispatch to registry, pass all params, compute complexity
2. Update MCP tool schema — add pattern-specific params
3. Update existing handler tests
4. Full regression test

## Library Decisions

| Component | Library | Version | Rationale |
|-----------|---------|---------|-----------|
| All | stdlib | — | Pure Go; matches project patterns. No new deps. |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|-------------------|
| Jaccard word similarity needed for sequential_thinking | LOW | Simple implementation: word sets + intersection/union |
| Some patterns have complex validation logic in v2 | MED | Port validation exactly from v2 source |
| Dialog config template interpolation | LOW | Simple string replacement: strings.ReplaceAll for {key} |
| handleThink needs to pass many optional params | MED | Build input map from all optional MCP params |

## Constitution Compliance

- **P3 (Correct Over Simple):** Full port of all 17 patterns, not simplified versions. All data catalogs ported verbatim.
- **P8 (Single Source of Config):** Dialog configs defined as data, not hardcoded in handler.
- **P11 (Immutable By Default):** ThinkResult and session state immutable — new copies on every update.
- **P17 (No Stubs):** Every pattern returns computed data, not echo strings.
