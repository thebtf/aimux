# Feature: Anti-Stub Verification System

**Slug:** anti-stub-verification
**Created:** 2026-04-05
**Status:** Draft
**Author:** AI Agent (reviewed by user)

## Overview

A multi-layer system that detects, prevents, and rejects stub/placeholder code at every stage of the aimux pipeline — both in aimux's own codebase (development) and in code that aimux generates for users (product feature). Uses a unified 8-rule taxonomy applied at prompt, hook, and CI levels.

## Context

### The Problem

AI coding agents consistently produce stubs disguised as real implementations:
- `_ = params` — computes value then discards it
- `return "delegating to exec"` — hardcoded string instead of real execution
- TODO in comments or string literals while marking task "complete"
- Constructor-only tests that pass on stubs (`assert.NotNil`)
- Correct interface implementations with zero-value returns

These bypass existing quality gates: `go build` compiles them, `go test` passes structural tests, `go vet` sees no issues. Our own v3 Go rewrite produced 118 passing tests with 4 critical stubs undetected.

### Two Levels of Application

**Level 1 — aimux as TOOL (product feature):**
aimux runs `exec(role="coding")` → codex produces code → sonnet reviews per-hunk → aimux applies. The reviewer MUST detect stubs in the driver's output. The auditor MUST flag stubs as findings.

**Level 2 — aimux as PROJECT (development process):**
When agents develop aimux itself, pre-commit hooks, CI gates, and prompt rules MUST prevent stubs from entering the codebase.

### Evidence

- Investigation report: `.agent/reports/investigate-anti-stub-verification-system-*.md` (11 findings)
- Investigation report: `.agent/reports/investigate-anti-stub-verification-as-product-featur-*.md` (9 findings)
- Gap analysis: `.agent/specs/v3-production-ready/gap-analysis.md` (13 gaps, 4 critical stubs)
- PRC report: `.agent/reports/2026-04-05-production-readiness.md` (CONDITIONALLY READY verdict)

## Functional Requirements

### FR-1: Stub Detection Taxonomy (8 Rules)
The system MUST define and enforce 8 machine-checkable stub patterns:

| Rule ID | Pattern | Default Severity |
|---------|---------|-----------------|
| STUB-DISCARD | `_ = expr` where expr is not a type assertion, error check, or range discard | HIGH |
| STUB-HARDCODED | Function returns string/value literal not computed from any parameter | HIGH |
| STUB-TODO | TODO/FIXME/SCAFFOLD/PLACEHOLDER/HACK in implementation code (not test code) | MEDIUM |
| STUB-NOOP | Function body contains only logging/printing and a return statement | HIGH |
| STUB-PASSTHROUGH | Function computes intermediate value from parameters, then discards it and returns unrelated value | CRITICAL |
| STUB-TEST-STRUCTURAL | Test function only asserts constructor result is non-nil, never tests behavioral output | MEDIUM |
| STUB-COVERAGE-ZERO | Exported function with zero test coverage | HIGH |
| STUB-INTERFACE-EMPTY | Interface implementation where all methods return zero/default values | CRITICAL |

### FR-2: PairCoding Reviewer Integration
The PairCoding reviewer prompt MUST include a 7th review criterion "Completeness" that instructs the reviewer to check each diff hunk against the 8 STUB-* rules. When a stub is detected, the reviewer MUST return verdict `changes_requested` with the specific STUB-* rule ID. The reviewer MUST NOT auto-fix stubs (verdict `modified`) — only the driver can re-implement.

### FR-3: Audit Pipeline Integration
The audit scanner's "stubs-quality" category MUST enumerate all 8 STUB-* rules with examples in its prompt. Each detected stub MUST be reported as a finding with:
- Rule ID (STUB-DISCARD, STUB-HARDCODED, etc.)
- File path and line number
- Severity (per rule default, overridable in config)
- Evidence (the specific code pattern matched)

Stub findings in audit MUST have severity HIGH minimum — stubs are functional gaps, not style issues.

### FR-4: Coding Agent Prevention Prompts
Every coding agent prompt (exec with role=coding, pair driver, agent run) MUST include anti-stub rules:
1. Every function parameter MUST influence the return value or cause a side effect
2. Return values MUST be computed from inputs, not hardcoded
3. `_ = variable` is FORBIDDEN (except type assertions, error ignoring with comment, range discards)
4. Before reporting "done", agent MUST self-audit: list each function, verify inputs→outputs causality

### FR-5: Pre-Commit Hook Detection
A pre-commit hook MUST scan staged Go files for STUB-* patterns and reject commits containing:
- `_ = ` assignments (outside test files and known exceptions)
- String literals containing "not implemented", "TODO", "scaffold", "wiring pending", "placeholder", "delegating to"
- Functions whose body is a single return of a string literal

### FR-6: CI Gate Detection
CI pipeline MUST include static analysis that catches stubs surviving pre-commit:
- Linter rules for unused assignments and discarded values
- Custom pattern matching for hardcoded returns and no-op functions
- Dead code analysis for unreachable functions

### FR-7: Mutation Testing Gate
CI pipeline MUST include mutation testing with a minimum kill rate threshold. Functions where mutants survive at >50% rate MUST be flagged as potential stubs — if changing the function body doesn't fail any test, the function is likely a stub or undertested.

### FR-8: Configurable Rule Set
The 8 STUB-* rules MUST be configurable per-project:
- Enable/disable individual rules
- Override severity per rule
- Add custom patterns (regex or keyword)
- Exclude specific files or functions (with justification comment)
- Per-project overrides via `{cwd}/.aimux/audit-rules.d/`

### FR-9: Constitution Amendment
The project constitution MUST be amended with a new principle (P17): "No Stubs — Every Code Path Must Produce Real Behavior. Functions that compile but don't perform their stated purpose are prohibited. Detected via STUB-* taxonomy."

## Non-Functional Requirements

### NFR-1: Detection Latency
Pre-commit hook MUST complete in under 5 seconds for a 100-file changeset. CI static analysis MUST complete in under 60 seconds. Mutation testing may run asynchronously (nightly or PR merge).

### NFR-2: False Positive Rate
The system MUST have less than 5% false positive rate on real Go codebases. Known legitimate patterns (error discards with `//nolint`, type assertion discards, range variable discards) MUST be excluded by default.

### NFR-3: Zero Escape Rate for Known Patterns
All 5 evasion patterns identified in the v3 investigation MUST be caught by at least one detection layer. No known pattern may pass through all layers undetected.

## User Stories

### US1: Pair-Reviewed Code Without Stubs (P1)
**As an** orchestrating agent using exec(role="coding"), **I want** the pair reviewer to catch stubs in the driver's output, **so that** no stub code reaches disk through the pair pipeline.

**Acceptance Criteria:**
- [ ] Reviewer prompt includes 7th criterion "Completeness" with 8 STUB-* rules
- [ ] Reviewer returns `changes_requested` when STUB-PASSTHROUGH detected in diff
- [ ] Reviewer returns `changes_requested` when STUB-HARDCODED detected in diff
- [ ] Driver re-prompted with specific stub feedback, produces real implementation
- [ ] After 3 rounds of stub rejection, pair escalates to caller (not infinite loop)

### US2: Audit Catches Stubs as Findings (P1)
**As an** orchestrating agent running audit, **I want** stub patterns flagged as HIGH+ findings, **so that** I can trust the audit report to surface disguised stubs.

**Acceptance Criteria:**
- [ ] audit(mode="quick") scanner prompt includes all 8 STUB-* rules with examples
- [ ] Each STUB-* match reported as individual finding with rule ID and evidence
- [ ] Stub findings have severity HIGH minimum (CRITICAL for STUB-PASSTHROUGH, STUB-INTERFACE-EMPTY)
- [ ] audit(mode="standard") validator confirms stub findings via cross-model check
- [ ] Audit report has a dedicated "Stub Detection" section

### US3: Pre-Commit Prevents Stub Commits (P1)
**As a** developer, **I want** git pre-commit to reject commits with stub patterns, **so that** stubs never enter the repository.

**Acceptance Criteria:**
- [ ] `_ = computedValue` in staged .go files blocks commit with specific error
- [ ] "not implemented" / "wiring pending" in string literals blocks commit
- [ ] Single-return-literal functions flagged with warning
- [ ] Runs in <5 seconds
- [ ] Test files excluded from STUB-TODO rule

### US4: CI Catches Surviving Stubs (P2)
**As a** CI system, **I want** static analysis and mutation testing to catch stubs that survived pre-commit, **so that** merged code is verified stub-free.

**Acceptance Criteria:**
- [ ] CI runs custom lint rules for STUB-* patterns
- [ ] Dead code analysis flags unreachable functions
- [ ] Mutation testing reports functions with >50% surviving mutants
- [ ] CI fails on any CRITICAL stub pattern (STUB-PASSTHROUGH, STUB-INTERFACE-EMPTY)

### US5: Configurable Rules Per Project (P2)
**As a** user with a custom project, **I want** to configure which STUB-* rules are active and their severity, **so that** I can tune the system for my codebase's conventions.

**Acceptance Criteria:**
- [ ] YAML config at `{cwd}/.aimux/audit-rules.d/stub-detection.yaml`
- [ ] Each rule has `enabled`, `severity`, `exclude_files`, `exclude_functions` fields
- [ ] Custom patterns addable via `additional_patterns` list
- [ ] Built-in defaults used when no project config exists

## Edge Cases

- Legitimate `_ = err` for intentional error ignoring → excluded with `//nolint:stub-discard` comment
- `_ = range` variable → excluded from STUB-DISCARD (it's idiomatic Go)
- Type assertion `_ = x.(SomeType)` for compile-time interface check → excluded
- Function intentionally returns constant (config getter) → excluded via `exclude_functions`
- Test helper returns mock data → test files excluded from STUB-HARDCODED
- Generated code (protobuf, etc.) → `exclude_files` pattern for `*.pb.go`

## Out of Scope

- Language support beyond Go (future: TypeScript, Python, Rust)
- AST-level analysis within aimux itself (relies on external tools: golangci-lint, semgrep)
- Real-time IDE integration (aimux is MCP server, not IDE plugin)
- Automated stub fixing (detection only — fixing requires human/agent judgment)

## Dependencies

- PairCoding pipeline functional (FR-2 depends on pair coding working end-to-end)
- Audit pipeline functional (FR-3 depends on audit scanner)
- Prompt template engine (FR-4 depends on prompts.d/ loading)
- Pre-existing constitution.md (FR-9 amends it)
- External: golangci-lint (FR-6), mutation testing tool (FR-7)

## Success Criteria

- [ ] Zero STUB-PASSTHROUGH or STUB-INTERFACE-EMPTY patterns in aimux v3 codebase
- [ ] PairCoding reviewer rejects at least 1 synthetic stub in test scenario
- [ ] Audit scanner produces STUB-* findings when run against known-stubby code
- [ ] Pre-commit hook blocks commit containing `_ = computedValue`
- [ ] v3 PRC re-run shows 0 critical stubs (was 4)
- [ ] Constitution amended with P17

## Open Questions

None — all decisions locked in investigation reports (20 findings across 2 investigations).
