# Feature: Executor Refactor — Control Plane / Data Plane Separation

**Slug:** executor-refactor
**Created:** 2026-04-09
**Status:** Draft
**Author:** AI Agent (reviewed by user)

> **Provenance:** Specified by claude-opus-4-6 on 2026-04-09.
> Evidence from: pkg/executor/ source code, pkg/types/interfaces.go, user feedback on
> timeout issues, CLI profile audit (codex exec ~10s baseline, aimux adds overhead),
> v2 TypeScript architecture comparison.
> Confidence: VERIFIED (source code read this session).

## Overview

Separate process lifecycle management (control plane) from I/O handling (data plane) in
the executor subsystem. Current architecture mixes spawn/kill/wait with stdout capture
and completion pattern matching in one blocking `Run()` call, causing timeout issues,
inability to get partial output, and blocking agent conversations.

## Context

**Problem:** All three executors (ConPTY, Pipe, PTY) implement `Run()` as a monolithic
blocking call that:
1. Spawns the process (`exec.Command + cmd.Start`)
2. Captures stdout into a buffer
3. Waits for process exit OR timeout OR completion pattern
4. Returns everything at once

This creates cascading issues:
- **Timeout fragility:** codex exec takes ~10s baseline. Aimux adds process management
  overhead. With reasoning_effort=high (model thinks longer), total time approaches
  default timeouts. Users experience random "timed out" errors.
- **No partial output:** If a process is killed by timeout, buffered output is lost or
  truncated. No way to stream progress during execution.
- **Blocking conversations:** Sync exec blocks the MCP response until process completes.
  Agent's conversation stalls with "Running..." for 30+ seconds.
- **Process/IO coupling:** Killing a process (control plane) tears down stdout pipes
  (data plane). Can't read remaining output after cancel.
- **v2 lesson:** The TypeScript v2 had the same problem — timeouts ruined the project.
  v3 must solve this architecturally, not by tweaking timeout values.

**Current interface:**
```go
type Executor interface {
    Run(ctx context.Context, args SpawnArgs) (*Result, error)
    Start(ctx context.Context, args SpawnArgs) (Session, error)
    Name() string
    Available() bool
}
```

`Run()` = fire-and-forget, blocks until done.
`Start()` = persistent session, but `Send()` also blocks on I/O.

## Functional Requirements

### FR-1: ProcessManager — Process Lifecycle
A dedicated subsystem managing process lifecycle independently of I/O:
- Spawn a process from SpawnArgs, return a process handle (PID + metadata)
- Kill a process by handle (Unix: SIGTERM → SIGKILL after 5s; Windows: TerminateProcess immediately)
- Check if a process is alive (non-blocking)
- Reap completed processes (collect exit code without blocking)
- Track all spawned processes for cleanup on server shutdown

### FR-2: IOManager — Streaming I/O
A dedicated subsystem managing process I/O independently of lifecycle:
- Attach to a process handle's stdout/stderr pipes
- Stream output line-by-line via channel (`<-chan string`)
- Accumulate output into a buffer (for final collection)
- Match completion patterns against accumulated output (non-blocking)
- Write to stdin pipe (for long prompts exceeding CLI's stdin threshold)
- Strip ANSI escape sequences from output (per line, using existing pipeline.StripANSI)
- Collect all accumulated output as string (after process exits or pattern matches)

### FR-3: Executor.Run() as Orchestration
`Run()` becomes a thin orchestration layer:
1. `handle := pm.Spawn(args)` — start process
2. `io := iom.Attach(handle)` — start streaming
3. `select { case <-io.PatternMatched(): ... case <-handle.Done(): ... case <-timeout: ... }`
4. `content := io.Collect()` — get output
5. `pm.Kill(handle)` — cleanup (if still running)

No direct process or pipe management in `Run()`.

### FR-4: Non-Blocking Completion Detection
Completion pattern matching runs in IOManager, not in the executor:
- IOManager checks each line against CompletionPattern as it arrives
- When pattern matches, IOManager signals via channel
- Executor decides what to do (kill process, return output)
- No 100ms polling — line-based checking as output arrives

### FR-5: Graceful Cancel with Output Preservation
When a process is cancelled (context, timeout, or explicit kill):
- IOManager continues reading until EOF or 1s drain timeout
- Accumulated output preserved and returned as partial result
- Process killed after drain completes
- No lost output on cancel

### FR-6: Backward-Compatible Interface
The `Executor` interface signature does not change:
```go
type Executor interface {
    Run(ctx context.Context, args SpawnArgs) (*Result, error)
    Start(ctx context.Context, args SpawnArgs) (Session, error)
    Name() string
    Available() bool
}
```
Internal implementation changes, external contract unchanged.
All callers (server.go handlers, orchestrator strategies, agent runner) work without modification.

### FR-7: Unified Implementation
ProcessManager and IOManager are shared across all three executor types.
Platform-specific code is limited to process spawning (ConPTY API, PTY API, plain pipes).
Pattern matching, output accumulation, timeout handling, and ANSI stripping are identical
regardless of platform.

## Non-Functional Requirements

### NFR-1: Performance
- Process spawn to first output byte: < 500ms overhead (currently ~1-2s)
- Completion pattern detection: < 100ms after pattern appears in output
- Output collection after kill: < 1s drain window

### NFR-2: Concurrency Safety
- ProcessManager and IOManager are goroutine-safe
- Multiple concurrent Run() calls do not interfere
- Shutdown cleanly terminates all tracked processes

### NFR-3: Zero Regression
- All existing tests pass without modification
- All e2e tests (59) pass
- Smoke tests on real CLIs (codex, gemini, qwen) pass

## User Stories

### US1: Agent Gets Fast Sync Response (P0)
**As an** AI agent calling aimux exec with sync mode, **I want** the response
to arrive as soon as the CLI produces completion output, **so that** my
conversation doesn't stall for 30+ seconds on reasoning-heavy tasks.

**Acceptance Criteria:**
- [ ] Codex exec with role=codereview (reasoning=high) completes within 15s of CLI output
- [ ] No timeout errors for CLIs that respond within their profile timeout
- [ ] Output includes full CLI response, not truncated

### US2: Partial Output on Cancel (P1)
**As an** operator cancelling a long-running exec, **I want** to see what
output was produced before cancellation, **so that** I don't lose work.

**Acceptance Criteria:**
- [ ] Cancel returns Result with Partial=true and Content containing output so far
- [ ] Content is not empty when CLI produced output before cancel
- [ ] ANSI stripping applied to partial output

### US3: Clean Shutdown (P1)
**As a** server process shutting down, **I want** all spawned CLI processes
to be killed gracefully, **so that** no orphan processes consume resources.

**Acceptance Criteria:**
- [ ] ProcessManager.Shutdown() kills all tracked processes
- [ ] Graceful: SIGTERM first, SIGKILL after 5s if still alive
- [ ] Server Shutdown() calls ProcessManager.Shutdown()

## Edge Cases

- Process exits before completion pattern checked → handle.Done() fires first, return output
- Process produces no output before timeout → return empty content with Partial=true
- Completion pattern is invalid regex → skip pattern matching, wait for process exit
- Process spawns child processes → kill process group, not just parent PID
- Concurrent kill + output read → IOManager drain must be safe under race
- stdin write after process exit → no panic, return error

## Out of Scope

- HTTP/WebSocket streaming to MCP clients (MCP protocol limitation)
- Process pooling / warm process reuse (future optimization)
- Network transport improvements (user decision: local-only)
- Changing the MCP tool response format

## Dependencies

- Existing `pkg/executor/pipeline/` (ANSI stripping, output filtering) — reused as-is
- `pkg/types/` interfaces — external contract unchanged
- No new external dependencies

## Success Criteria

- [ ] All three executors (ConPTY, PTY, Pipe) use ProcessManager + IOManager internally
- [ ] `go test ./pkg/executor/... -count=1` — all pass
- [ ] `go test ./test/e2e/ -count=1 -timeout 300s` — 59/59 pass
- [ ] Smoke test: codex with role=codereview (reasoning=high) responds in <15s
- [ ] Smoke test: cancel returns partial output
- [ ] Zero regressions in `go test ./...`

## Clarifications

### Session 2026-04-09

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Data Model | ProcessHandle type definition? | struct { PID int, Cmd *exec.Cmd, Stdout io.ReadCloser, Stderr io.ReadCloser, Done <-chan error, StartedAt time.Time } | 2026-04-09 |
| C2 | Data Lifecycle | When are handles cleaned up? | Auto-removed when Done chan receives. Shutdown() kills all remaining. sync.Map tracking. | 2026-04-09 |
| C3 | Reliability | Crash during spawn? | exec.Command.Start() is atomic — succeeds+track or fails+nothing. No partial state. | 2026-04-09 |
| C4 | Constraints | Why not process pooling? | CLI processes are stateful, not reusable across prompts. Pooling deferred. | 2026-04-09 |
| C5 | Terminology | ProcessManager vs Executor? | Executor = public interface (unchanged). ProcessManager + IOManager = internal types. | 2026-04-09 |
