# Spec: aimux v3 Audit Fixes — Bugs, Security, Dead Code

**Date:** 2026-04-06
**Status:** Active
**Source:** bug-hunting-report.md (14), security-scan-report.md (13), dead-code-report.md (42)
**Repo:** D:\Dev\aimux (github.com/thebtf/aimux)

## Problem Statement

Three independent audits (bug-hunter, security-scanner, dead-code-hunter) found 69 raw findings.
After deduplication: ~35 unique issues. 3 P1 critical, 8 P2 high, and ~10 files of dead code.
The project achieved READY PRC status but these findings must be addressed for production hardening.

## Functional Requirements

### FR-1: Fix Data Race in Pipe Executor (BUG-001, P1 CRITICAL)
CompletionPattern polling goroutine reads `stdout.String()` concurrently with OS pipe writes.
`bytes.Buffer` is not goroutine-safe. Must synchronize access with mutex.

### FR-2: Enforce MaxConcurrentJobs (FIND-001, P1 CRITICAL)
`cfg.Server.MaxConcurrentJobs` configured but never checked before spawning async jobs.
Unbounded goroutine+process creation = DoS vector. Must check before `go executeJob()`.

### FR-3: Fix mergeEnv to Inherit Parent Environment (BUG-004, FIND-006, P2)
`mergeEnv()` creates env from only override map, discarding parent process PATH/HOME/API keys.
Must start with `os.Environ()` and merge overrides on top. Affects pipe, conpty, pty executors.

### FR-4: Fix pipeSession.Send Truncation (BUG-005, P2)
Read loop breaks on partial read (`n < len(tmp)`), which is normal for pipes.
Must use timeout-based or delimiter-based reading instead of buffer-fill assumption.

### FR-5: Fix GenAI Client Leak (BUG-006, P2)
`genai.NewClient()` created per request with no `Close()`. Must either add `defer client.Close()`
or hoist client to struct field created once during construction.

### FR-6: Validate CWD Parameter (FIND-002, P2)
`cwd` from MCP request flows to `cmd.Dir` without validation. Must `filepath.Clean()` and
`os.Stat()` to prevent path traversal. Reject non-existent or non-directory paths.

### FR-7: Sanitize Model/Effort Arguments (FIND-003, P2)
`buildArgs` uses `fmt.Sprintf(template, effort)` — `%` in effort string causes format errors.
Must use `strings.ReplaceAll` consistently (like driver/template.go does).

### FR-8: Implement sessions kill/gc Actions (BUG-003, C-1, P2)
`kill` and `gc` advertised in enum but not handled in switch. Must implement or remove from enum.

### FR-8b: Wire GCReaper into Server (BUG-009, prerequisite for FR-8)
GCReaper exists but is never instantiated. Must wire into Server struct and start
background goroutine. Required for `gc` action and to prevent unbounded memory growth.

### FR-9: Add Prompt Size Limit (FIND-007, P3)
No limit on prompt size — arbitrarily large prompts can be sent. Must enforce max (e.g., 1MB).

### FR-10: Fix WAL Recovery Mutex (BUG-002, P2)
`RecoverFromWAL` writes directly to map fields bypassing mutex. Must use exported methods
that lock before inserting. Safe today (called before goroutines) but structurally broken.

### FR-11: Fix SnapshotAll Transaction No-Op (BUG-007, P3)
Transaction opened but SQL executes outside it via `s.db.Exec`. Must pass `tx` to snapshot methods.

### FR-12: Fix cached Field Misleading (BUG-008, P3)
Always reports `cached: !force` even on cache miss. Must track actual cache hit status.

### FR-13: Implement Async for Consensus/Debate (BUG-014, P4)
`async` parameter accepted but ignored. Must implement or remove from schema.

### FR-14: Store CancelFunc for Async Jobs (BUG-013, P4)
Cancel action updates DB state but can't stop subprocess (uses context.Background).
Must store CancelFunc per async job and call it on cancel.

## Dead Code Removal (from dead-code-report)

### DC-1: Delete Entire Dead Files
Files confirmed 100% dead by `deadcode` tool:
- `pkg/driver/spark.go` — SparkDetector never referenced
- `pkg/executor/buffer.go` — OutputBuffer never used
- `pkg/executor/backoff.go` — Backoff never used
- `pkg/executor/kill.go` — KillProcessTree never called (stub inside too)
- `pkg/orchestrator/assignments.go` — FileAssignment never used
- `pkg/orchestrator/handoff.go` — SessionHandoff never used
- `pkg/orchestrator/trust.go` — TrustLevel never used
- `pkg/orchestrator/chains.go` — ChainStep never used
- `pkg/orchestrator/progress.go` — ProgressTracker never used

### DC-2: Delete Unused Exported Functions
- `driver.Registry.IsAvailable`, `driver.Registry.All`
- `driver.BuildStdinArgs`
- `executor.Selector.SelectByName`, `executor.IsWindows`
- Duplicate `orchestrator.parseAuditFindings` (public `parser.ParseTextFindings` exists)

### DC-3: Delete Unused Config Fields
Fields defined in structs but never read at runtime (per deadcode analysis).

## Non-Functional Requirements

### NFR-1: Zero Data Races
After fixes, `go test -race ./...` must pass (on Linux CI where CGO available).

### NFR-2: Build + Tests Green
`go build ./...` and `go test ./...` must pass after every task.

### NFR-3: Reduced LOC
Dead code removal should reduce total LOC by 500+.

## Success Criteria

1. All P1/P2 findings resolved
2. Dead code files deleted, build still passes
3. stub-grep.sh still reports 0
4. Test count maintained or increased (no regression)
5. Re-run security-scanner: 0 P1/P2 findings remaining
