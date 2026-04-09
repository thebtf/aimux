# Feature: Investigate Enhancements v2 — Persona Auto-Selection + Cross-Tool Dispatch

**Slug:** sprint7-investigate-enhance
**Created:** 2026-04-07
**Status:** Draft

## Overview

Three enhancements to the investigate tool that make it smarter and more autonomous:

1. **Persona auto-selection** — when starting an investigation, auto-detect the best domain from topic keywords instead of requiring explicit `domain` parameter
2. **More investigation domains** — add security, performance, architecture, research domains beyond generic and debugging  
3. **Cross-tool dispatch** — assess can auto-dispatch suggested_think_call and feed results back into the investigation

## Context

### Current State
- `start(topic, domain?)` — domain is optional, defaults to "generic". Only 2 domains: generic (10 areas), debugging (8 areas, 7 patterns)
- `assess` returns `suggested_think_call` as a string — the calling agent must parse and execute it manually
- No way to run think patterns as part of an investigation automatically
- Server handler `handleInvestigate` is self-contained — no cross-tool calls

### User Feedback (from investigate regression test)
- F-0-4: "Planned persona auto-selection NOT implemented — only generic/debugging"
- F-0-5: "Planned cross-tool invocation NOT implemented — investigate is self-contained"
- Both confirmed as planned features that were deprioritized in Sprint 1-6 roadmap

## Functional Requirements

### FR-1: Topic-Based Domain Auto-Selection
Add `AutoDetectDomain(topic string) string` to `pkg/investigate/domains.go`:
- Scan topic for keyword patterns → map to domain
- Keywords: "bug", "crash", "error", "fail" → debugging
- Keywords: "security", "auth", "injection", "XSS", "OWASP", "CVE" → security (NEW)
- Keywords: "slow", "latency", "memory", "CPU", "bottleneck" → performance (NEW)
- Keywords: "architecture", "coupling", "module", "dependency", "design" → architecture (NEW)
- Keywords: "research", "paper", "literature", "survey", "compare" → research (NEW)
- Default: generic
- Applied in `start` action when `domain` param is empty

### FR-2: Security Investigation Domain
New domain in `pkg/investigate/domains.go`:
- Coverage areas: authentication, authorization, input_validation, output_encoding, secrets_management, dependency_vulnerabilities, transport_security, error_disclosure
- Patterns: hardcoded secrets, SQL injection, XSS, path traversal, CSRF, insecure deserialization, broken auth, excessive permissions
- Angles: attacker perspective, compliance (OWASP), defense-in-depth

### FR-3: Performance Investigation Domain
- Coverage areas: CPU_hotspots, memory_allocation, IO_bottlenecks, database_queries, network_calls, concurrency, caching, algorithm_complexity
- Patterns: N+1 queries, unbounded growth, synchronous IO, missing indexes, goroutine leaks, excessive allocation

### FR-4: Architecture Investigation Domain  
- Coverage areas: module_boundaries, coupling_analysis, dependency_direction, abstraction_levels, data_flow, error_propagation, configuration, extensibility
- Patterns: circular dependencies, god objects, leaky abstractions, layer violations, hardcoded assumptions

### FR-5: Research Investigation Domain
- Coverage areas: prior_art, methodology, reproducibility, limitations, comparisons, novelty_claim, implementation_gaps, real_world_applicability
- Patterns: cherry-picked benchmarks, missing baselines, unreproducible results, overclaiming

### FR-6: Cross-Tool Dispatch in Assess
When assess returns `recommendation: "CONTINUE"` and has a `suggested_think_call`:
- Parse the think call suggestion
- Execute `think(pattern, params)` internally (in-process, not via MCP)
- Add think result as a new finding with source "auto:think:{pattern}" and confidence INFERRED
- Return assess result with `auto_dispatched: true` and the think result summary

This makes investigate semi-autonomous — it uses think tools to enrich its own analysis.

### FR-7: Assess Auto-Dispatch Toggle
Add `auto_dispatch` boolean param to assess action (default: true).
When false, behaves as before (suggestion only, no execution).
When true, executes suggested think call and adds result as finding.

## Non-Functional Requirements

### NFR-1: Backward Compatibility
- `domain` param still works explicitly (overrides auto-detection)
- assess without auto_dispatch behaves identically to current version
- All existing tests pass

### NFR-2: No New Dependencies  
- Cross-tool dispatch calls think in-process (same Go binary)
- No external MCP calls — uses `think.GetPattern()` + `handler.Handle()` directly

## Edge Cases

- Topic with no keyword matches → generic domain (as before)
- Topic with multiple domain keywords → highest-priority match (security > performance > architecture > debugging > generic)
- Auto-dispatch with unknown think pattern → skip, log warning
- Auto-dispatch think call fails → add error finding, continue

## Success Criteria

- [ ] AutoDetectDomain maps 20+ keywords to 6 domains
- [ ] 4 new domains registered with areas + patterns + angles
- [ ] start() auto-selects domain from topic when domain param empty
- [ ] assess() auto-dispatches think call when auto_dispatch=true
- [ ] Think result added as finding with INFERRED confidence
- [ ] All existing investigate tests pass
- [ ] New tests for auto-detection, new domains, cross-tool dispatch
