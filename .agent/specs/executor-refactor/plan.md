# Implementation Plan: Executor Refactor

**Spec:** .agent/specs/executor-refactor/spec.md
**Created:** 2026-04-09
**Status:** Draft

> **Provenance:** Planned by claude-opus-4-6 on 2026-04-09.
> Evidence from: spec.md, pkg/executor/ source (758 LOC across 4 files),
> pkg/types/interfaces.go, pipe.go completion pattern implementation.
> Confidence: VERIFIED (all source read this session).

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| ProcessManager | Custom (Go) | 50 LOC — too simple for a library |
| IOManager | Custom (Go) | Uses io.ReadCloser + bufio.Scanner + regexp — stdlib only |
| safeBuffer | Shared (extract from pipe.go) | Already battle-tested, just needs dedup |
| ANSI stripping | Existing pipeline.StripANSI | Reuse as-is |

No external dependencies needed. Pure Go stdlib refactor.

## Architecture

```
pkg/executor/
  process.go      ← NEW: ProcessManager (spawn, kill, track, shutdown)
  iomanager.go    ← NEW: IOManager (stream, pattern match, collect, drain)
  safebuf.go      ← NEW: extracted safeBuffer (shared across all executors)
  select.go       ← UNCHANGED
  validate.go     ← UNCHANGED
  breaker.go      ← UNCHANGED
  pipeline/       ← UNCHANGED (ANSI stripping, filtering)
  conpty/
    conpty.go     ← MODIFIED: Run() delegates to ProcessManager + IOManager
  pipe/
    pipe.go       ← MODIFIED: Run() delegates to ProcessManager + IOManager
  pty/
    pty.go        ← MODIFIED: Run() delegates to ProcessManager + IOManager
```

### Key Design: ProcessHandle

```go
type ProcessHandle struct {
    PID       int
    cmd       *exec.Cmd
    stdout    io.ReadCloser
    stderr    io.ReadCloser
    done      chan error      // closed when process exits
    exitCode  int
    startedAt time.Time
}
```

**REVERSIBLE** — internal type, no external API change.

### Key Design: IOManager

```go
type IOManager struct {
    handle  *ProcessHandle
    buf     *safeBuffer
    scanner *bufio.Scanner
    pattern *regexp.Regexp
    matched chan struct{}     // signaled when pattern matches
    done    chan struct{}     // signaled when EOF
}
```

**REVERSIBLE** — internal type.

### Key Design: Run() Orchestration

```go
func (e *Executor) Run(ctx context.Context, args SpawnArgs) (*Result, error) {
    handle, err := e.pm.Spawn(args)
    if err != nil { return nil, err }
    defer e.pm.Cleanup(handle)

    iom := NewIOManager(handle, args.CompletionPattern)
    go iom.StreamLines()  // reads stdout line by line, checks pattern

    select {
    case <-iom.PatternMatched():
        // CLI produced completion output — success
    case <-handle.Done():
        // Process exited naturally
    case <-time.After(timeout):
        // Timeout — drain and return partial
    case <-ctx.Done():
        // Cancelled
    }

    iom.Drain(1 * time.Second)  // collect remaining output
    handle.Kill()
    return &Result{Content: iom.Collect(), ...}, nil
}
```

**REVERSIBLE** — same external interface, different internal flow.

### Reversibility Summary

| Decision | Tag | Justification |
|----------|-----|---------------|
| Extract safeBuffer to shared file | REVERSIBLE | Pure move, no API change |
| ProcessHandle struct design | REVERSIBLE | Internal, can reshape freely |
| IOManager line-based streaming | REVERSIBLE | Replaces 100ms polling with line-based — strictly better |
| Run() as orchestration | REVERSIBLE | Same interface, different impl |
| Keep Executor interface | REVERSIBLE | No external contract change |

## File Structure

```text
pkg/executor/
  process.go         # ProcessManager: Spawn, Kill, Cleanup, Shutdown, IsAlive
  process_test.go    # Tests for process lifecycle
  iomanager.go       # IOManager: Attach, StreamLines, PatternMatched, Drain, Collect
  iomanager_test.go  # Tests for IO management
  safebuf.go         # safeBuffer (extracted from pipe.go)
  safebuf_test.go    # Tests for thread-safe buffer
  select.go          # Unchanged
  validate.go        # Unchanged
  breaker.go         # Unchanged
  pipeline/          # Unchanged
  conpty/conpty.go   # Modified: Run() uses ProcessManager + IOManager
  pipe/pipe.go       # Modified: Run() uses ProcessManager + IOManager
  pty/pty.go         # Modified: Run() uses ProcessManager + IOManager
```

## Phases

### Phase 1: Extract Shared Components
Extract safeBuffer from pipe.go to `pkg/executor/safebuf.go`.
Create ProcessManager in `pkg/executor/process.go` — spawn/kill/track.
Create IOManager in `pkg/executor/iomanager.go` — stream/pattern/collect.
Tests for each new file.

**Deliverable:** 3 new files with tests, build passes, no behavior change.

### Phase 2: Refactor Pipe Executor
Rewrite pipe.go `Run()` to use ProcessManager + IOManager.
Remove duplicated safeBuffer, completion pattern code.
Pipe is the simplest — refactor here first, validate pattern.

**Deliverable:** pipe.go uses new subsystems, all pipe tests pass.

### Phase 3: Refactor ConPTY + PTY
Apply same pattern to conpty.go and pty.go.
Platform-specific code stays (ConPTY probe, PTY allocation).
Only Run() internals change.

**Deliverable:** All 3 executors refactored, all tests pass.

### Phase 4: Persistent Session Refactor
Update Start()/Send()/Stream() to use IOManager for I/O.
ProcessManager tracks persistent sessions for shutdown cleanup.

**Deliverable:** Session interface works with new subsystems.

### Phase 5: Integration + Smoke Test
Full test suite (unit + e2e).
Smoke tests on real CLIs (codex, gemini, qwen) with role-based reasoning.
Binary rebuild + MCP smoke test.

**Deliverable:** Zero regressions, all smoke tests pass.

## Library Decisions

| Component | Library | Rationale |
|-----------|---------|-----------|
| All | Go stdlib | Process management + I/O = os/exec + io + bufio. No library needed. |

## Unknowns and Risks

| Unknown | Impact | Resolution |
|---------|--------|------------|
| Line-based streaming vs polling | MED | Pipe: bufio.Scanner on stdout pipe. ConPTY: same (stdout is a pipe on Windows). PTY: same (pty fd is readable). Test on all 3 platforms. |
| Process group kill on Windows | MED | Windows: cmd.Process.Kill() kills process tree if SysProcAttr set. Test with codex (which spawns child processes). |
| Persistent session I/O reuse | MED | Send() currently closes stdout pipe — need to keep it open. IOManager must support multiple attach/drain cycles. |
