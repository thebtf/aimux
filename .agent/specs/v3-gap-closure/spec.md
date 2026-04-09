# Spec: aimux v3 Gap Closure — Production Ready

**Date:** 2026-04-05
**Status:** Active
**Source:** gap-analysis.md (13 gaps from PRC + manual review)
**Repo:** D:\Dev\aimux (github.com/thebtf/aimux)

## Problem Statement

aimux v3 passed all 120+ tests but PRC verdict was CONDITIONALLY READY.
Code review found 7 true stubs (STUB-DISCARD, STUB-HARDCODED), 3 missing integrations,
and 3 coverage gaps. The anti-stub verification system (Constitution P17, 8-rule taxonomy)
was implemented but the v3 codebase itself still contains the stubs it's designed to catch.

## Functional Requirements

### FR-1: Pair Coding via Orchestrator (P0 — CRITICAL)
exec(role="coding") MUST route through PairCoding strategy via orchestrator.Execute().
Currently: `_ = pairParams` at server.go:459 — params prepared then discarded.
After: orchestrator returns ReviewReport with driver output + reviewer verdict.

### FR-2: Agents Run Action (P0 — HIGH)
agents(action="run") MUST actually execute the agent via exec handler.
Currently: returns `"status": "delegating to exec"` without calling anything.
After: creates session+job, spawns CLI with agent prompt, returns real output.

### FR-3: Session Resume (P1)
exec with session_id MUST resume existing session.
Currently: `_ = sessionID` at server.go:395 — param discarded.
After: lookup session from DB, validate CLI match, reuse session context.

### FR-4: Bootstrap Prompt Injection (P1)
All exec calls MUST prepend role-specific bootstrap prompt from prompts.d/ templates (.md/.txt).
Currently: prompt engine exists but never called from exec handler.
After: `injectBootstrap(role, prompt)` called before spawning CLI.

### FR-5: Stdin Piping for Long Prompts (P1)
Prompts exceeding `profile.StdinThreshold` MUST use stdin piping (Windows 8191 char limit).
Currently: StdinThreshold configured per CLI but never checked in exec handler.
After: exec handler checks `len(prompt) > profile.StdinThreshold`, sets `args.Stdin` and clears prompt arg.

### FR-6: Audit Validation Response Parsing (P1)
Audit validate phase MUST parse validator CLI output, not discard it.
Currently: `_ = result.Content` at audit.go:195 — all findings auto-confirmed.
After: parse validator response to set confidence per finding.

### FR-7: Spark Detector Version Check (P2)
SparkDetector.probe() MUST use codex --version output.
Currently: `_ = output` at spark.go:56 — version captured then discarded.
After: parse version string to detect Spark capability.

### FR-8: Dead Code Removal (P2)
Old deepresearch.go placeholder MUST be removed.
deepresearch.go has `Execute()` returning "API integration pending" — superseded by client.go.

### FR-9: Completion Pattern in Pipe Executor (P2)
Pipe executor MUST detect completion patterns and stop reading.
Currently: CompletionPattern field exists in SpawnArgs but pipe executor ignores it.
After: read loop checks output against pattern, stops gracefully on match.

### FR-10: PTY/ConPTY Start() for Persistent Sessions (P3)
Document PTY/ConPTY Start() as intentional limitation (Pipe handles persistent sessions).
Currently: returns error "not yet implemented".
After: documented limitation with proper error message referencing Pipe executor.

## Non-Functional Requirements

### NFR-1: Zero Stubs
After completion, `scripts/stub-grep.sh` MUST report 0 findings in pkg/ (excluding test files and prompt strings).

### NFR-2: Build + Tests Green
`go build ./...` and `go test ./...` MUST pass with 0 errors after every task.

### NFR-3: No Regressions
All 120+ existing tests MUST continue passing.

## Success Criteria

1. PRC verdict changes from CONDITIONALLY READY to READY
2. `stub-grep.sh` reports 0 true-positive stubs
3. All 11 MCP tools return real results (no fake data)
4. `go test -race ./...` passes on all platforms

## Clarifications

### Session 2026-04-05

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Functional Scope | PRC re-run as acceptance test? | Out of scope — stub-grep + tests are verification | 2026-04-05 |
| C2 | Integration | FR-4 references TOML but v3 uses .md | Updated spec to match reality (prompts.d/ .md/.txt) | 2026-04-05 |
| C3 | Edge Cases | Session resume of failed/completed sessions? | Reject with error, suggest new session | 2026-04-05 |
| C4 | Reliability | Invalid CompletionPattern regex? | Graceful fallback: log skip, process runs to natural exit | 2026-04-05 |
| C5 | Terminology | ShouldUseStdin/BuildStdinArgs vs StdinThreshold | Updated spec to match v3 implementation | 2026-04-05 |
