# Implementation Plan: Investigate Tool — Full Port + Enhancements

**Spec:** .agent/specs/investigate-enhancements/spec.md
**Created:** 2026-04-06
**Status:** Draft

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| State management | Custom `pkg/investigate/` | Port from v2; same pattern as existing pkg/ |
| Persistence | In-memory map + sync.RWMutex | Matches v3 session pattern; SQLite optional later |
| Report generation | String builder + markdown | No template library needed; matches v2 approach |
| Domain config | Go structs (frozen data) | Port v2 domain freeze-objects as Go structs |
| All | stdlib | Zero new dependencies |

## Architecture

```
MCP Client → investigate tool → server.handleInvestigate
                                       ↓
                              pkg/investigate/state.go (session map)
                              pkg/investigate/assess.go (convergence + coverage)
                              pkg/investigate/report.go (markdown generation)
                              pkg/investigate/domains.go (generic + debugging)
                              pkg/investigate/types.go (Finding, State, etc.)
```

**Data flow:**
1. `start` → creates InvestigationState in memory map, returns session_id + coverage areas
2. `finding` → adds Finding to state, updates coverage, detects corrections + dedup
3. `assess` → computes convergence + coverage, returns recommendation + gaps + angle
4. `report` → generates markdown, saves to .agent/reports/, returns content
5. `status/list/recall` — read-only queries on state

## Data Model

### Finding
| Field | Type | Notes |
|-------|------|-------|
| ID | string | F-{iteration}-{N} |
| Severity | string | P0-P3 |
| Confidence | string | VERIFIED/INFERRED/STALE/BLOCKED/UNKNOWN (NEW) |
| Description | string | |
| Source | string | |
| Iteration | int | |
| CoverageArea | string | nullable |
| CorrectedBy | string | nullable — ID of correcting finding |

### Correction
| Field | Type | Notes |
|-------|------|-------|
| OriginalID | string | |
| OriginalClaim | string | |
| CorrectedClaim | string | |
| Evidence | string | |
| Iteration | int | |

### InvestigationState
| Field | Type | Notes |
|-------|------|-------|
| Topic | string | |
| Domain | string | nullable |
| Iteration | int | |
| Findings | []Finding | |
| Corrections | []Correction | |
| CoverageAreas | []string | from domain |
| CoverageChecked | map[string]bool | |
| ConvergenceHistory | []float64 | |
| CreatedAt | time.Time | |
| LastActivityAt | time.Time | |

## API Contracts

### finding action (params via MCP tool args)
- Input: session_id (required), description (required), source (required), severity (P0-P3, required), confidence (optional, default VERIFIED), coverageArea (optional), corrects (optional — finding ID)
- Output: `{finding: Finding, correction: Correction|null, message: string}`
- Errors: session not found, corrects target not found

### assess action
- Input: session_id (required)
- Output: `{iteration, convergenceScore, coverageScore, findingsCount, correctionsCount, recommendation, uncheckedAreas, weakAreas, conflictingAreas, priorityNext, suggestedAngle, suggestedThinkCall, antiPatternWarnings, patternHints, message}`
- recommendation: CONTINUE | MAY_STOP | COMPLETE
- adversarial prompt included when MAY_STOP + P0 findings

### report action
- Input: session_id (required), cwd (optional — for file save)
- Output: full markdown report + file path if saved
- Sections: header (with metadata), findings table, corrections, skepticism, coverage map, convergence, key takeaways

## File Structure

```
pkg/
  investigate/
    types.go        ← Finding, Correction, InvestigationState, AssessResult, Confidence
    state.go        ← State management: create, get, addFinding, nextIteration
    assess.go       ← computeConvergence, computeCoverage, assess function
    report.go       ← generateReport (markdown with all sections)
    domains.go      ← GenericDomain, DebuggingDomain structs + registry
    types_test.go   ← Type construction tests
    state_test.go   ← State management + finding tests
    assess_test.go  ← Convergence + coverage computation tests
    report_test.go  ← Report generation tests
  server/
    server.go       ← handleInvestigate rewrite (~100 lines → ~200 lines)
```

## Phases

### Phase 1: Types + State (Foundation)
1. Create `pkg/investigate/types.go` — all Go types ported from v2 + Confidence field
2. Create `pkg/investigate/state.go` — in-memory state management (create, get, addFinding, list, nextIteration)
3. Create `pkg/investigate/domains.go` — GenericDomain + DebuggingDomain with coverage areas, methods, angles, patterns, anti-patterns
4. Unit tests for state management

### Phase 2: Assess + Report (Core logic)
1. Create `pkg/investigate/assess.go` — convergence/coverage computation, recommendation logic, angle rotation, gap tracking (weakAreas + conflictingAreas)
2. Create `pkg/investigate/report.go` — markdown generation with all sections (findings, corrections, skepticism, coverage, convergence, takeaways, metadata)
3. Unit tests for assess + report

### Phase 3: Server Wiring + Tool Schema (Integration)
1. Rewrite `handleInvestigate` in server.go — dispatch all 7 actions to investigate package
2. Update MCP tool registration — add finding-specific params (description, source, severity, confidence, corrects, coverageArea, cwd)
3. Integration test: full lifecycle start → finding → assess → report

### Phase 4: Cleanup + Polish
1. Delete legacy scripts (check-parity.sh, side-by-side.sh)
2. Full regression test
3. Update CONTINUITY.md

## Library Decisions

| Component | Library | Version | Rationale |
|-----------|---------|---------|-----------|
| All | stdlib | — | Pure Go; matches project patterns. No new deps. |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|-------------------|
| v2 uses node:sqlite for persistence; v3 has modernc.org/sqlite | LOW | Start with in-memory map (matches session pattern). Add SQLite later if needed. |
| "conflictingAreas" detection: how to determine contradiction? | MED | Same coverageArea + different severity (P0 vs P3) OR findings with opposing descriptions. Start with severity-based detection. |
| MCP tool schema may not support all new params | LOW | mcp-go supports WithString for additional params. Verified in existing tool registrations. |

## Constitution Compliance

- **P3 (Correct Over Simple):** Full port, not shortcuts. All v2 algorithms ported.
- **P8 (Single Source of Config):** Domains defined as data, not hardcoded in handler.
- **P11 (Immutable By Default):** InvestigationState immutable — state.go creates new state objects, never mutates.
- **P17 (No Stubs):** Every action fully implemented. No generic "acknowledged" responses.
