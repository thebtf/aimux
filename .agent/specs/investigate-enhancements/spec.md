# Feature: Investigate Tool — Full Port + Enhancements

**Slug:** investigate-enhancements
**Created:** 2026-04-06
**Status:** Draft
**Author:** AI Agent (user-specified requirements)

## Overview

The investigate MCP tool currently has a stub handler — `start` creates a session, all other actions (finding, assess, report, status, list, recall) return generic responses. The full implementation from mcp-aimux v2 (TypeScript) needs to be ported to Go v3, plus 6 enhancements: confidence-classified findings, enhanced gap tracking, adversarial validation, report template improvements, metadata/provenance, and completeness gates.

## Context

### V2 reference (TypeScript, D:\Dev\mcp-aimux\src\investigate\):
- `types.ts` — Finding, Correction, Assumption, InvestigationState, AssessResult interfaces
- `state.ts` (~450 lines) — In-memory + SQLite state, finding accumulation, convergence/coverage computation, domain-aware assessment with investigation angles + methods + anti-patterns
- `report.ts` (~170 lines) — Markdown report generation with findings table, corrections, coverage map, convergence history. File save/list/recall.
- `domains/` — Generic + debugging domain algorithms with coverage areas, methods, angles, anti-patterns

### V3 current state (Go):
- `pkg/server/server.go:handleInvestigate` — 30 lines stub. Only `start` works.
- No `pkg/investigate/` package exists.

### Key design decisions from v2:
- Findings have corrections (finding B corrects finding A)
- Coverage areas are domain-specific (generic: 10 areas, debugging: custom)
- Convergence = 1 - (corrections_this_iteration / findings_this_iteration)
- Assessment suggests investigation angles (rotated per iteration) + think tool calls
- Reports saved to `.agent/reports/investigate-{slug}-{date}.md`

## Functional Requirements

### FR-1: Investigation State Management (port from v2)
New `pkg/investigate/` package with: InvestigationState, Finding, Correction types. In-memory map + SQLite persistence. Coverage areas (default 10 + domain-specific). Convergence history tracking.

### FR-2: Finding Action (port + enhancement)
The `finding` action MUST accept: description (required), source (required), severity (P0-P3, required), confidence (VERIFIED/INFERRED/STALE/BLOCKED/UNKNOWN, optional, default VERIFIED — NEW), coverageArea (optional), corrects (optional — ID of finding being corrected). Dedup by description. Sequential IDs: F-{iteration}-{N}.

### FR-3: Assess Action (port + enhancement)
The `assess` action MUST compute: convergenceScore, coverageScore, uncheckedAreas, weakAreas (NEW — areas with only 1 finding), conflictingAreas (NEW — areas with contradictory findings). Recommendation: CONTINUE/MAY_STOP/COMPLETE. Suggest investigation angle + think pattern per iteration (rotation). Anti-pattern warnings from domain. When MAY_STOP with P0 findings → adversarial prompt suggestion (NEW).

### FR-4: Report Action (port + enhancement)
The `report` action MUST generate markdown with: header (topic, metadata), findings table (with confidence column — NEW), corrections section, "What to Be Skeptical Of" section (NEW — auto-generated from INFERRED findings + low-coverage areas), coverage map, convergence history, "Key Takeaways" section (NEW — top 3: root cause, recommendation, dangerous assumption). Report metadata: timestamp, model (from env CLAUDE_MODEL), session ID (from env CLAUDE_CODE_SESSION_ID), coverage%, confidence aggregate (NEW). Save to `.agent/reports/`.

### FR-5: Completeness Gate (NEW)
When `report` called with coverage < 80%, include prominent warning in output.

### FR-6: Status/List/Recall Actions (port)
- `status` — return current investigation state summary
- `list` — list all active investigations
- `recall` — load findings from past report by topic match

### FR-7: Tool Schema Update
Update MCP tool registration to add finding-specific params: description, source, severity, confidence, corrects, coverageArea.

### FR-8: Legacy Script Cleanup
Delete `scripts/check-parity.sh` and `scripts/side-by-side.sh`.

## Non-Functional Requirements

### NFR-1: Backward Compatibility
All new fields optional. `confidence` defaults to VERIFIED. Existing `start` behavior unchanged.

### NFR-2: Port Fidelity
Core algorithms (convergence computation, coverage tracking, angle rotation) MUST match v2 behavior. Domain system (generic + debugging) MUST be ported.

## User Stories

### US1: Full Investigation Lifecycle (P1)
**As a** debugging agent, **I want** to accumulate findings, track convergence, and generate reports, **so that** investigations produce actionable structured output.

**Acceptance Criteria:**
- [ ] start → finding → finding → assess → finding → assess → report lifecycle works
- [ ] findings stored with corrections chain
- [ ] convergence computed correctly (1 - corrections/findings per iteration)
- [ ] report saved to .agent/reports/

### US2: Confidence-Aware Assessment (P1)
**As a** debugging agent, **I want** confidence levels on findings and gap tracking, **so that** I know what's verified vs inferred.

**Acceptance Criteria:**
- [ ] confidence field accepted in finding action
- [ ] weakAreas and conflictingAreas in assess result
- [ ] report shows confidence per finding
- [ ] "What to Be Skeptical Of" section auto-generated

### US3: Report Quality (P1)
**As a** developer reading a report, **I want** provenance metadata and key takeaways, **so that** I know what to trust and remember.

**Acceptance Criteria:**
- [ ] report metadata includes model, session ID, coverage, confidence aggregate
- [ ] "Key Takeaways" section with 3 items
- [ ] completeness warning when coverage < 80%

## Edge Cases

- finding without session_id → error
- assess with 0 findings → coverage 0%, CONTINUE
- report at 0% coverage → warning + empty findings table
- corrects referencing nonexistent finding → error
- duplicate finding (same description) → return existing, no duplicate
- conflicting areas: detected by same coverageArea with different severity findings

## Out of Scope

- Domain algorithms beyond generic + debugging (extensible later)
- Cross-investigation correlation
- Real-time streaming of investigation progress

## Dependencies

- V2 reference: `D:\Dev\mcp-aimux\src\investigate/` (read-only)
- `pkg/session/` (existing session store for SQLite)
- `pkg/server/server.go` (handleInvestigate rewrite)

## Success Criteria

- [ ] All 7 investigate actions fully implemented
- [ ] Unit tests for state, assess, report logic
- [ ] Convergence + coverage match v2 behavior
- [ ] Report generates complete markdown with all new sections
- [ ] Legacy scripts deleted
- [ ] All existing 306 tests pass
