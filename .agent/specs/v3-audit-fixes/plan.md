# Plan: aimux v3 Audit Fixes

**Spec:** spec.md
**Date:** 2026-04-06

## Approach

Fix by priority (P1 → P2 → P3 → P4), dead code in parallel batch.
Each fix has paired VERIFY task. Commit after each verified fix.

## Phase 1: P1 Critical Fixes

### 1a. Data Race in Pipe Executor (FR-1, BUG-001)
- Add `sync.Mutex` to protect stdout buffer access
- Polling goroutine locks before `stdout.String()`
- OS pipe write path uses mutex-protected wrapper
- Test: `go test -race` on pipe package (requires CGO)

### 1b. MaxConcurrentJobs Enforcement (FR-2, FIND-001)
- Add `CountRunning()` method to JobManager
- Check count < MaxConcurrentJobs before `go executeJob()`
- Return error "max concurrent jobs reached" when limit hit
- Test: create MaxConcurrentJobs+1 async jobs, last should fail

## Phase 2: Dead Code Removal

### 2a. Delete 9 Dead Files (DC-1)
Parallel batch — all independent:
- spark.go, buffer.go, backoff.go, kill.go
- assignments.go, handoff.go, trust.go, chains.go, progress.go

### 2b. Delete Unused Functions (DC-2)
- Remove from loader.go, template.go, select.go, audit.go
- Verify no references remain

### 2c. Shutdown.go Assessment
- Check if shutdown.go has any live code or is entirely dead
- Delete or keep based on findings

## Phase 3: P2 High Fixes

### 3a. mergeEnv Fix (FR-3, BUG-004)
- Prepend `os.Environ()` in all 3 executors
- Test: spawn process, verify PATH inherited

### 3b. pipeSession.Send Fix (FR-4, BUG-005)
- Replace partial-read break with timeout-based reading
- Use 100ms inactivity timeout after last data received
- Test: send response > 4096 bytes, verify no truncation

### 3c. GenAI Client Leak Fix (FR-5, BUG-006)
- Hoist client to Client struct field (create once in NewClient)
- Remove per-request client creation
- Test: verify client reuse

### 3d. CWD Validation (FR-6, FIND-002)
- filepath.Clean + os.Stat in exec handler
- Reject non-directory, non-existent paths
- Test: invalid CWD returns error

### 3e. Model/Effort Sanitization (FR-7, FIND-003)
- Replace fmt.Sprintf with strings.ReplaceAll for effort formatting
- Test: effort with % character doesn't cause format error

### 3f. sessions kill/gc (FR-8, BUG-003)
- Implement kill: lookup session, fail jobs, delete session
- Implement gc: wire GCReaper, call CollectOnce
- Test: kill valid session, gc cleans expired

### 3g. WAL Recovery Mutex (FR-10, BUG-002)
- Add Import() methods to Registry and JobManager
- RecoverFromWAL calls Import() instead of direct map write
- Test: concurrent Import + List doesn't panic

## Phase 4: P3-P4 Fixes

### 4a. Prompt Size Limit (FR-9)
- Add MaxPromptBytes config (default 1MB)
- Check in exec handler, return error if exceeded

### 4b. SnapshotAll Transaction (FR-11)
- Pass tx to SnapshotSession/SnapshotJob

### 4c. Cached Field Fix (FR-12)
- Return cacheHit bool from Research(), use in response

### 4d. Async Consensus/Debate (FR-13)
- Copy async pattern from handleExec/handleAudit

### 4e. CancelFunc for Async Jobs (FR-14)
- Store context.CancelFunc per job, call on cancel action

## Phase 5: Final Verification
- stub-grep.sh → 0
- go test ./... → all pass
- go build → clean
- Compare LOC before/after

## Dependencies

Phase 1a, 1b → independent
Phase 2 → independent of Phase 1 (dead code doesn't affect functionality)
Phase 3a-3g → independent of each other, depend on Phase 2 (cleaner codebase)
Phase 4 → after Phase 3
Phase 5 → after all
