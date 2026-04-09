# Feature: Dialog Context Management + ConflictingAreas Improvement

**Slug:** sprint3-dialog-improvements
**Created:** 2026-04-07
**Status:** Draft

## Overview

V3 orchestrator strategies (dialog, consensus, debate) work but lack context management. Long dialogs silently overflow participant context windows. Synthesis prompts can exceed limits. ConflictingAreas detection in investigate is severity-based only, missing source-level and content-level conflicts.

## Context

### Dialog handlers — WORKING (not stubs)
- `handleDialog` → `SequentialDialog.Execute()` — sequential multi-turn
- `handleConsensus` → `ParallelConsensus.Execute()` — blinded parallel + synthesis
- `handleDebate` → `StructuredDebate.Execute()` — adversarial + verdict synthesis
- All three are functional, dispatch via orchestrator

### Gaps identified (from v2 gap analysis)
1. **No context budget** — turns accumulate raw content with no limit
2. **No turn compaction** — whitespace, blank lines, verbose output waste tokens
3. **No extractive summarization** — older turns at full size
4. **No synthesis truncation** — synthesis prompt unbounded
5. **ConflictingAreas** — severity-based only (P0/P1 vs P2/P3 in same area)

## Functional Requirements

### FR-1: Context Budget Calculator
New `pkg/orchestrator/context.go` with:
- `ComputeDialogBudget(participantContextWindows []int) int` — takes minimum context window across participants, applies 80% safety factor, converts to chars (3 chars/token)
- Default context window: 128K tokens if not specified

### FR-2: Turn Compaction
- `CompactTurnContent(content string, maxChars int) string` — collapse consecutive blank lines to single, strip trailing whitespace per line, truncate at paragraph boundary if > maxChars (default 20K)
- Applied to every turn before storage

### FR-3: Extractive Summary for Older Turns
- `ExtractSummary(content string) string` — first paragraph + last paragraph, separated by "..."
- Applied to turns older than the 2 most recent when building context
- Recent turns (last 2) get full (compacted) content

### FR-4: Budget-Aware Context Building
- `BuildDialogContext(turns []TurnEntry, budget int) string` — packs turns newest-first into budget. Recent turns get full content, older turns get extractive summary. Stops when budget exceeded.
- Integrated into SequentialDialog, StructuredDebate prompt building

### FR-5: Synthesis Prompt Truncation
- `BuildSynthesisPrompt(topic string, responses []string, budget int) string` — truncates each response proportionally to fit within budget
- Integrated into ParallelConsensus and StructuredDebate synthesis steps

### FR-6: Max Response Hint
- Add response length guidance to dialog/debate prompts: "Keep your response under N characters"
- N = budget / (remaining_turns * participants_count)

### FR-7: ConflictingAreas Enhancement
Update `pkg/investigate/assess.go`:
- **Source-level conflict**: same area, different Source values, different conclusions → stronger conflict signal
- **Graduated conflict score**: P0 vs P3 = score 3, P0 vs P2 = score 2, P1 vs P2 = score 1
- Return `ConflictingArea` struct with area, score, conflicting findings instead of just area name
- Keep backward compatibility (ConflictingAreas still returns area names in assess result)

### FR-8: Partial Results on Dialog Failure
- When a turn fails in SequentialDialog, return accumulated content from successful turns instead of bare error
- Matches v2 `getAccumulatedContent()` behavior

## Non-Functional Requirements

### NFR-1: Backward Compatibility
- All MCP tool schemas unchanged
- Dialog/consensus/debate responses same shape, with optional new fields
- ConflictingAreas in assess response still string array

### NFR-2: Zero Dependencies
Pure Go stdlib.

## Edge Cases

- Zero turns → budget unused, no compaction needed
- Single participant → budget = their context window * 0.8
- All turns within budget → no summarization needed
- Content shorter than maxChars → no truncation
- Synthesis with one response → no truncation needed
- No conflicting areas at all → empty result (unchanged)

## Success Criteria

- [ ] ComputeDialogBudget returns correct budget
- [ ] CompactTurnContent strips whitespace, collapses blanks, truncates at paragraph boundary
- [ ] ExtractSummary returns first + last paragraph
- [ ] BuildDialogContext respects budget, prioritizes recent turns
- [ ] BuildSynthesisPrompt truncates proportionally
- [ ] ConflictingAreas returns source-level conflicts and graduated scores
- [ ] SequentialDialog returns partial results on failure
- [ ] All existing tests pass
