# Code Quality Requirements Quality Checklist

**Feature:** Anti-Stub Verification System
**Focus:** Code quality + detection completeness
**Created:** 2026-04-05
**Depth:** Standard

## Requirement Completeness

- [ ] CHK001 Are all 8 STUB-* rules defined with pattern, severity, and example for each? [Completeness, Spec §FR-1]
- [ ] CHK002 Are all 5 evasion patterns from the v3 investigation mapped to at least one STUB-* rule? [Completeness, Spec §NFR-3]
- [ ] CHK003 Are exclusion patterns documented for each STUB-* rule (what is NOT a stub)? [Completeness, Spec §Edge Cases]
- [ ] CHK004 Are all 3 integration points specified (pair reviewer, audit scanner, coding prompts)? [Completeness, Spec §FR-2, FR-3, FR-4]
- [ ] CHK005 Are requirements defined for what happens when a stub is detected at each integration point? [Completeness, Spec §FR-2 verdict, FR-3 severity]

## Requirement Clarity

- [ ] CHK006 Is "computed value" in STUB-DISCARD precisely defined — what counts as "computed" vs "received"? [Clarity, Spec §FR-1]
- [ ] CHK007 Is "not derived from inputs" in STUB-HARDCODED measurable — how many degrees of separation are acceptable? [Clarity, Spec §FR-1]
- [ ] CHK008 Is "only logging/printing and a return" in STUB-NOOP bounded — does a function with log + one assignment + return qualify? [Clarity, Spec §FR-1]
- [ ] CHK009 Is the boundary between STUB-PASSTHROUGH and legitimate "prepare then delegate" patterns defined? [Clarity, Spec §FR-1]
- [ ] CHK010 Is "constructor-only test" in STUB-TEST-STRUCTURAL precisely scoped — does a test that calls a function and asserts type qualify? [Clarity, Spec §FR-1]

## Requirement Consistency

- [ ] CHK011 Are STUB-* rule severities consistent between spec §FR-1 table and plan §Phase 2 audit prompt? [Consistency, Spec §FR-1 ↔ Plan §Phase 2]
- [ ] CHK012 Are the 8 STUB-* rule IDs consistent across spec, plan, tasks, and YAML config? [Consistency, All artifacts]
- [ ] CHK013 Is "changes_requested" verdict in FR-2 consistent with HunkReview type in types.go? [Consistency, Spec §FR-2 ↔ types.ReviewVerdict]
- [ ] CHK014 Are exclusion patterns in spec §Edge Cases reflected in YAML config's exclude_files/exclude_functions? [Consistency, Spec ↔ Config]

## Acceptance Criteria Quality

- [ ] CHK015 Can "reviewer returns changes_requested when STUB-PASSTHROUGH detected" be tested with a synthetic diff? [Measurability, Spec §US1]
- [ ] CHK016 Can "stub findings have severity HIGH minimum" be verified by inspecting finding objects? [Measurability, Spec §US2]
- [ ] CHK017 Can "pre-commit completes in <5 seconds" be benchmarked on 100-file changeset? [Measurability, Spec §NFR-1]
- [ ] CHK018 Can "<5% false positive rate" be measured on known-clean codebases? [Measurability, Spec §NFR-2]
- [ ] CHK019 Can "zero escape rate for known patterns" be verified by running all 5 patterns through all 3 layers? [Measurability, Spec §NFR-3]

## Scenario Coverage

- [ ] CHK020 Are requirements defined for when a function legitimately returns a constant (config getter, version string)? [Coverage, Edge Case]
- [ ] CHK021 Are requirements defined for generated code (protobuf, go generate) that may match STUB-* patterns? [Coverage, Edge Case]
- [ ] CHK022 Are requirements defined for test files — should STUB-TEST-STRUCTURAL check test quality of OTHER tests? [Coverage, Spec §FR-1]
- [ ] CHK023 Are requirements defined for when reviewer and auditor disagree on whether something is a stub? [Coverage, Gap]
- [ ] CHK024 Are requirements defined for stub detection in non-Go files (YAML, Markdown, config) or only .go? [Coverage, Gap]

## Edge Case Coverage

- [ ] CHK025 Are requirements defined for `_ = err` with an explicit `//nolint` comment? [Edge Case, Spec §Edge Cases]
- [ ] CHK026 Are requirements defined for a function that uses all parameters but returns a default on one code path? [Edge Case, Gap]
- [ ] CHK027 Are requirements defined for interface methods that intentionally return `nil, nil` (no-op middleware)? [Edge Case, Gap]
- [ ] CHK028 Are requirements defined for how STUB-COVERAGE-ZERO interacts with platform-specific files (build tags)? [Edge Case, Gap]

## Non-Functional Requirements

- [ ] CHK029 Is the <5s pre-commit latency target specified with measurement methodology (cold start vs warm)? [NFR, Spec §NFR-1]
- [ ] CHK030 Is the <60s CI analysis target specified with scope (per-package vs whole repo)? [NFR, Spec §NFR-1]
- [ ] CHK031 Is the 75% mutation kill rate threshold justified with evidence, or arbitrary? [NFR, Spec §FR-7]
- [ ] CHK032 Is the <5% false positive rate target specified per-rule or aggregate across all 8 rules? [NFR, Spec §NFR-2]

## Dependencies and Assumptions

- [ ] CHK033 Is the assumption "golangci-lint can detect `_ = params` via ineffassign + SA4006" validated against actual tool behavior? [Dependency, Plan §Tech Stack]
- [ ] CHK034 Is the assumption "gremlins supports Go 1.25" validated? [Dependency, Plan §Unknowns]
- [ ] CHK035 Is the assumption "pair coding pipeline is functional" documented as a blocking dependency for US1? [Dependency, Spec §Dependencies]
- [ ] CHK036 Is the assumption "prompt template engine loads prompts.d/ at runtime" validated against current code? [Dependency, Plan §Phase 1]
