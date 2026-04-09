# Feature: Investigate Recall + Role Prompts

**Slug:** sprint2-recall-roles
**Created:** 2026-04-07
**Status:** Draft
**Author:** AI Agent

## Overview

Two improvements to aimux v3:

1. **Investigate Recall**: The `recall` action is a stub (always returns `found: false`). Implement real report discovery: list saved reports, search by topic (substring + content), return full report content. Also add cleanup of old reports.

2. **Role Prompts**: v3 has a prompt engine (`pkg/prompt/`) that loads .md templates from `config/prompts.d/`, but only 3 generic prompts exist. v2 had 17 specialized role prompts (coding, codereview, debug, planner, etc.) with expert system instructions. Port these as .md files into `config/prompts.d/roles/` so the bootstrap injection system can use them.

## Context

### Investigate Recall — Current State
- `pkg/investigate/report.go` has `GenerateReport` and `SaveReport` — both fully implemented
- `SaveReport` writes to `.agent/reports/investigate-{slug}-{date}.md`
- Server handler `recall` action (server.go:~1310) always returns `found: false`
- v2 had `listReports`, `recallReport` (substring match on topic slug), `cleanupExpiredReports`
- v2 recall was primitive (filename-only substring match) — v3 should search content too

### Role Prompts — Current State
- `pkg/prompt/Engine` loads .md/.txt from `config/prompts.d/`
- `injectBootstrap` in server.go prepends role-specific prompt when `role` param is passed to exec
- Current prompts.d/: `coding-rules.md`, `diff-only.md`, `review-checklist.md`, `styles/`
- v2 had 17 TOML files in `config/prompts/` with `[meta]`, `[system_prompt]`, `[guidance]` sections
- v2 persona registry (`src/personas/`) existed but was unused — actual loading was via workflow prompts
- v3 prompt engine is simpler (plain .md) — no TOML parsing needed

### v2 Role Definitions (17 roles)
| Role | Purpose | Expert CLI |
|------|---------|-----------|
| analyze | Holistic technical audit | gemini |
| architect | ATAM + C4 architecture analysis | gemini |
| challenge | Skeptical plan review | gemini |
| codereview | Expert code review (13-pass protocol) | gemini |
| coding | TDD pair programming | (none) |
| debug | Hypothesis-driven debugging | gemini |
| docgen | Documentation generation | gemini |
| planner | Expert planning with branching | gemini |
| planning | Simpler implementation planning | (none) |
| precommit | Pre-commit gatekeeper | gemini |
| refactor | Adaptive refactoring | gemini |
| research | Multi-model research synthesis | gemini |
| review | Simpler code review (7-check) | (none) |
| secaudit | Full OWASP security audit | gemini |
| testgen | Test generation (5-persona workflow) | gemini |
| thinkdeep | Deep analytical thinking | gemini |
| tracer | Code tracing (execution flow) | gemini |

## Functional Requirements

### FR-1: ListReports
New function `ListReports(cwd string) ([]ReportEntry, error)` in `pkg/investigate/report.go`.
- Scans `.agent/reports/` for files matching `investigate-*.md`
- Parses filename to extract topic slug and date
- Returns: filename, topic, date, size
- Sorted by date descending (newest first)

### FR-2: RecallReport
New function `RecallReport(cwd, topicQuery string) (*RecallResult, error)` in `pkg/investigate/report.go`.
- Calls ListReports to get all reports
- Matches by: (1) topic slug substring match (case-insensitive), (2) file content search if slug match fails
- Returns first match: filename, topic, content (full file read), date
- Returns nil if no match found
- Content search: reads first 50 lines of each report, checks if topicQuery appears

### FR-3: Server Recall Action
Rewrite `recall` case in `handleInvestigate` to call `RecallReport`.
- Input: topic (required)
- Output: found (bool), filename, topic, content, date — or found=false with list of available topics

### FR-4: Server List Action Enhancement
Update `list` action to also return saved reports (not just active investigations).
- Current: lists active in-memory investigations
- New: also include saved reports from filesystem

### FR-5: CleanupExpiredReports
New function `CleanupExpiredReports(cwd string, maxAgeDays int) (int, error)`.
- Deletes investigate reports older than maxAgeDays (default 180)
- Returns count of deleted files

### FR-6: Role Prompt Files
Create .md files in `config/prompts.d/roles/` for all 17 roles.
- Each file: `{role}.md` with the system prompt content
- Format: markdown with role description, expertise areas, guidelines, tool discipline
- The prompt engine already loads from subdirectories — `roles/` will be discovered automatically
- `injectBootstrap` resolves by role name → roles/{role}.md matches

### FR-7: Prompt Engine Subdirectory Support
Verify that `pkg/prompt/Engine.Load()` recursively loads templates from subdirectories.
If not — add recursive directory walking.

## Non-Functional Requirements

### NFR-1: Backward Compatibility
- Existing report format unchanged
- Existing prompt loading unchanged
- list action returns superset of current data

### NFR-2: Performance
- ListReports scans a directory — fast for typical (<100) report counts
- Content search only triggered if slug match fails — avoids reading all files

## Edge Cases

- No .agent/reports/ directory → ListReports returns empty, no error
- Topic query matches multiple reports → return newest
- Report file corrupted/unreadable → skip, don't fail
- Role prompt file missing for a role → injectBootstrap returns empty (existing behavior)
- Empty topic query → error

## Out of Scope

- Semantic/embedding-based report search
- Report versioning or diffing
- Role prompt hot-reloading
- TOML format for prompts (v3 uses plain .md)

## Success Criteria

- [ ] ListReports returns correct entries from .agent/reports/
- [ ] RecallReport finds reports by topic (slug match + content search fallback)
- [ ] recall action returns real report content
- [ ] list action includes saved reports
- [ ] CleanupExpiredReports removes old files
- [ ] 17 role prompt .md files created
- [ ] injectBootstrap loads role prompts correctly
- [ ] All existing tests pass
