# Bug Hunting Report — aimux v3

**Date:** 2026-04-05
**Scope:** Full codebase, 18 packages, focus on server, executor/pipe, orchestrator, session
**Build status:** `go build ./...` — clean. `go vet ./...` — clean.
**Race detector:** Could not run (`CGO_ENABLED=1` required, GCC not in PATH on this machine).

---

## Summary

| Priority | Count |
|----------|-------|
| P1 Critical | 2 |
| P2 High    | 4 |
| P3 Medium  | 5 |
| P4 Low     | 3 |
| **Total**  | **14** |

---

## P1 Critical

### BUG-001 — Race condition: concurrent bytes.Buffer access in pipe executor

**File:** `pkg/executor/pipe/pipe.go`, lines 47–83 and 116–154

**Description:**
`bytes.Buffer` is not goroutine-safe. In `Run()`, `cmd.Stdout = &stdout` causes the OS-level goroutine inside `cmd.Wait()` to write to `stdout` concurrently. Simultaneously, the `CompletionPattern` polling goroutine (lines 77–91) calls `stdout.String()` every 100ms. This is an unsynchronized concurrent read+write on `bytes.Buffer` — a data race.

```go
// stdout is a bytes.Buffer, written by cmd.Stdout pipeline and read here concurrently
go func() {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            if re.MatchString(stdout.String()) { // concurrent read — DATA RACE
```

**Impact:** Undefined behavior: corrupted output strings, panics from internal slice bounds violations in `bytes.Buffer`. Triggered whenever `CompletionPattern` is set.

**Fix:** Replace `bytes.Buffer` with a mutex-protected wrapper, or use `bytes.Buffer` guarded by a `sync.Mutex` for both writes and reads.

---

### BUG-002 — Race condition: WAL recovery bypasses Registry/JobManager mutexes

**File:** `pkg/session/recovery.go`, lines 30 and 58

**Description:**
`RecoverFromWAL` writes directly to unexported struct fields `sessions.sessions` and `jobs.jobs`, bypassing the `sync.RWMutex` guards in `Registry` and `JobManager`. If recovery is called while any other goroutine concurrently reads (e.g., the GC reaper, a `List()` call) there is an unsynchronized map write.

```go
// Line 30 — bypasses Registry.mu
sessions.sessions[sess.ID] = &sess

// Line 58 — bypasses JobManager.mu
jobs.jobs[job.ID] = &job
```

**Impact:** Map concurrent access panic (`fatal error: concurrent map read and map write`) or silent data corruption during startup if recovery and any concurrent access overlap. In the current codebase `RecoverFromWAL` is called before serving (no goroutines running yet), but this is fragile — a future caller invoking it after startup will trigger the race.

**Fix:** Add exported `Import` methods to `Registry` and `JobManager` that lock before inserting, and call those from `RecoverFromWAL`.

---

## P2 High

### BUG-003 — Logic bug: `sessions` tool `kill` and `gc` actions silently fail

**File:** `pkg/server/server.go`, lines 174 and 696–745

**Description:**
The `sessions` tool advertises `kill` and `gc` as valid enum values in the tool schema:

```go
mcp.Enum("list", "info", "kill", "gc", "health", "cancel"),
```

The `handleSessions` switch implements only `list`, `info`, `health`, and `cancel`. `kill` and `gc` fall through to `default`, which returns an error: `"unknown action \"kill\""`. This contradicts the advertised API contract — callers following the schema will get an error.

**Impact:** `kill` (terminate a session) and `gc` (trigger garbage collection) are completely non-functional via the MCP tool. The `GCReaper` and `CollectOnce()` implementations exist but are unreachable through the public API.

**Fix:** Add `case "kill"` and `case "gc"` to the switch. For `kill`: look up by `session_id`, fail running jobs, delete session. For `gc`: call `gcReaper.CollectOnce()` (which requires wiring `GCReaper` into `Server` — see BUG-009).

---

### BUG-004 — Environment inheritance stripped: CLIs launched with empty environment

**File:** `pkg/executor/pipe/pipe.go`, lines 43–45 and 164–166; same pattern in `pkg/executor/conpty/conpty.go` lines 65–69 and `pkg/executor/pty/pty.go` lines 53–56

**Description:**
`mergeEnv` constructs `cmd.Env` from only the `args.Env` map:

```go
func mergeEnv(extra map[string]string) []string {
    env := make([]string, 0)
    for k, v := range extra {
        env = append(env, k+"="+v)
    }
    return env
}
```

When `cmd.Env` is set to a non-nil slice, Go's `exec.Command` uses that slice as the complete environment — the parent process's `PATH`, `HOME`, `GOPATH`, `GOOGLE_API_KEY`, and all other variables are discarded. If `args.Env` is populated with only a small override map, the spawned CLI will not find its own dependencies, cannot write to the home directory, and will miss API keys.

**Impact:** CLI tools that rely on any inherited environment variable will fail with obscure errors (binary not found, API key missing). The conpty.go variant does the same in its loop.

**Fix:** Start with `os.Environ()` as the base and merge overrides on top:
```go
func mergeEnv(extra map[string]string) []string {
    env := os.Environ()
    for k, v := range extra {
        env = append(env, k+"="+v)
    }
    return env
}
```

---

### BUG-005 — Broken read loop in `pipeSession.Send` causes premature response truncation

**File:** `pkg/executor/pipe/pipe.go`, lines 213–226

**Description:**
The read loop in `pipeSession.Send` breaks when `n < len(tmp)`:

```go
for {
    n, readErr := s.stdout.Read(tmp)
    if n > 0 {
        buf.Write(tmp[:n])
    }
    if readErr != nil {
        break
    }
    if n < len(tmp) { // BUG: partial read does not mean EOF on a pipe
        break
    }
}
```

On a pipe, `Read` returns as soon as any bytes are available — it does not fill the buffer to capacity before returning. A partial read (e.g., the first 512 bytes of a 10 KB response) causes the loop to exit immediately, returning a truncated response. The function has no way to know when the CLI has finished writing its full response, so it needs a delimiter or timeout-based approach.

**Impact:** Multi-turn live sessions (`pipeSession`) return truncated responses consistently. Any CLI response smaller than 4096 bytes gets returned correctly by luck; longer responses are cut at first `Read` boundary.

**Fix:** Use a delimiter-based read (e.g., read until a known sentinel the prompt injects) or replace with a line-oriented scanner that reads until a known completion marker. The `Stream` method delegates to `Send`, so both code paths are affected.

---

### BUG-006 — GenAI client created on every request with no `Close` — connection leak

**File:** `pkg/tools/deepresearch/client.go`, lines 56–61

**Description:**
`Research()` creates a new `genai.Client` on every call:

```go
client, err := genai.NewClient(ctx, &genai.ClientConfig{...})
if err != nil {
    return "", fmt.Errorf("create GenAI client: %w", err)
}
// ... no defer client.Close()
```

The `genai.Client` holds HTTP connections, gRPC channels, or both depending on backend. There is no `defer client.Close()`. Under repeated `deepresearch` calls, HTTP transports accumulate and are never returned to the pool.

**Impact:** Connection leak proportional to deepresearch call frequency. Under sustained load, the process exhausts file descriptors or hits HTTP transport limits.

**Fix:** Add `defer client.Close()` immediately after the nil check, or hoist the client to a `Client` struct field created once during construction.

---

## P3 Medium

### BUG-007 — `SnapshotAll` transaction is a no-op: individual methods use `s.db.Exec` not `tx.Exec`

**File:** `pkg/session/sqlite.go`, lines 121–142

**Description:**
`SnapshotAll` opens a database transaction and defers `tx.Rollback()`, but `SnapshotSession` and `SnapshotJob` execute their SQL against `s.db.Exec` directly — not the transaction. The transaction commits or rolls back nothing that `SnapshotAll` controls. If `SnapshotSession` fails partway through the loop, the already-executed upserts are permanently committed.

```go
func (s *Store) SnapshotAll(sessions *Registry, jobs *JobManager) error {
    tx, err := s.db.Begin()          // transaction opened
    defer tx.Rollback()
    for _, sess := range sessions.List("") {
        s.SnapshotSession(sess)      // uses s.db.Exec — outside the transaction
    }
    return tx.Commit()               // commits nothing (tx has no statements)
}
```

**Impact:** Partial snapshots cannot be rolled back. On an `SnapshotAll` error mid-loop, SQLite contains a mix of new and old data with no atomicity guarantee.

**Fix:** Pass `tx` to `SnapshotSession` and `SnapshotJob` as a parameter, or restructure to execute all SQL directly within `SnapshotAll` using `tx.Exec`.

---

### BUG-008 — Misleading `cached` field in deepresearch response

**File:** `pkg/server/server.go`, line 1057

**Description:**
The response always sets `"cached": !force` regardless of whether the result actually came from cache:

```go
result := map[string]any{
    "topic":   topic,
    "content": content,
    "cached":  !force,  // BUG: true even when force=false but cache was cold (miss)
}
```

When `force=false` but no cache entry exists, the code makes a live API call but reports `"cached": true`.

**Impact:** Callers cannot distinguish cache hits from live API calls. Misleading for cost tracking, debugging, and decision-making about whether to `force=true`.

**Fix:** Track whether the cache was hit inside `Client.Research()` and return that boolean, or return a struct with an explicit `CacheHit bool` field.

---

### BUG-009 — GCReaper, WAL, and SQLite Store are dead infrastructure — never wired

**File:** `cmd/aimux/main.go`, `pkg/server/server.go`, `cmd/aimux/shutdown.go`

**Description:**
Three major subsystems are fully implemented with tests but never instantiated at runtime:
- `session.GCReaper` — never created or started; no GC loop runs
- `session.WAL` — never created; crash recovery is inoperative
- `session.Store` (SQLite) — never created; no persistence

`shutdown.go` explicitly acknowledges this: `"Future: WAL.Flush() + WAL.Close()"`. The `handleSessions` `gc` action (BUG-003) would also require `GCReaper` to be wired.

**Impact:** Sessions and jobs accumulate indefinitely in memory; the process memory grows unboundedly over time. On process restart all session state is lost. The GC documentation and `sessions(gc)` API advertise functionality that doesn't run.

**Fix:** Wire `NewGCReaper`, `NewWAL`, and `NewStore` in `server.New()` or `main.go`. Start the reaper goroutine with a context tied to process lifetime.

---

### BUG-010 — `audit` strategy `investigate` phase is a stub returning placeholder strings

**File:** `pkg/orchestrator/audit.go`, lines 228–237

**Description:**
The `investigate` phase — called only in `deep` mode — performs no actual investigation. It iterates over `HIGH+` findings and appends static strings:

```go
func (a *AuditPipeline) investigate(ctx context.Context, params types.StrategyParams, findings []auditFinding) []string {
    var reports []string
    for _, f := range findings {
        if f.Severity != "CRITICAL" && f.Severity != "HIGH" {
            continue
        }
        reports = append(reports, fmt.Sprintf("Investigated: [%s] %s — %s", f.Severity, f.Rule, f.Message))
    }
    return reports
}
```

No executor call is made. The function signature takes `ctx` and `params` (including CLIs and CWD) but ignores them entirely. The investigate phase is documented as "deep investigation via CLI" in strategy comments.

**Impact:** `audit(mode="deep")` silently produces the same output as `audit(mode="standard")`. The deep mode distinction is meaningless.

**Fix:** Implement actual executor calls per finding, or clearly mark deep mode as unimplemented and return a structured error instead of a no-op.

---

### BUG-011 — `handleThink` is a stub returning a static format string

**File:** `pkg/server/server.go`, lines 936–963

**Description:**
`handleThink` performs no actual structured thinking. It returns a static string:

```go
"output": fmt.Sprintf("Thinking with pattern '%s' about: %s", pattern, input),
```

No pattern logic, no CLI dispatch, no in-process reasoning — all inputs are discarded after being echoed back in the format string. The tool description promises "Structured thinking patterns for analysis and reasoning."

**Impact:** Callers receive a meaningless echo. The `think` tool advertises 10 named patterns (`critical_thinking`, `decision_framework`, etc.) in the tool description but none are implemented.

**Fix:** Either implement real pattern-dispatch logic, or route to a CLI (via executor) with a pattern-specific prompt template loaded from `prompts.d/`. Alternatively, if the implementation is genuinely deferred, remove the tool registration or return a clear `"not_implemented"` error rather than a fake success response.

---

## P4 Low

### BUG-012 — `uuid.Must` panics on UUID generation failure

**File:** `pkg/session/registry.go` line 46, `pkg/session/jobs.go` line 50

**Description:**
Both `Create` methods use `uuid.Must(uuid.NewV7())`. If the UUID library fails to read from the OS entropy source (very rare, but possible under resource exhaustion), `uuid.Must` calls `panic()`. The panic propagates through the mutex lock, leaving it permanently held and deadlocking all subsequent session/job operations.

**Impact:** Extremely low probability but catastrophic when triggered: permanent server deadlock.

**Fix:** Handle the error explicitly:
```go
id, err := uuid.NewV7()
if err != nil {
    // fallback to UUID v4 or return error
}
```

---

### BUG-013 — `cancel` action sets job to failed but cannot stop the running subprocess

**File:** `pkg/server/server.go`, lines 730–740

**Description:**
Cancelling a job via `sessions(action="cancel", job_id=...)` calls `FailJob()` to update in-memory state, but the goroutine running the actual subprocess continues executing. The async goroutines (`executePairCoding`, `executeJob`) hold no reference to a cancellable context — they use `context.Background()`:

```go
go s.executeJob(context.Background(), job.ID, sess.ID, args, cb)
```

After a cancel, the job shows as `failed` in status queries, but the subprocess is still running. When it eventually finishes, `CompleteJob` is called but fails the state transition (already `failed`) — the content is silently discarded.

**Impact:** Resource waste; cancelled jobs continue consuming CPU and API quota. Additionally, the PID in the job record is always `0` because `StartJob(jobID, 0)` is called with a hardcoded zero — so `KillProcessTree` cannot be used to clean up either.

**Fix:** Store a `context.CancelFunc` per async job at launch time; `cancel` action calls that function. Also track the real PID in `StartJob`.

---

### BUG-014 — `handleConsensus` and `handleDebate` ignore `async` parameter

**File:** `pkg/server/server.go`, lines 1002–1029 and 1065–1091

**Description:**
Both `consensus` and `debate` tool definitions include `mcp.WithBoolean("async", ...)`, and users can pass `async=true`. The handlers read and parse other parameters correctly, but `async` is retrieved with `request.GetBool("async", false)` and the returned value is never used. Both handlers always execute synchronously regardless of the `async` flag.

**Impact:** Calling `consensus(async=true)` or `debate(async=true)` blocks the MCP connection for the full multi-turn duration instead of returning a `job_id` immediately as the schema implies.

**Fix:** Implement the same async branching pattern used in `handleExec` and `handleAudit`: create a session+job, launch a goroutine, return `job_id`.

---

## Dependency / Security Notes

- `pkg/executor/pipe/pipe.go` `mergeEnv` (BUG-004): the `args.Command` value is passed directly to `exec.Command`. Command values come from `profile.Command.Base` loaded from config (TOML) — not from user input at request time. No command injection via MCP tool parameters is possible in the current wiring.
- No hardcoded credentials found.
- No SQL injection risk: all SQLite queries in `sqlite.go` use parameterized `?` placeholders.
- `pheromones` map key/value are set by internal code only, not from raw user strings.

---

## Quick Wins (fix in < 30 minutes each)

1. **BUG-004** — `mergeEnv`: one-line fix, prepend `os.Environ()`.
2. **BUG-003** — `handleSessions` missing `kill`/`gc` cases: add two switch cases.
3. **BUG-006** — `defer client.Close()`: one line after nil check.
4. **BUG-008** — `cached` field: thread a bool return value from `Research()`.
5. **BUG-014** — `async` ignored in consensus/debate: copy existing pattern from `handleAudit`.

---

## Findings by File

| File | Bugs |
|------|------|
| `pkg/executor/pipe/pipe.go` | BUG-001, BUG-004, BUG-005 |
| `pkg/executor/conpty/conpty.go` | BUG-004 |
| `pkg/executor/pty/pty.go` | BUG-004 |
| `pkg/session/recovery.go` | BUG-002 |
| `pkg/server/server.go` | BUG-003, BUG-008, BUG-011, BUG-013, BUG-014 |
| `pkg/session/sqlite.go` | BUG-007 |
| `pkg/tools/deepresearch/client.go` | BUG-006 |
| `pkg/orchestrator/audit.go` | BUG-010 |
| `cmd/aimux/main.go` + `pkg/server/server.go` | BUG-009 |
| `pkg/session/registry.go`, `jobs.go` | BUG-012 |
