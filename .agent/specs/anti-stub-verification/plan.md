# Implementation Plan: Anti-Stub Verification System

**Spec:** .agent/specs/anti-stub-verification/spec.md
**Constitution:** .agent/specs/constitution.md
**Created:** 2026-04-05
**Status:** Draft

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Static analysis | golangci-lint (ineffassign, staticcheck SA4006, godox) | Already in CI, catches `_ = params` and TODO keywords |
| Pattern matching | Shell grep + Go custom linter | Semgrep not installed; grep catches 90% of patterns, custom Go analyzer for edge cases |
| Mutation testing | gremlins (go-gremlins/gremlins) | CI threshold gate, Go-native, maintained |
| Prompt templates | Existing prompts.d/ (markdown) | No new dependency — modify review-checklist.md, add coding-rules.md |
| Audit rules config | YAML in config/audit-rules.d/ | Follows constitution P8 (Single Source of Config) + P9 (Plugin Dirs) |

## Architecture

```
Anti-stub verification is NOT a new package — it's a cross-cutting concern
applied at 3 existing integration points:

                    ┌─────────────┐
                    │ Constitution │ P17: No Stubs
                    └──────┬──────┘
                           │ governs
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
    ┌──────────┐    ┌──────────┐    ┌──────────┐
    │  Prompts │    │  Hooks   │    │    CI    │
    │ (Level A)│    │(Level B) │    │(Level C) │
    └────┬─────┘    └────┬─────┘    └────┬─────┘
         │               │               │
    ┌────┴────┐    ┌─────┴─────┐   ┌─────┴──────┐
    │review-  │    │pre-commit │   │golangci-   │
    │checklist│    │hook.sh    │   │lint + grep  │
    │+7th rule│    │           │   │+ gremlins   │
    ├─────────┤    ├───────────┤   ├────────────┤
    │coding-  │    │PostToolUse│   │deadcode    │
    │rules.md │    │hook (CC)  │   │analysis    │
    ├─────────┤    └───────────┘   └────────────┘
    │audit    │
    │STUB-*   │
    │rules    │
    └─────────┘
```

## Data Model

### StubRule (config/audit-rules.d/stub-detection.yaml)
| Field | Type | Constraints | Notes |
|-------|------|-------------|-------|
| id | string | PK | STUB-DISCARD, STUB-HARDCODED, etc. |
| description | string | required | Human-readable rule description |
| severity | enum | CRITICAL/HIGH/MEDIUM/LOW | Default severity |
| pattern | string | optional | Regex pattern for grep-based detection |
| keywords | []string | optional | Keywords to search in string literals |
| exclude_files | []string | optional | Glob patterns to exclude |
| exclude_functions | []string | optional | Function names to exclude |
| enabled | bool | default true | Per-project toggle |

## File Structure

```
D:\Dev\aimux\
  config/
    prompts.d/
      review-checklist.md          # MODIFY: add 7th criterion
      coding-rules.md              # NEW: anti-stub rules for driver/agent prompts
    audit-rules.d/
      stub-detection.yaml          # NEW: 8 STUB-* rules
  scripts/
    pre-commit-stub-check.sh       # NEW: pre-commit hook
    stub-grep.sh                   # NEW: CI grep scanner
  .golangci.yml                    # NEW: linter config with stub rules
  .github/workflows/
    ci.yml                         # MODIFY: add stub detection + mutation stage

D:\Dev\mcp-aimux\
  .agent/specs/constitution.md     # MODIFY: add P17
```

## Phases

### Phase 1: Taxonomy + Prompts (config changes only, no Go code)
- Create `config/audit-rules.d/stub-detection.yaml` with 8 rules
- Update `config/prompts.d/review-checklist.md` — add 7th criterion "Completeness"
- Create `config/prompts.d/coding-rules.md` — anti-stub rules for coding agents
- Amend constitution with P17
- **Deliverable:** All prompts include anti-stub rules. Pair reviewer and auditor have explicit instructions.
- **Test:** Manual review of prompt text. No code changes to verify.

### Phase 2: Audit Scanner Enhancement (Go code change)
- Update `pkg/orchestrator/audit.go` — expand stubs-quality category prompt with 8 STUB-* rules, examples, and severity
- Load rules from `config/audit-rules.d/stub-detection.yaml` via config loader
- Add STUB-* rule ID to finding output format
- **Deliverable:** `audit(mode="quick")` on aimux v3 detects the 4 known stubs.
- **Test:** Run audit on v3 codebase, verify 4 findings with STUB-* IDs.

### Phase 3: Pre-Commit Hook (shell script)
- Create `scripts/pre-commit-stub-check.sh`
- Grep for: `_ = ` (excluding test files, type assertions), stub keywords in strings, single-return-literal functions
- Create `.golangci.yml` with ineffassign + SA4006 + godox + custom keywords
- **Deliverable:** `git commit` blocked when staged files contain stub patterns.
- **Test:** Stage a file with `_ = params`, verify commit rejected.

### Phase 4: CI Gate (GitHub Actions)
- Update `.github/workflows/ci.yml` — add stub detection job
- Create `scripts/stub-grep.sh` — CI-friendly grep scanner with exit code
- Add golangci-lint with `.golangci.yml` config (stub-specific rules)
- Add deadcode analysis step
- **Deliverable:** CI fails on any CRITICAL stub pattern.
- **Test:** Push a branch with a stub, verify CI rejects.

### Phase 5: Mutation Testing Gate (CI, async)
- Add gremlins to CI as nightly/PR-merge job
- Configure threshold: 75% mutation kill rate
- Report functions with >50% surviving mutants
- **Deliverable:** Functions that survive mutations are flagged.
- **Test:** Create a stub function with structural-only test, verify gremlins catches it.

### Phase 6: Verification
- Run audit on aimux v3 with new rules — must find 0 stubs (after v3 stubs fixed)
- Run pre-commit hook on v3 codebase — must pass clean
- Run CI pipeline — must pass clean
- Re-run PRC — verdict must be READY (not CONDITIONALLY READY)

## Library Decisions

| Component | Library | Version | Rationale |
|-----------|---------|---------|-----------|
| Linting | golangci-lint | v2.1+ | Already in CI, supports ineffassign + staticcheck + godox |
| Mutation | go-gremlins/gremlins | latest | Go-native, CI threshold gate, well maintained |
| Dead code | golang.org/x/tools/deadcode | latest | Official Go team, call graph analysis |
| Pattern matching | Shell grep | — | No new dependency, works in pre-commit and CI |
| Config loading | gopkg.in/yaml.v3 | existing | Already used for CLI profiles |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|-------------------|
| gremlins support for Go 1.25 | MED | Check during Phase 5. Fallback: mutest or skip mutation gate |
| False positive rate for `_ = ` grep | MED | Calibrate exclude patterns during Phase 3 on real codebase |
| golangci-lint Go 1.25 support | LOW | Already known issue (continue-on-error in CI). Use grep as primary, lint as secondary |

## Constitution Compliance

| Principle | How Addressed |
|-----------|---------------|
| P2 Always Pair | FR-2: reviewer gets explicit stub detection criteria |
| P3 Correct Over Simple | Anti-stub rules prevent "simple" stubs from being accepted |
| P7 Typed Errors | STUB-* rule IDs in findings are structured, not string messages |
| P8 Single Config | FR-8: rules in YAML config, not hardcoded |
| P9 Plugin Dirs | audit-rules.d/ follows cli.d/ pattern |
| P10 Verify Before Ship | FR-7: mutation testing as final verification |
| P12 Evidence | Each STUB-* finding includes code evidence (file:line:pattern) |
| NEW P17 | Constitution itself is updated as part of this feature |
