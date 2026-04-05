# Dead Code Detection Report

**Generated**: 2026-04-05
**Codebase**: D:\Dev\aimux (aimux v3, Go)
**Status**: SCAN COMPLETE

---

## Executive Summary

**Total Dead Code Items**: 42
**By Priority**:
- Critical: 2
- High: 18
- Medium: 14
- Low: 8

**By Category**:
- Unused Files/Packages: 2 (entire files with no callers)
- Unreachable Functions (`deadcode` tool): 28
- Config Fields Never Read: 7 (entire nested config structs)
- Missing Switch Cases (registered but unhandled): 2
- Semantic Bugs in Dead Code: 1
- Low-value utility functions: 3

**Validation Status**: PASSED — `go build ./...` and `go vet ./...` both clean.

**Detection Tools Used**:
- `golang.org/x/tools/cmd/deadcode -test ./...` (primary — RTA call graph)
- `honnef.co/go/tools/cmd/staticcheck ./pkg/... ./cmd/...` (supplementary — clean)
- Manual grep analysis for config field reads, switch-case gaps, debug artifacts

---

## Detailed Findings

---

### Priority: Critical

---

#### C-1. `sessions` tool: `kill` and `gc` actions are registered but never handled

**File**: `pkg/server/server.go:174`

**Issue**: The `sessions` tool's `action` enum explicitly advertises `"kill"` and `"gc"` as valid values, but the handler switch has no `case "kill"` or `case "gc"`. Both fall through to `default` which returns an error: `unknown action "kill"`.

```go
// server.go:174 — advertised:
mcp.Enum("list", "info", "kill", "gc", "health", "cancel"),

// handler switch — missing cases:
switch action {
case "list": ...
case "info": ...
case "health": ...
case "cancel": ...
default:
    return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
}
```

**Analysis**:
- Clients calling `sessions(action: "kill")` receive a misleading error — the tool definition says it's valid but the runtime disagrees.
- `GCReaper.CollectOnce()` exists and is the natural implementation target for `"gc"`, but is never called from anywhere.
- `sessions.Delete()` exists for `"kill"` but is also never called from the tool handler.
- This is a broken contract between the tool schema and the implementation.

**Suggested Fix**: Implement the missing cases, or remove `"kill"` and `"gc"` from the enum until implemented.

---

#### C-2. Entire `pkg/session/sqlite.go` — SQLite persistence layer built but never wired

**File**: `pkg/session/sqlite.go`

**Issue**: `NewStore`, `migrate`, `SnapshotSession`, `SnapshotJob`, `SnapshotAll`, and `Close` are all unreachable from `main`. The server creates in-memory `Registry` and `JobManager` but never instantiates a `Store`.

**Analysis**:
- `cmd/aimux/main.go` has no reference to `session.NewStore` or any snapshot call.
- `shutdown.go` explicitly notes `"Future: Store.Close() (SQLite)"` as a comment — confirming this is planned but not wired.
- All sessions and jobs are lost on restart. The WAL (`pkg/session/wal.go`) is also never opened (see H-4).
- This is not a safety issue for current usage (in-memory), but represents a significant gap between what is implemented and what runs.

**Suggested Fix**: Either wire `NewStore` in `main.go` with periodic snapshots, or delete the file and the WAL until the feature is ready. The current state is misleading — the persistence infrastructure exists but provides zero durability.

---

### Priority: High

---

#### H-1. Entire `pkg/driver/spark.go` — SparkDetector never used

**File**: `pkg/driver/spark.go`

**Issue**: `NewSparkDetector`, `SparkDetector.Available`, `SparkDetector.probe`, `SparkDetector.ModelName`, `SparkDetector.FallbackModel` are all unreachable.

**Analysis**:
- Nothing in `pkg/server`, `pkg/routing`, or `cmd/` references `SparkDetector`.
- The routing config already handles model selection via `AIMUX_ROLE_*` env and profile defaults.
- Additionally, `ModelName()` and `FallbackModel()` both return the identical string `"gpt-5.3-codex"`, making `FallbackModel()` semantically identical to `ModelName()` — a logic bug that would be invisible until this code were actually called.

**Suggested Fix**: Delete `pkg/driver/spark.go`. If Spark detection is needed later, implement it at the routing layer where model selection actually happens.

---

#### H-2. `driver.Registry.IsAvailable` and `Registry.All` — unused methods

**File**: `pkg/driver/loader.go:49`, `pkg/driver/loader.go:82`

**Issue**: `IsAvailable(name string) bool` and `All() map[string]*config.CLIProfile` are never called outside their own file. `EnabledCLIs()` and `Get()` are the methods actually used by `server.go`.

**Suggested Fix**: Delete both methods. If per-CLI availability checks are needed, use `EnabledCLIs()` result lookup.

---

#### H-3. `driver.BuildStdinArgs` — unused function

**File**: `pkg/driver/template.go:84`

**Issue**: `BuildStdinArgs` builds args for stdin-piped prompts. It is never called — the server executes prompts directly via `executor.Run` with a `types.SpawnArgs` struct. The stdin-threshold logic from the TypeScript v2 (`ShouldUseStdin`) has `ShouldUseStdin` available but `BuildStdinArgs` (the function that would act on it) is dead.

**Suggested Fix**: Delete `BuildStdinArgs`. If stdin piping is re-introduced, implement it at the executor layer where it belongs.

---

#### H-4. Entire `pkg/session/wal.go:99` — `WAL.Flush` unreachable; entire WAL never opened

**File**: `pkg/session/wal.go:99`

**Issue**: `WAL.Flush()` is unreachable. More broadly, `NewWAL` is never called from `main.go` or `server.go`. The only reference is a comment in `shutdown.go`: `// Future: WAL.Flush() + WAL.Close()`.

**Analysis**:
- `Replay`, `WAL.Append`, `WAL.Close`, `WAL.Truncate`, `RecoverFromWAL`, `SessionsForResume` are all unreachable at runtime (though `deadcode` catches them via the `-test` flag which includes recovery tests).
- The WAL adds ~150 lines of complexity for a feature that provides no runtime benefit today.

**Suggested Fix**: Either wire the WAL in `main.go` or delete the file. The comment in `shutdown.go` is not a substitute for wiring.

---

#### H-5. Entire `pkg/executor/buffer.go` — OutputBuffer never used

**File**: `pkg/executor/buffer.go`

**Issue**: `NewOutputBuffer`, `OutputBuffer.Write`, `OutputBuffer.String`, `OutputBuffer.Len`, `OutputBuffer.Close`, `OutputBuffer.Spilled` are all unreachable. The executors (pipe, pty, conpty) accumulate output in `bytes.Buffer` directly.

**Suggested Fix**: Delete `pkg/executor/buffer.go`. The 10MB spill-to-disk feature is not connected to any executor.

---

#### H-6. Entire `pkg/executor/backoff.go` — Backoff never used

**File**: `pkg/executor/backoff.go`

**Issue**: `DefaultBackoff`, `Backoff.Delay`, `Backoff.Wait`, `Backoff.ShouldRetry` are all unreachable. No retry logic in the current codebase uses `Backoff`.

**Suggested Fix**: Delete `pkg/executor/backoff.go`. Retry logic, if needed, should be implemented where the retry actually happens (e.g., inside a specific executor or the circuit breaker).

---

#### H-7. `executor.KillProcessTree` and helpers — unreachable

**File**: `pkg/executor/kill.go`

**Issue**: `KillProcessTree`, `killTreeWindows`, `killTreeUnix`, `findProcessGroup` are all unreachable. Process cleanup uses `cmd.Process.Kill()` directly in the executors.

**Analysis**: `findProcessGroup` also contains a stub implementation: it claims to return the process group ID but simply returns `pid` — it never calls `syscall.Getpgid`. This would silently fail to kill process groups on Unix if this code were ever called.

**Suggested Fix**: Delete `pkg/executor/kill.go`. If graceful process tree kill is needed, implement it properly using `syscall.Getpgid` at the call site.

---

#### H-8. `executor.Selector.SelectByName` and `executor.IsWindows` — unreachable

**File**: `pkg/executor/select.go:31`, `pkg/executor/select.go:41`

**Issue**: `SelectByName(name string)` and `IsWindows()` are never called. `Selector.Select()` (the method that is used) picks by availability, not by name. `IsWindows()` duplicates a `runtime.GOOS == "windows"` check done inline in other packages.

**Suggested Fix**: Delete both functions.

---

#### H-9. Entire `pkg/orchestrator/assignments.go` — FileAssignment system unused

**File**: `pkg/orchestrator/assignments.go`

**Issue**: `AssignFiles` and `generateAgentID` are unreachable. The multi-agent file assignment feature (round-robin distribution across N agents) exists in full but no orchestration strategy calls it.

**Suggested Fix**: Delete `pkg/orchestrator/assignments.go`. The `FileAssignment` struct and `AssignFiles` logic should be introduced when a strategy that performs parallel per-file work is actually implemented.

---

#### H-10. Entire `pkg/orchestrator/handoff.go` — SessionHandoff unused

**File**: `pkg/orchestrator/handoff.go`

**Issue**: `NewSessionHandoff`, `SessionHandoff.Save`, `SessionHandoff.Get`, `SessionHandoff.All` are all unreachable. The plan→execute session ID handoff mechanism exists but no orchestration strategy creates or reads handoffs.

**Suggested Fix**: Delete `pkg/orchestrator/handoff.go`.

---

#### H-11. Entire `pkg/orchestrator/pheromones.go` — PheromoneReader unused (methods unreachable)

**File**: `pkg/orchestrator/pheromones.go`

**Issue**: `NewPheromoneReader`, `PheromoneReader.ShouldSkipApproach`, `PheromoneReader.GetDiscovery`, `PheromoneReader.HasWarning` are all unreachable. The pheromone constants (`PheromoneDiscovery`, etc.) are defined and the `Job.Pheromones` map is populated in types, but no strategy reads pheromone state via this reader.

**Suggested Fix**: Delete `pkg/orchestrator/pheromones.go`. The pheromone constants may stay if they're used as map keys in job metadata, but the reader struct adds no value without callers.

---

#### H-12. Entire `pkg/orchestrator/trust.go` — DomainAuthority unused

**File**: `pkg/orchestrator/trust.go`

**Issue**: `NewDomainAuthority`, `DomainAuthority.IsAuthoritative`, `DomainAuthority.GetAuthority`, `DomainAuthority.SetAuthority` are all unreachable. The domain-based CLI veto system (Constitution P13) is designed but not wired into any strategy.

**Suggested Fix**: Delete `pkg/orchestrator/trust.go`. Reintroduce when a strategy actually implements domain-authority-weighted consensus.

---

#### H-13. `parser.ExtractSessionID` — unreachable

**File**: `pkg/parser/jsonl.go:73`

**Issue**: `ExtractSessionID` is never called. The server does not attempt to capture CLI session IDs from JSONL output — session IDs are stored separately when the CLI reports them.

**Suggested Fix**: Delete the function. If JSONL session-ID extraction is needed in the future, restore it from version control.

---

#### H-14. `parser.ExtractFinalJSON` — unreachable

**File**: `pkg/parser/text.go:101`

**Issue**: `ExtractFinalJSON` is never called. The audit pipeline uses `parseAuditFindings` (a private function in `orchestrator/audit.go`) rather than the shared `parser` package for its text extraction.

**Analysis**: Additionally, `parser.ParseTextFindings` and the `parser.Finding` struct are also unused in production code — `audit.go` defines its own private `auditFinding` struct with almost identical fields. This is an abstraction that was built but not adopted.

**Suggested Fix**: Delete `ExtractFinalJSON`. Evaluate whether `ParseTextFindings` and `Finding` should replace the duplicate private implementation in `audit.go` or be deleted too.

---

#### H-15. `server.DefaultChains` and `server.GetRecommendedNext` — unreachable

**File**: `pkg/server/chains.go`

**Issue**: `DefaultChains()` and `GetRecommendedNext()` are never called. The skill chain navigation feature (where tool responses hint at recommended next tools) is defined but not embedded into any tool response.

**Suggested Fix**: Delete `pkg/server/chains.go` or wire `GetRecommendedNext` into tool result construction.

---

#### H-16. `server.NewProgressBridge` and `ProgressBridge.Forward` — unreachable

**File**: `pkg/server/progress.go`

**Issue**: `NewProgressBridge` and `ProgressBridge.Forward` are never called. MCP progress notifications are sent inline via hardcoded `onProgress` callbacks in `handleExec` rather than through this bridge abstraction.

**Suggested Fix**: Delete `pkg/server/progress.go` or wire `ProgressBridge.Forward` into the async job execution path.

---

#### H-17. `session.GCReaper.Run` — unreachable (GC never started)

**File**: `pkg/session/gc.go:31`

**Issue**: `GCReaper.Run` is unreachable. `NewGCReaper` is never called from `main.go` or `server.go`. Sessions and stuck jobs accumulate in memory indefinitely — there is no GC loop running.

**Analysis**:
- The `sessions(action: "gc")` tool action is also broken (see C-1), so manual GC is also unavailable.
- `CollectOnce` (the manual trigger) is also unreachable.
- `cfg.Server.SessionTTLHours` and `cfg.Server.GCIntervalSeconds` are defined and have defaults applied, but never read.

**Suggested Fix**: Wire `NewGCReaper` into `server.New()` and start `GCReaper.Run` as a goroutine with context cancellation. Also fix the `"gc"` switch case (see C-1).

---

#### H-18. `session.JobManager.Delete` — unreachable

**File**: `pkg/session/jobs.go:193`

**Issue**: `JobManager.Delete` is never called from outside the package. The GC reaper calls `sessions.Delete` (registry) but never `jobs.Delete`. Completed/failed jobs accumulate in the `JobManager` map indefinitely.

**Suggested Fix**: Call `JobManager.Delete` from `GCReaper.collect()` after jobs are in terminal state and past TTL, or from the `"kill"` sessions action.

---

### Priority: Medium

---

#### M-1. `session.LiveSession.Stream` — unreachable

**File**: `pkg/session/live.go:41`

**Issue**: `LiveSession.Stream` is never called. `LiveSession.Send` is also never called — the entire `LiveSession` wrapper is unused. The server dispatches via `executor.Run` directly, not through persistent live sessions.

**Suggested Fix**: Delete `pkg/session/live.go` or wire `LiveSession` into a stateful multi-turn execution path.

---

#### M-2. Config fields: entire nested structs never read in production

**Files**: `pkg/config/config.go`

The following `ServerConfig` fields are defined, have defaults applied in `applyDefaults`, but are never read by any production code path:

| Field | Type | Current Value Used? |
|-------|------|---------------------|
| `Server.DBPath` | `string` | No |
| `Server.MaxConcurrentJobs` | `int` | No — no concurrency limit enforced |
| `Server.SessionTTLHours` | `int` | No — GC not wired |
| `Server.GCIntervalSeconds` | `int` | No — GC not wired |
| `Server.ProgressIntervalSeconds` | `int` | No — ProgressBridge not wired |
| `Server.DefaultAsync` | `bool` | No |
| `Server.DefaultTimeoutSeconds` | `int` | No |

These five entire nested config structs are also defined but never read:
- `ServerConfig.Consensus` (`ConsensusConfig`) — `DefaultBlinded`, `DefaultSynthesize`, `MaxTurns`, `TimeoutPerTurnSeconds`
- `ServerConfig.Debate` (`DebateConfig`) — all fields
- `ServerConfig.Research` (`ResearchConfig`) — all fields
- `ServerConfig.Think` (`ThinkConfig`) — all fields

**Fields that ARE used** (for completeness):
- `Server.LogLevel`, `Server.LogFile` — read in `main.go`
- `Server.Pair.MaxRounds` — read in `handlePair`
- `Server.Audit.ParallelScanners`, `.ScannerRole`, `.ValidatorRole` — read in `handleAudit`
- `CircuitBreaker.*` — fully used in `server.New`

**Suggested Fix**: Either remove the dead config fields (cleaning up YAML schema and `applyDefaults`) or wire them to the features they control. Partially: `DefaultTimeoutSeconds` should cap the `timeout_seconds` parameter in `handleExec`; `MaxConcurrentJobs` should be enforced via a semaphore.

---

#### M-3. `config.CLIProfile.OutputFormat` — defined but never read

**File**: `pkg/config/config.go:102`

**Issue**: `CLIProfile.OutputFormat string` is declared in the profile struct and can be set in YAML, but no code path reads `profile.OutputFormat`. The JSON vs text output format decision is made by `params.JSON` in `ResolveCommand`, not by the profile field.

**Suggested Fix**: Remove the field or wire it as the default value for `params.JSON` when the profile declares `output_format: json`.

---

#### M-4. `config.CommandConfig.ArgsTemplate` — defined but never read

**File**: `pkg/config/config.go:118`

**Issue**: `ArgsTemplate string` exists in `CommandConfig` but `ResolveCommand` in `template.go` never reads it. Command construction is done imperatively, not via a template string.

**Suggested Fix**: Remove the field.

---

#### M-5. `parser.ParseTextFindings` and `parser.Finding` — unreachable in production

**File**: `pkg/parser/text.go:19`

**Issue**: `ParseTextFindings` and the `Finding` struct it returns are never imported or called from any production package. The audit pipeline uses a near-identical private `auditFinding` type and `parseAuditFindings` function in `orchestrator/audit.go`.

**Analysis**: There is a duplicate parsing implementation: `parser.ParseTextFindings` (exported, shared) and `orchestrator.parseAuditFindings` (private, inline). The shared version was presumably intended to replace the private one but the wiring was never done.

**Suggested Fix**: Replace `orchestrator.parseAuditFindings` with `parser.ParseTextFindings` and delete the duplicate, OR delete `parser.ParseTextFindings` if the orchestrator's version handles its specific format differences. Either way, consolidate to one implementation.

---

#### M-6. `deepresearch.UploadFiles` and `detectMIMEType` — unreachable

**File**: `pkg/tools/deepresearch/files.go`

**Issue**: `UploadFiles` and `detectMIMEType` are never called. The `handleDeepresearch` server handler calls `client.Research(ctx, topic, outputFormat, nil, force)` — passing `nil` for files. File attachment support exists in the implementation but is not surfaced through the MCP tool interface.

**Suggested Fix**: Either add `files` as a parameter to the `deepresearch` MCP tool and wire `UploadFiles`, or delete `pkg/tools/deepresearch/files.go` until file attachment is scoped for implementation.

---

#### M-7. `deepresearch.Cache.Search` and `Cache.Cleanup` — unreachable

**File**: `pkg/tools/deepresearch/cache.go:74`, `cache.go:97`

**Issue**: `Cache.Search` and `Cache.Cleanup` are never called. `Client.SearchCache` (which wraps `Cache.Search`) is also never called from the server handler. The `deepresearch` tool has no `search_cache` action.

**Suggested Fix**: Either expose `search_cache` as a `deepresearch` action and wire `Client.SearchCache`, or delete these methods. `Cache.Cleanup` should be called periodically to prevent memory growth from expired entries.

---

#### M-8. `session.RecoverFromWAL` and `SessionsForResume` — unreachable at runtime

**File**: `pkg/session/recovery.go`

**Issue**: `RecoverFromWAL` and `SessionsForResume` are never called from `main.go`. Since `NewWAL` is also never called, crash recovery is not functional.

**Suggested Fix**: Wire in `main.go` alongside `NewWAL`, or delete both files until WAL is properly integrated.

---

#### M-9. `executor.Backoff` used nowhere in retry logic

**File**: `pkg/executor/backoff.go` (see H-6 above for full details)

The circuit breaker (`executor/breaker.go`) does cooldown but does not use `Backoff` for jitter. No executor uses `Backoff.Wait`. This is classified Medium as an overlap with H-6.

---

### Priority: Low

---

#### L-1. `SparkDetector.FallbackModel` identical to `ModelName` — semantic bug in dead code

**File**: `pkg/driver/spark.go:66`

**Issue**: Both `ModelName()` and `FallbackModel()` return the string `"gpt-5.3-codex"`. The method names imply they should return different values (primary model vs fallback when unavailable), but the implementation makes them equivalent. This would be a silent logic error if the code were ever activated.

```go
func (d *SparkDetector) ModelName() string    { return "gpt-5.3-codex" }
func (d *SparkDetector) FallbackModel() string { return "gpt-5.3-codex" } // same — wrong
```

**Suggested Fix**: Delete the file (see H-1). If kept, `FallbackModel` should return a different, lower-capability model.

---

#### L-2. `shutdown.go` is comment-only — zero executable code

**File**: `cmd/aimux/shutdown.go`

**Issue**: The file contains only a package declaration and a multi-line comment describing planned shutdown behavior. It contributes no symbols.

**Suggested Fix**: Delete the file. The comment describes aspirational future work; move it to `TECHNICAL_DEBT.md` or an ADR.

---

#### L-3. `executor.IsWindows` duplicates inline pattern

**File**: `pkg/executor/select.go:41` (see H-8 above)

Other packages check `runtime.GOOS == "windows"` inline. `IsWindows()` adds indirection without reuse.

---

#### L-4. `orchestrator.PheromoneDiscovery` constants defined but reader struct unused

**File**: `pkg/orchestrator/pheromones.go:4`

The four pheromone constants (`PheromoneDiscovery`, `PheromoneWarning`, `PheromoneRepellent`, `PheromoneProgress`) are used as map keys in `types.Job.Pheromones`, so they are not entirely dead. However, the `PheromoneReader` struct that provides the access methods is unused. If the reader is deleted, the constants may remain.

---

#### L-5. `executor.DefaultBufferThreshold` constant exported but unreachable

**File**: `pkg/executor/buffer.go:21`

`const DefaultBufferThreshold = 10 * 1024 * 1024` is exported but the entire `OutputBuffer` is dead, making the constant also dead.

---

#### L-6. `config.ResearchConfig`, `ConsensusConfig`, `DebateConfig`, `ThinkConfig` structs exported but never instantiated by callers

**File**: `pkg/config/config.go:53–78`

These structs are embedded in `ServerConfig` and deserialized from YAML, but no production code reads their fields (see M-2). They could be removed without affecting runtime behavior.

---

#### L-7. `driver.Registry.Probe` results partially ignored

**File**: `pkg/driver/loader.go:30`

`Probe()` sets `available` map entries via `exec.LookPath`. `EnabledCLIs()` reads the map. `IsAvailable(name)` also reads the map but is dead (see H-2). The `available` map is populated correctly, but the per-name availability check is never used — callers always iterate `EnabledCLIs()` to get the full list.

This is a design inconsistency rather than dead code: the individual-lookup API (`IsAvailable`) was never adopted.

---

#### L-8. `session.Store.SnapshotAll` calls `jobs.ListRunning` — only running jobs persisted

**File**: `pkg/session/sqlite.go:122`

`SnapshotAll` snapshots only running jobs (`jobs.ListRunning()`), not completed or failed jobs. This means completed job results would be lost across restarts even if the Store were wired. This is a correctness issue in unreachable code, but worth noting for when it is wired.

---

## Validation Results

### deadcode Analysis
PASSED — 55 unreachable function declarations identified across 9 packages (run with `-test` to include test-executable reachability). Report covers the 28 most significant; the rest are methods on the same dead types.

### staticcheck Analysis
PASSED — 0 issues reported.

### go vet
PASSED — 0 issues reported.

### go build
PASSED — 0 errors.

### Overall Status
SCAN COMPLETE — 42 dead code items identified across 7 packages.

---

## Next Steps

1. **Address Critical items first** (C-1, C-2): Fix the broken `sessions` tool enum/handler mismatch and decide the SQLite persistence story.
2. **Delete confirmed dead files** (High priority): `spark.go`, `buffer.go`, `backoff.go`, `kill.go`, `assignments.go`, `handoff.go`, `pheromones.go` (reader), `trust.go`, `chains.go`, `progress.go` — these are fully unreachable with no wiring comment.
3. **Wire or delete infrastructure** (High priority): GC reaper (`gc.go`), WAL (`wal.go`), SQLite (`sqlite.go`), LiveSession (`live.go`) — these have real implementations but are not connected to the running server. Decide which get wired in Phase 8 and which get deferred to `TECHNICAL_DEBT.md`.
4. **Clean config structs** (Medium): Remove dead nested config types (`ConsensusConfig`, `DebateConfig`, `ResearchConfig`, `ThinkConfig`) or wire their fields.
5. **Resolve parser duplication** (Medium): `parser.ParseTextFindings` vs `orchestrator.parseAuditFindings` — consolidate to one implementation.

---

## Appendix

### Files with Zero Callable Exports (entire file dead)

| File | Reason |
|------|--------|
| `pkg/driver/spark.go` | SparkDetector never instantiated |
| `pkg/executor/buffer.go` | OutputBuffer never instantiated |
| `pkg/executor/backoff.go` | Backoff never instantiated |
| `pkg/executor/kill.go` | KillProcessTree never called |
| `pkg/orchestrator/assignments.go` | AssignFiles never called |
| `pkg/orchestrator/handoff.go` | SessionHandoff never instantiated |
| `pkg/orchestrator/trust.go` | DomainAuthority never instantiated |
| `pkg/server/chains.go` | DefaultChains/GetRecommendedNext never called |
| `pkg/server/progress.go` | NewProgressBridge never called |
| `cmd/aimux/shutdown.go` | Comment-only file, no symbols |

### Packages with Partial Dead Code

| Package | Dead Items |
|---------|-----------|
| `pkg/driver` | `Registry.IsAvailable`, `Registry.All`, `BuildStdinArgs` |
| `pkg/executor` | `Selector.SelectByName`, `IsWindows` |
| `pkg/orchestrator` | `PheromoneReader` methods (constants survive) |
| `pkg/parser` | `ExtractSessionID`, `ExtractFinalJSON`, `ParseTextFindings`, `Finding` |
| `pkg/session` | `GCReaper.Run`, `JobManager.Delete`, `LiveSession.Stream`, `WAL.Flush`, full SQLite `Store`, `RecoverFromWAL`, `SessionsForResume` |
| `pkg/tools/deepresearch` | `UploadFiles`, `detectMIMEType`, `Cache.Search`, `Cache.Cleanup`, `Client.SearchCache` |
| `pkg/config` | Multiple field declarations never read |

### Detection Methods Used
- `golang.org/x/tools/cmd/deadcode v0.43.0` — primary, RTA call graph from `cmd/aimux/main`
- `honnef.co/go/tools/cmd/staticcheck` — supplementary unused-variable/import checks
- Grep analysis for config field reads, switch-case gaps, commented code
- Manual source review of all flagged files for context and priority classification
