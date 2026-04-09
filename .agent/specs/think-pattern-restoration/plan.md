# Implementation Plan: Think Pattern Restoration

**Spec:** .agent/specs/think-pattern-restoration/spec.md
**Created:** 2026-04-09
**Status:** Draft

> **Provenance:** Planned by claude-opus-4-6 on 2026-04-09.
> Evidence from: spec.md (7 FR, 3 NFR), v2 source code (mcp-aimux/src/think/patterns/),
> original source code (thinking-patterns/src/servers/), v3 codebase analysis.
> Confidence: VERIFIED (line counts, code read) / INFERRED (sampling integration).

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| DAG analysis | Custom Go | Algorithm is 50 lines (DFS + Kahn's). No library needed. Port from v2. |
| Entity graph | Custom Go | Simple adjacency matrix. v2 had custom impl too. |
| Sampling | mcp-go `EnableSampling` | Already in SDK, verified via Context7. |
| Session store interface | Custom Go interface | Deferred implementation. Interface only. |
| All pattern logic | Custom Go | Port from v2 TypeScript, language-specific translation. |

## Architecture

```
pkg/think/
├── types.go              # PatternHandler interface (unchanged)
├── registry.go           # Pattern registration (unchanged)
├── session.go            # Session management (exists, may extend)
├── complexity.go         # Complexity scoring (unchanged)
├── sampling.go           # NEW: SamplingProvider interface + integration
├── patterns/
│   ├── problem_decomposition.go  # RESTORE: DAG analysis from v2
│   ├── domain_modeling.go        # RESTORE: entity graph from v2
│   ├── architecture_analysis.go  # RESTORE: layering from v2
│   ├── stochastic_algorithm.go   # RESTORE: risk assessment from v2
│   ├── temporal_thinking.go      # RESTORE: timeline from v2
│   ├── collaborative_reasoning.go # RESTORE: voting from v2
│   ├── sequential_thinking.go    # RESTORE: branches from v2
│   ├── scientific_method.go      # RESTORE: experiment depth from v2
│   └── ... (15 others unchanged)
│
pkg/server/
├── server.go             # ADD: EnableSampling() call in New()
```

## Data Model

### DAG (for problem_decomposition)
| Field | Type | Notes |
|-------|------|-------|
| Edges | []DagEdge{From, To string} | Extracted from dependencies array |
| HasCycle | bool | DFS cycle detection |
| CyclePath | []string | Path if cycle found |
| TopologicalOrder | []string | Kahn's algorithm result |
| OrphanSubProblems | []string | In subProblems but not in DAG |

### EntityGraph (for domain_modeling)
| Field | Type | Notes |
|-------|------|-------|
| Entities | []Entity{Name, Attributes} | From input |
| Relationships | []Relationship{From, To, Type} | 1:1, 1:N, N:M |
| AdjMatrix | map[string][]string | Computed |
| ConnectedComponents | int | Graph analysis |

### SamplingProvider (interface for FR-4)
```go
type SamplingProvider interface {
    RequestSampling(ctx context.Context, messages []SamplingMessage, maxTokens int) (string, error)
}
```

## API Contracts

### PatternHandler (unchanged)
```go
type PatternHandler interface {
    Name() string
    Description() string
    Validate(input map[string]any) (map[string]any, error)
    Handle(validInput map[string]any, sessionID string) (*ThinkResult, error)
}
```

### SamplingAwareHandler (new, optional)
```go
type SamplingAwareHandler interface {
    PatternHandler
    SetSampling(provider SamplingProvider)
}
```

Patterns that support sampling implement this additional interface. Server checks at
registration time and injects the provider. Non-sampling patterns unaffected.

## File Structure

No new files created beyond `pkg/think/sampling.go`. All changes are modifications
to existing pattern files in `pkg/think/patterns/`.

## Phases

### Phase 1: Audit Matrix (FR-1)
Produce the comparative audit report. No code changes.
- Read all 23 v3 patterns, all 17 v2 patterns, all 16 original patterns
- Build matrix: pattern name, v3 lines, v2 lines, orig lines, features lost, STUB classification
- Save to `.agent/research/think-pattern-audit.md`

### Phase 2: Restore Top 4 Patterns (FR-2, priority 1-4)
Port missing logic from v2 to v3 for the 4 highest-priority patterns:
1. **problem_decomposition** — DAG analysis (DFS cycle detect, Kahn's topo sort, orphan detect)
2. **domain_modeling** — entity relationship graph, adjacency matrix, connected components
3. **architecture_analysis** — layering detection, fan-in/fan-out importance ranking
4. **stochastic_algorithm** — full EV calculation, variance, risk assessment

Each pattern: read v2 source → port to Go → add tests → verify anti-STUB.

### Phase 3: Restore Bottom 4 Patterns (FR-2, priority 5-8)
5. **temporal_thinking** — timeline construction, event ordering, gap detection
6. **collaborative_reasoning** — voting aggregation, consensus detection, perspective weighting
7. **sequential_thinking** — branch tracking, revision history
8. **scientific_method** — experiment lifecycle depth, hypothesis confidence tracking

### Phase 4: EnableSampling PoC (FR-4)
- Create `pkg/think/sampling.go` with SamplingProvider interface
- Add `EnableSampling()` to server init in `pkg/server/server.go`
- Wire sampling provider into problem_decomposition
- PoC: when called without subProblems, use sampling to decompose

### Phase 5: Schema Verification + Anti-Stub Gate (FR-5, FR-6)
- End-to-end test for each pattern via MCP with JSON-string params
- Anti-STUB-PASSTHROUGH verification for all 8 restored patterns
- Coverage check > 80%

## Library Decisions

| Component | Library | Rationale |
|-----------|---------|-----------|
| All pattern logic | Custom | Port from v2 TS. Algorithms are small (50-200 lines each). |
| Sampling | mcp-go EnableSampling | SDK-native, verified in Context7 docs |
| Session interface | Custom | Deferred. Interface-only, 3 methods. |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|-------------------|
| Claude Code sampling support | HIGH | Test in Phase 4. Graceful degradation if unsupported. |
| v2 patterns use `Promise<ThinkResult>` (async) | MEDIUM | Go patterns are sync. Sampling adds async via context. |
| Some v2 patterns have 200+ lines of logic | MEDIUM | Port incrementally, test each function. |

## Constitution Compliance

| Principle | Compliance |
|-----------|-----------|
| P3: Correct Over Simple | ✅ Restoring full logic, not keeping stubs |
| P11: Immutable | ✅ Input maps not mutated, new objects returned |
| P17: No Stubs | ✅ This IS the P17 fix — removing STUB-PASSTHROUGH patterns |
| P22: Config-as-Files | N/A — patterns are Go code, not config |
