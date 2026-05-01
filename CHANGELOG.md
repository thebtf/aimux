# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [5.5.0] — 2026-05-01 — AIMUX-17 Launcher debug tool

Adds `tools/launcher/` — a standalone debug binary that decorates `types.ExecutorV2`
over both CLI subprocess (ConPTY/PTY/Pipe) and HTTP API (OpenAI/Anthropic/Google)
backends. Built to investigate executor regressions without rebuilding the aimux
daemon: cuts the debug-loop latency from minutes (rebuild + daemon restart + MCP
shim reconnect + filtered output) to seconds (direct binary invocation with
explicit visibility into resolved SpawnArgs, raw stdout/stderr, classification,
breaker state, and cooldown).

Production binary `cmd/aimux/` is unchanged. Launcher is a dev/operator tool only.

### Added

- `tools/launcher/` — new dev-tool binary (no separate go.mod; main module sub-package, loomlint pattern)
- Subcommand `launcher cli --cli <name> --prompt <text>` — one-shot CLI prompt via the same `pkg/executor` surface as production
- Subcommand `launcher api --provider {openai|anthropic|google} --prompt <text>` — one-shot HTTP API prompt
- Subcommand `launcher session --cli <name>` (or `--provider <p>`) — interactive multi-turn REPL with slash-commands `/quit`, `/reset`, `/dump`, `/save <path>`, `/raw on|off`, `/history`, `/help`
- Subcommand `launcher replay --log <path>` — read JSONL log, filter by event kind, human-readable or `--raw` byte-identical re-emit
- L1 universal ExecutorV2 decorator (`debug_executor.go`) — emits `spawn_args`, `complete`, `classify`, `breaker_state`, `cooldown_state` events per Send; chunk events per SendStream
- L2 pipe-only raw spawn (`raw_spawn.go`) — `--bypass` mode with `io.TeeReader` capturing raw stdout/stderr bytes pre-StripANSI alongside ANSI-stripped line events for side-by-side debug
- JSONL replay log writer (`jsonl.go`) with monotonic seq counter, fsync-after-write contract, 1 MB max line buffer
- 14 typed event payload structs covering `spawn_args`, `spawn`, `stdout`, `stderr`, `chunk`, `exit`, `complete`, `classify`, `breaker_state`, `cooldown_state`, `turn`, `error`, `http_request`, `http_response` kinds
- Default executor backend = `pipe` (deterministic for headless CLIs); `--executor pipe|conpty|pty|auto` flag for parity testing
- NFR-7 disclosure: `--bypass --log <path>` prints 4-line UNREDACTED-secrets warning to stderr at startup
- SIGINT/SIGTERM handler — emits final `error {signal: interrupt}` event and exits 130 (POSIX SIGINT convention)
- `tools/launcher/README.md` — usage, L1 vs L2 capability matrix, 8 verification smoke commands, troubleshooting
- 18 unit tests + 7 subtests across `jsonl_test.go`, `debug_executor_test.go`, `raw_spawn_test.go`, `repl_test.go` — `go test ./tools/launcher/... -count=1` PASS in ~8.5s

### Notes

- HTTP middleware (`http_request` / `http_response` events) probed during implementation: `google.golang.org/genai` SDK does not expose a custom `*http.Client` injection point, so middleware is skipped for all 3 providers (Path B) for parity. Reserved payload structs in code; revisit when google SDK adds support.
- L2 raw capture is pipe-only by design. PTY merges stdout+stderr into ptmx; ConPTY uses terminal emulation — neither matches the byte-level visibility the tee-reader pattern provides for plain pipes. Documented constraint in README.
- Spec artifacts under `.agent/specs/AIMUX-17-launcher-debug-tool/` (gitignored on this public repo): spec.md, plan.md, CR-001 tasks.md (24 tasks + 7 GATEs), clarification report, completeness report, 7 phase-gate reports.

## [5.4.0] — 2026-05-01 — AIMUX-16 Transport-Completeness Pass

Drives all 6 PARTIAL transport capabilities to COMPLETE per the canonical contract
in `.agent/data/audits/transport-contract-2026-05-01.md`. CLI scope: codex, claude,
gemini (9 deferred CLIs out of scope per operator directive — tracked under AIMUX-3 /
AIMUX-4). Final post-audit verdict: 18 / 18 transport capabilities COMPLETE
(`.agent/data/audits/transport-audit-2026-05-01-post-AIMUX-16.md`).

### Added — CR-001 / T11 — Claude profile completeness (`bc276b3`)

- **`config/cli.d/claude/profile.yaml`** — three previously missing T11 fields
  filled to mirror the codex / gemini precedent:
  - `cooldown_seconds: 3600` (1h Anthropic quota refresh; matches gemini).
  - `model_fallback: [claude-opus-4-7, claude-sonnet-4-6, claude-haiku-4-5-20251001]`
    (verified against platform.claude.com/docs/en/about-claude/models/overview at audit time).
  - `completion_pattern: ^---END---$` (anchored canonical sentinel from `claudeCorpus`).
- Explicit `stdin_sentinel: ""` declared per CLI-profile required-fields rule
  (CodeRabbit MAJOR review feedback #136).
- Audit-row regression: claude T11 row entries flip from MISSING/MISSING/MISSING to ✓/✓/✓.

### Added — CR-002 / T10 — Circuit breaker wired into dispatch (`5dfc7d9`)

- **`pkg/executor/select.go::SelectAvailableCLIs`** — pre-select gate filtering CLIs by
  breaker state. CLIs whose breakers are Open are excluded; Closed and HalfOpen-with-
  budget pass through. nil breaker → passthrough. Empty result on all-Open per EC-2.1.
- **`pkg/executor/fallback.go::RecordResultToBreaker`** — maps `ErrorClass` to breaker
  state per EC-2.3: Fatal trips immediately (permanent=true); Quota counts toward
  threshold; Transient / ModelUnavailable / Unknown are deliberate no-ops (flapping-
  network protection).
- **`pkg/executor/fallback.go::RunWithModelFallbackBreaker`** — breaker-aware variant
  of the fallback runner that records the terminal outcome to the registry post-dispatch.
- **19 integration tests** (`pkg/executor/breaker_wire_test.go`) covering the full
  state machine: Fatal trips immediately, Quota counts, Transient stays no-op,
  K Fatals trip → SelectAvailableCLIs excludes, HalfOpen → Closed on healthy probe, etc.

### Added — CR-003 / T13 — Real per-(cli, role) capability probe + cache + refresher (`67685cc`)

- **`pkg/driver/capability_cache.go`** — `CapabilityCache` with TTL + RWMutex (1h
  default, configurable via `cfg.Driver.CapabilityCacheTTLSeconds`); per-(cli, role)
  verified-set storage. `CapabilityRefresher` background goroutine running every TTL/2
  (clamped at 5s minimum). `MakeCapabilityProbeFn` factory closing over a real pipe
  executor; role-shaped prompt asks the CLI to acknowledge the role string in
  `{"role": "<role>", "ok": true}`.
- **`pkg/routing/routing.go::CapabilityVerifier`** — interface + `SetCapabilityVerifier`
  / `CapabilityVerifier()` accessors. Decision matrix in `cliHasCapability`:
  declared=false → false; verified=true → true; cache miss → declared fallback (EC-3.2);
  verified=false → hard exclude. `ResolveWithFallback` drops verified=false CLIs from
  the chain entirely (EC-3.1).
- **`pkg/server/server.go`** — `NewDaemon` constructs `capCache` + `capRefresher` and
  wires the cache via `router.SetCapabilityVerifier`. `RunPhaseB` calls
  `RunWarmupWithCache` so the inline warmup pass populates verified outcomes; refresher
  keeps them fresh. `Shutdown` stops the refresher. `sessions(action="health")` health
  surface adds `capability_cache: {cli → {declared, verified, errors}}` plus TTL +
  refresher tick (FR-3 acceptance signal).
- **`pkg/config/config.go::DriverConfig`** — adds `capability_cache_ttl_seconds` +
  `capability_probe_timeout_seconds` (operator-tunable defaults: 3600s TTL, 5s per-probe
  budget).
- **`config/default.yaml`** — new `driver:` section.
- **21 new tests** (16 driver + 5 routing) covering EC-3.1 / EC-3.2 / EC-3.3 plus
  refresher lifecycle, transient-error no-op, CAS write-back, timeout fallback.

### Added — CR-004 / T3 — Real Win32 ConPTY backend (`ba47f50`)

- **`pkg/executor/conpty/conpty_windows.go`** (FR-4) — real `CreatePseudoConsole` /
  `ResizePseudoConsole` / `ClosePseudoConsole` implementation via the pinned
  `github.com/UserExistsError/conpty v0.1.4` library (MIT, pure Go). Replaces the
  pre-CR-004 documented stub that announced ConPTY support but used a plain
  `exec.Command` pipe. Child process inherits the pseudo-console as stdio so
  `isatty()` returns true (codex chat / aider interactive flows now work).
  Win10 1809+ minimum (build ≥ 17763).
- **`pkg/executor/conpty/conpty_other.go`** (FR-4) — non-Windows stub. Every
  entrypoint returns `ErrPlatformUnsupported`; `probeConPTY()` returns false.
- **`pkg/executor/conpty/conpty_params.go`** — platform-neutral parameter types
  (`openParams`) and constants (`defaultConPTYWidth=120`, `defaultConPTYHeight=30`)
  shared by Windows real path and non-Windows stub.
- **Honest probe (EC-4.1)** — `probeConPTY()` checks Win10 build ≥ 17763, verifies
  `kernel32.dll` exports the ConPTY family, and consults the upstream library's
  `IsConPtyAvailable()`. Every refusal logs an explicit warning. Operator directive
  (`feedback_aimux_interactive_required.md`): NO silent pipe downgrade.
- **`pkg/executor/process.go`** — `ProcessHandle.MarkExited()` exported method.
  Used by external reap goroutines (CR-004's `winConsoleHandle.reapProcess`) that own
  their child-process lifecycle outside `ProcessManager.Spawn`.
- **`.github/workflows/ci.yml`** (NFR-5) — explicit `windows-latest` step asserting
  `TestProbeConPTY_RealHostSucceeds` / `TestOpenWindowsConPTY_SpawnsRealPseudoConsole`
  / `TestExecutor_Run_ProducesOutput` PASS (skip is PROHIBITED — CI fails the build
  on skip; silent skip would re-introduce the deferral pattern CR-004 was filed to fix).
- **8 Windows-only build-tag-gated tests** (`pkg/executor/conpty/conpty_windows_test.go`):
  minimum build gate, kernel32 exports gate, version-lookup failure, real-host happy
  path, `CreatePseudoConsole` actually fires, `ResizePseudoConsole` wired,
  `sync.Once` Close idempotency, end-to-end through `Executor.Run()`.

### Added — CR-005 / T18 / DEF-13 — Loom progress signal port (`63eb88c`)

- **`loom/task.go`** — `Task` extends with `LastOutputLine`, `ProgressLines`,
  `ProgressUpdatedAt` fields at parity with the legacy `JobManager` triple.
- **`loom/store.go` / `loom/progress.go::Store.AppendProgress(taskID, line) (ProgressInfo, error)`**
  — UTF-8-safe truncate (≤ 100 bytes), newline-aware line counter, monotonic timestamp;
  per-row `UPDATE…RETURNING` round-trip serialises concurrent writes via SQLite WAL
  (EC-5.3). Whitespace-only lines preserve `LastOutputLine` (signal primacy) but
  still bump the counter and timestamp so activity-since polls keep advancing.
- **`loom/loom.go::LoomEngine.AppendProgress`** — wraps the store call and emits
  `EventTaskProgress` ONLY when `info.OK == true`, populating `ProjectID` / `RequestID`
  from the returned identity fields (PR #139 CodeRabbit MAJOR fixes #1 + #2: multi-tenant
  subscriber filtering + distributed tracing parity; no-event-for-unknown-task contract).
- **`loom/event.go::EventTaskProgress`** — additive enum value (NFR-6 preserves
  existing subscribers).
- **`loom/workers/streaming_base.go::ProgressSink`** — interface field on
  `StreamingBase`; workers opt into the persistent progress signal alongside the
  existing `OnLine` callback. `*LoomEngine` satisfies `ProgressSink` directly.
- **Schema migration v5** — 3 columns (`last_output_line`, `progress_lines`,
  `progress_updated_at`). Idempotent on re-run; reversible via `MigrateV5Down`
  (SQLite ≥ 3.35 ALTER TABLE DROP COLUMN — bundled with `modernc/sqlite`).
- **`mcp__aimux__status`** Loom branch surfaces `progress_tail` / `progress_lines` /
  `progress_updated_at`; whitelist updated; `progress_updated_at` formatted as RFC3339
  UTC or null.
- **15 new tests** — 11 progress + 4 streaming_base + v5 migration tests.
- **Closes engram issue #173** — closure marker at
  `.agent/data/audits/engram-173-closure-marker.md`. Both code paths (legacy
  JobManager and new Loom) surface progress until JobManager retirement (DEF-13).

### Added — CR-006 / T12 — Discovery cache TTL + mtime invalidation (`80b64b1`)

- **`pkg/driver/discovery.go::DiscoveryCache`** — per-profile binary lookup cache.
  Entries record resolved path, binary mtime, and cached-at timestamp; lookups
  validated against the live file before being served.
- **Invalidation triggers** (per spec EC-6.1..EC-6.4): cache age ≥ TTL (default 24h,
  configurable via `config/default.yaml::driver.discovery_cache_ttl_seconds`); mtime
  change; ENOENT (logs WARN); turn-into-directory; non-regular-file (CodeRabbit MAJOR);
  POSIX exec-bit removal (CodeRabbit MAJOR); negative-result entries always re-probed.
- **Symlink resolution** — `os.Stat` follows symlinks by default (CR-006 review
  removed redundant explicit `EvalSymlinks` step).
- **`pkg/driver/loader.go::Registry.Probe`** — consults `DiscoveryCache.Lookup`;
  warm-cache lookups skip `DiscoverBinary` entirely.
- **`cmd/aimux/main.go`** — reads `cfg.Driver.DiscoveryCacheTTLSeconds` from YAML
  and applies via `Registry.SetDiscoveryCacheTTL` before the first Probe (operator-
  tunable without recompile). DAEMON-ONLY block — explicit invariant comment
  preserves `TestShim_NoSQLiteWrites` (AIMUX-6 NFR-3 regression gate).
- **`pkg/driver/discovery_cache_test.go`** — 9 tests including best-of-5 perf gate
  (`TestRegistry_ProbeWarmCacheUnder1ms` — 12-profile warm Probe ≤ 1ms ceiling on
  busy-CI-tolerant best-of-N sampling).
- **`LoggerFunc`** hook on `DiscoveryCache` — daemon routes invalidation events
  through the project logger (gemini review feedback).

### Fixed — Reviewer findings consolidated across all 6 PRs

- **CR-001 / PR #136** — gemini hardcoded duration bounds replaced with profile-derived
  `CooldownSeconds ± 100s` tolerance; `stdin_sentinel: ""` explicit declaration
  (CodeRabbit MAJOR).
- **CR-002 / PR #137** — `classifyTerminalOutcome` splits `ErrModelUnavailable` from
  `ErrQuotaExhausted` so the CLI-level breaker is not tripped by per-(cli, model)
  unavailability (CodeRabbit MAJOR); `(ExitCode=0, err != nil)` masking guard added —
  preserves `err != nil ⇒ exitCode != 0` invariant required by `ClassifyError`
  (CodeRabbit MAJOR).
- **CR-003 / PR #141** — transient context errors leave the cache slot UNTOUCHED in
  both warmup-time pass and refresher (was: writing `verified=false` for a TTL window
  on transient failure — contradicted EC-3.2 declared-fallback contract);
  `CapabilityRefresher.Stop()` cancels the in-flight probe via private child context
  (was: hostage to probe deadline); `staleEntries` returns `LastProbed` paired with key
  and refresher writes back via `SetIfUnchanged` CAS (was: clobber risk on slow probe
  vs fresh inline write); `resolveCapabilityProbeTimeout` falls back to
  `cfg.Server.WarmupTimeoutSeconds` before the hard 5s default (was: ignored operator-
  raised global warmup timeout); `attachCapabilityHealth` helper unifies
  `sessions(action="health")` and `aimux://health` resource emission (parity);
  `buildCapabilityCacheReport` takes a single `Snapshot()` at the top (was: O(N²·M)
  walk).
- **CR-004 / PR #140** — `reapProcess` uses lifetime context, defensive STILL_ACTIVE +
  context error guard (CodeRabbit CRIT — persistent sessions no longer killed when the
  originating spawn-request context expires); `buildSyntheticCmd` populates
  `cmd.Process` via `os.FindProcess(pid)` so `ProcessManager.Kill` works for ConPTY
  children (CodeRabbit MAJOR — was: `BaseSession.Close` could not terminate ConPTY
  child); stdin write/close errors surface as `ExecutorError` instead of being silently
  dropped (CodeRabbit MAJOR); `TestOpenWindowsConPTY_CloseIsIdempotent` asserts
  "at most one non-nil error across all Close calls" (was: assumed channel-receive
  order); `quoteArg` Windows-correct backslash escaping mirroring stdlib
  `syscall.makeCmdLine`; `conptyWriter.Close` documented as known limitation (upstream
  library does not expose stdin-only close — ConPTY is interactive-TUI-only by design,
  EOF-dependent batch tools belong on the pipe executor; selector enforces this via
  capability flags).
- **CR-005 / PR #139** — `EventTaskProgress` carries `ProjectID` / `RequestID`
  (CodeRabbit MAJOR — was: broken multi-tenant subscriber filtering and distributed
  tracing correlation); `TaskStore.AppendProgress` returns `ProgressInfo` with `OK` flag
  so the engine emits no event for unknown / cancelled tasks (CodeRabbit MAJOR — was:
  contradicted doc-comment); `last_output_line` redacted with the same pattern set as
  `tasks.error` for openai svcacct / Bearer / Google AI key formats (gemini MEDIUM —
  prevented credential leak through MCP status response); `1 + strings.Count(line, "\n")`
  parity formula vs legacy `pkg/session/jobs.go:242` preserved with corrected code
  comment (gemini MEDIUM REJECTED with rationale — semantic parity with JobManager
  caller comparison takes precedence over the misleading comment, which was rewritten).
- **CR-006 / PR #138** — `LoggerFunc` hook on `DiscoveryCache` (gemini); cache-validity
  gate invalidates on non-regular-file or POSIX exec-bit removal (CodeRabbit MAJOR);
  redundant `EvalSymlinks` removed (gemini); `loader.go::Probe` simplified — `cache`
  non-nil after `NewRegistry`, `DiscoveryCache.Lookup` handles nil receiver internally
  (gemini); 1ms wall-clock perf gate switched to best-of-5 sampling (CodeRabbit MAJOR
  — busy-CI-tolerant without weakening the FR-6 ceiling).

### Verification

- **18 / 18** transport capabilities COMPLETE per re-audit
  (`.agent/data/audits/transport-audit-2026-05-01-post-AIMUX-16.md`).
- **27 packages** × `go test -count=1 -timeout 300s` GREEN (incl. `tests/critical/`,
  `test/e2e/`, `loom/` standalone module).
- **`windows-latest`** CI matrix runs ConPTY tests without skip (NFR-5 gate enforced).
- **NFR-2** warm Send avg ≤ 100ms preserved across the ConPTY change
  (`tests/critical/persistent_session_warm_send_test.go`).
- **engram #173** RESOLVED (closure marker at
  `.agent/data/audits/engram-173-closure-marker.md`).
- Master HEAD: `67685cc69f089d01b1209f0224d23c31d0bab50d`.

## [5.3.0] — 2026-04-30 — AIMUX-14 persistent CLI sessions (M6)

Foundation для multi-turn workflows (consensus, dialog, debug-loop) — subprocess
запускается ОДИН раз per Handle и удерживается живым между Send'ами через
sentinel-based completion detection. Stateful TTL idle reaper + Persistent
SpawnMode survival semantics. Wire'ит уже существующие ConPTY/PTY/pipe.Start()
backend методы через ExecutorV2 + SessionFactory layer.

Wired into Swarm Stateful/Persistent paths via MaybeStartSession capability-
detect helper; full Get-path session attachment scoped к operator orchestrator
wiring (consensus orchestrator, dialog driver) when those land.

### Added (AIMUX-14 CR-001 — 6 FRs)

- **`pkg/types/interfaces.go`** (FR-1) — `SessionFactory` side-interface.
  ExecutorV2 implementations MAY satisfy it to expose persistent-session
  capability beyond stateless `Run()` surface. `ErrNotSupported` defensive
  guard for misuse.
- **`pkg/executor/session/session.go`** (FR-3) — `BaseSession.New` accepts
  `completionPattern string` parameter. Reader matches each newline-terminated
  line against compiled regex (Q-CLAR-1 line-anchored semantics);
  `completionMatched` flag carried atomically in `readChunk` so matched-line
  + completion signal delivered without race.
- **`pkg/executor/adapter_{conpty,pty,pipe}.go`** (FR-2) — adapter session-bound
  Send dispatch. New `NewCLI{Pipe,ConPTY,PTY}AdapterWithSession` constructors.
  Send/IsAlive/Close branch on bound session; stateless path preserved
  byte-identically (AIMUX-13 FR-1 immutability invariant).
- **`pkg/executor/{conpty,pty,pipe}/*.go`** (FR-1) — backends satisfy
  `types.SessionFactory` via `StartSession` method. Compile-time assertion
  enforces the contract.
- **`pkg/swarm/swarm.go`** (FR-4) — `MaybeStartSession` capability-detect
  helper. Stateful TTL idle reaper goroutine (default 5min, `WithStatefulTTL(d)`
  override) closes stale Stateful handles; Persistent-mode handles SKIPPED
  (US3 contract). `ErrNotSupported` exported var.
- **`cmd/persistent_testcli/main.go`** — cross-platform echo+sentinel CLI
  used by critical-suite for real-subprocess NFR validation.

### Tests added — 9 new critical-suite + 1 unit-tier

- T010 `TestCritical_PersistentSession_ColdStartUnderTwoSeconds` (NFR-1) —
  6.5ms vs 2s budget.
- T011 `TestCritical_PersistentSession_WarmSendUnderHundredMs` (NFR-2 +
  PID stability) — 25µs avg over 20 warm Sends; PID unchanged.
- T013 trio (NFR-6 + NFR-7) — 0 MB heap growth (5×200 sends), 46k single-
  session Sends/sec, 188k aggregate (4 sessions).
- T012 `TestBaseSession_CompletionPattern_FalsePositiveRate` (NFR-4 +
  CHK011) — 0%/1%/0% false-pos rate across codex/gemini/claude vs 1%
  ceiling.
- T014 `TestCritical_Consensus_SessionReuse_SpawnEventCount` (US2) — 3
  CLIs × 5 rounds → exactly 3 EventSwarmSpawn (12 cache hits).
- T015 `TestCritical_Consensus_PerCLISentinelIsolation` (US2) — 3 distinct
  sentinels, 5 rounds interleaved, zero cross-contamination.
- T018 `TestCritical_PersistentMode_SurvivesStatefulReaper` (US3) — TTL=50ms,
  Stateful reaped, Persistent survived 4×TTL.
- T019 `TestCritical_PersistentSession_CloseGraceful` (NFR-3) — Close ≤ 500ms
  budget; observed ~0ms.
- T020 `TestCritical_PersistentMode_ParallelSameNameReuse` (EC-9) — 2 Persistent
  Gets → identical Handle.ID; exactly 1 spawn event.

### Compatibility

NFR-2 patch-eligible: existing factoryFn signature preserved; all new behavior
opt-in via Stateful/Persistent SpawnMode + capability check. Single-operator
deployment behavior unchanged.

NFR-3 zero new external deps. AIMUX-12+13 critical-suite preserved 18/18.
6 FRs landed (FR-1..FR-6 including cross-tenant invariant preservation).

## [5.2.2] — 2026-04-30 — AIMUX-15 tech debt batch 1

5 actionable DEF entries from `aimux-v5-roadmap` graduated to active resolution.
Patch release scope: zero new feature surface, zero behavior change for end users
running single-operator deployment. All fixes pre-emptive hardening for multi-tenant
deployment trigger OR pre-Layer-5 wiring readiness.

### Fixed (AIMUX-15 CR-001 — 5 FRs)

- **`pkg/swarm/swarm.go`** (DEF-8 / FR-2 / T001) — `spawnLocked` factoryFn ran under
  `s.mu` write lock (latency bomb when Layer 5 wires blocking subprocess factory).
  Replaced with per-key `sync.Map[string]*sync.Mutex` topology: factoryFn runs
  outside `s.mu`; distinct keys execute in parallel; same-key TOCTOU prevention
  preserved via per-key serialization. Bench 96.9 ns/op (1.67× regress, IF-WRONG
  threshold 120 — PASS). New tests: `TestSwarm_ParallelKeysFactoryNonBlocking` (T002)
  + `TestSwarm_SameKeyConcurrentSerialFactory` (T003).
- **`pkg/server/authorize_session.go`** (DEF-10 / FR-3 / T004) — UID enumeration
  oracle closed: shim-visible `SessionAuth.Reason` redacted to the literal `"access denied"`
  on tenant resolution failure. `OperatorUID` audit field preserved (UID stays
  operator-side for forensics). New test: `TestAuthorizeSession_DenyResponseNoUID`
  with exact-match + digit-absence regex anti-stub.
- **`pkg/server/server.go`** (DEF-11 / FR-4 / T005) — multi-tenant audit init
  failure now fail-closed: `os.Exit(1)` via testable `auditFatalfFn` indirection
  instead of degrading to `discardAuditLog` (silent `cross_tenant_blocked` event drop).
  Single-tenant (legacy default, `IsMultiTenant() == false`) preserves warn-and-
  continue behavior — EC-5 dev-iteration unaffected. `initAuditLog` extracted
  for test injection. New tests: `TestServer_AuditInitFatal_MultiTenant` +
  `TestServer_AuditInitWarn_SingleTenant`.
- **`pkg/upgrade/coordinator_test.go`** (DEF-12 / FR-5a / T006) — macOS pkg/upgrade
  flake root cause: `sockaddr_un.sun_path` is 104 bytes; `t.TempDir()` rooted under
  `/var/folders/<hash>/T/<test-name>NNN/...` could push the test path past the
  104-byte limit when combined with `aimux-gr.sock` suffix, yielding intermittent
  EINVAL. Race detector
  amplified timing window. Fix: darwin uses short `/tmp/amx-<pid>-<nano>.sock` ≤
  40 bytes (well within sun_path limit).
- **`test/e2e/upgrade_diag_test.go`** (DEF-12 / FR-5b / T007) — Windows e2e
  `TestE2E_Upgrade_HotSwap_RuntimeEngineMode` explicit `t.Skip()` with tracked engram
  issue #183 (muxcore handoff timing on Windows MoveFileEx semantics, exceeds
  patch budget). Reopen trigger: multi-tenant deployment OR muxcore upstream
  deterministic handoff protocol.

### Documentation

- **`.agent/specs/aimux-v5-roadmap/architecture.md`** — DEF-5/8/10/11/12 entries
  annotated with RESOLVED markers + commit references. Summary table updated. New
  DEF-13 added for escalated worker-dependent Loom v0.2.0 gaps (FR-1 audit
  classified 3 of 5 as worker-driven, deferred to Layer 5 worker landing).
- **`.agent/specs/aimux-15-tech-debt-batch-1/research-loom-gap-audit.md`** —
  T008 audit document classifying all 5 spec FR-1 gaps: 0 addressable, 3
  escalated → DEF-13, 2 already-resolved at v0.1.0 release time.

### Tests

- 5 new tests, all PASS x3 locally:
  - `TestSwarm_ParallelKeysFactoryNonBlocking` — proves DEF-8 fix (parallel-key
    factoryFn execution within 130ms budget vs serial 200ms minimum)
  - `TestSwarm_SameKeyConcurrentSerialFactory` — TOCTOU regression preservation
    (50 goroutines, 1 spawn, 1 handle ID)
  - `TestAuthorizeSession_DenyResponseNoUID` — UID-absence regex anti-stub
  - `TestServer_AuditInitFatal_MultiTenant` — multi-tenant fail-closed path
  - `TestServer_AuditInitWarn_SingleTenant` — EC-5 single-tenant preservation
- AIMUX-12 + AIMUX-13 critical-suite preserved unchanged (18/18 PASS).

### Release vehicle

Patch release v5.2.2 per AIMUX-15 clarification C2: debt clearing ships
separately from AIMUX-14 feature work (different test surface, different
blast radius). Zero breaking changes; single-operator deployment unaffected.

## [5.2.1] — 2026-04-30 — AIMUX-13 PRC v7 audit follow-ups

PRC v7 agent-trio audit follow-up cycle on the AIMUX-13 tenant-aware Swarm
release. 8 inline fixes applied (zero-deferral discipline per autopilot
promise) + 4 entries deferred to roadmap. Patch release; no behavior change
for single-operator deployments.

### Fixed

- **BUG-011** — `emit{Spawn,Close,Restart}` canonicalize TenantID
- **BUG-014** — prune dead handles inside `Get` find-or-spawn (memory leak)
- **BUG-005** — `NewDispatchMiddleware` nil-guard `auditLog` + eager panic on nil registry
- **BUG-007** — `TestSwarm_DistinctTenantsDistinctHandles` partition assertion
- **BUG-008** — `Health()` comment fixed (first-write-wins, not last-write-wins)
- **BUG-010** — `joinErrors` wrapper removed (`errors.Join` inline)
- **HIGH-2** — distinct audit `Reason` strings (SIEM disambiguation)
- **HIGH-3** — `spawn`/`spawnLocked` DRY → `makeHandle` helper
- **MEDIUM-1** — byte-identical test asserts response content (anti-stub)
- **F2 S3** — `pkg/tenant/loader.go` rejects `legacy-default` reserved name

### Deferred

- **DEF-8** (BUG-012 `spawnLocked` `factoryFn` under lock — Layer 5 dependency) — resolved later in v5.2.2
- **DEF-9** (F1 Windows `peerUID=0` — pre-existing AIMUX-12, blocked muxcore #110)
- **DEF-10** (F3 audit `Reason` UID — pre-existing AIMUX-12) — resolved later in v5.2.2
- **DEF-11** (F4 `DiscardLog` fail-open — pre-existing AIMUX-12) — resolved later in v5.2.2

### Won't Fix (adjudicated)

- **BUG-013** audit `ToolName` leak — security F5 CLEAR (`h.Name` is the public CLI type)
- **BUG-009** unused context import — false alarm (used 5x)
- **MEDIUM-2** `discardAuditLog` alias — false alarm (unexported)
- **HIGH-1** `emitRestart` anti-flood — intentional design (health failures observability-required)

### Notes

Pre-existing macOS `pkg/upgrade` flake unchanged from v5.2.0 — tracked
separately in CR-002 of the ci-stability spec.

Files changed: `pkg/server/dispatch_middleware.go`, `pkg/swarm/swarm.go`,
`pkg/swarm/swarm_bench_test.go`, `pkg/tenant/loader.go`,
`tests/critical/swarm_legacy_byte_identical_test.go`. Audit source commit
`c14dec2`.

## [5.2.0] — 2026-04-29 — AIMUX-13 tenant-aware Swarm

Layer 2 (process pool manager) extended с tenant прошивкой continuing AIMUX-12 multi-tenant
foundation. Last cross-cutting layer без tenant scoping closed. Foundation для Phase C
reactivation Layer 3-4 dormant code post-purge.

### Added (AIMUX-13 tenant-aware-swarm, 5 phases T001-T015)

- **`pkg/swarm/swarm.go`** — `Handle.TenantID string` field (immutable after spawn for ALL
  SpawnMode); `Swarm.auditLog audit.AuditLog` field; `Swarm.New(factory, auditLog)` constructor;
  `registryKey(tenantID, scope, name)` triple partition; `checkTenant(ctx, h)` cross-tenant
  enforcement; `emitSpawn / emitClose / emitRestart` audit hooks (legacy-mode anti-flood guard
  via `isMultiTenantID` discriminator).
- **`pkg/swarm/swarm.go`** — `ErrHandleNotFound` exported var. Cross-tenant Send / SendStream
  returns this error (NEVER 403) per CHK079 defense-in-depth — generic message, no tenantID
  leak.
- **`pkg/tenant/context.go`** — exported `tenant.FromContext(ctx) (TenantContext, bool)` and
  `tenant.WithContext(ctx, tc) context.Context` helpers. Canonical `tenantContextKey{}` type
  migrated from pkg/server (pkg/server.TenantContextFromContext now thin alias).
- **`pkg/audit/types.go`** — 3 new EventType constants: `EventSwarmSpawn` (`"swarm_spawn"`),
  `EventSwarmClose` (`"swarm_close"`), `EventSwarmRestart` (`"swarm_restart"`).
- **`tests/critical/swarm_cross_tenant_test.go`** — `TestCritical_Swarm_CrossTenantHandleBlocked`
  (rule #10 release blocker): Alice's Stateful Handle, Bob's Send → ErrHandleNotFound + audit
  EventCrossTenantBlocked + no info leak in error message.
- **`tests/critical/swarm_legacy_byte_identical_test.go`** — `TestCritical_Swarm_LegacyMode_ByteIdentical`
  (rule #10 release blocker): empty TenantContext → byte-identical pre-AIMUX-13 behavior + zero
  EventSwarmSpawn/EventSwarmClose emit'ы (anti-flood per FR-4).
- **`tests/critical/swarm_legacy_canonicalization_test.go`** — `TestCritical_Swarm_LegacyCanonicalization_SamePartition`
  (rule #10 release blocker, CodeRabbit MAJOR PR #131 regression guard): empty ctx and
  `tenant.LegacyDefault` ctx must hit the SAME registry partition (split-brain prevention).
- **`pkg/swarm/swarm_bench_test.go`** — `BenchmarkSwarm_Get` (NFR-1 ≤ 200 ns/op overhead target;
  measured **57.97 ns/op** — 3.4x under budget); `BenchmarkSwarm_Get_Stateless` (reference
  full spawn cost 112.2 ns/op); `BenchmarkSwarm_Get_Concurrent_100Tenants` (NFR-2 linear scaling
  ~160 ns/op per Get for 1000 Gets across 100 tenants); `TestSwarm_SameTenantConcurrentGet`
  (BUG-003 anti-TOCTOU verification — 50 goroutines, 1 cached Handle, 1 spawn).

### Changed

- **`pkg/swarm.New` constructor signature** — `New(factoryFn FactoryFn) *Swarm` →
  `New(factoryFn FactoryFn, auditLog audit.AuditLog) *Swarm`. nil-auditLog falls through to
  audit.DiscardLog{}. ALL existing callers (pkg/swarm/swarm_test.go — 17 sites) migrated.
- **`pkg/swarm.registryKey`** — internal helper now keyed `(tenantID, scope, name)` triple.
  Separator `|`. Collision impossibility cross-referenced to AIMUX-12 W1 sanitizeTenantID
  ASCII allowlist.
- **`pkg/server.TenantContextFromContext`** — refactored к thin alias of `tenant.FromContext`.
  Canonical key type lives in pkg/tenant.

### Architecture

- **NFR-3-Security (trust boundary IRREVERSIBLE):** Swarm trusts in-process ctx as
  authoritative. DispatchMiddleware = single source of truth для tenant identity resolution.
  Forge requires system compromise already (anti-requirements documented in spec).
- **NFR-Persistent-Honesty:** Persistent SpawnMode survives `Shutdown(ctx)` but NOT hot-swap
  (different daemon process). FD migration handoff (`sendmsg SCM_RIGHTS` + `pidfd`) deferred to
  future AIMUX-N when first production Persistent consumer materializes (currently zero
  non-test consumers).
- **Tech debt vacated** — TECHNICAL_DEBT.md formerly tracked 7 items now all migrated to
  active spec roadmap entries (`.agent/specs/aimux-v5-roadmap/architecture.md` Phase D1 +
  DEF-1..DEF-7). File now empty per autopilot promise discipline.

### Spec / artifacts

- `.agent/specs/aimux-13-tenant-aware-swarm/` — full SpecKit pipeline complete:
  user_job_statement, spec.md (5 FRs + 5 NFRs + 3 USs + 8 edge cases + 5 clarifications),
  plan.md (Reversibility Audit PASS, 6 ADRs, Standard parallelism gate), checklists/security.md
  (35 items, 100% traceability), completeness-report.md (PROCEED), tasks.md
  (CR-001 — 15 T-tasks + 5 G-gates).
- `.agent/specs/aimux-v5-roadmap/architecture.md` updated с DEF-7 (TestShim_Latency outlier
  upstream tracking).

### Test evidence

- 5 phase gates G001-G005 PASS evidence saved under `changes/CR-001-initial-scope/evidence/`.
- Full repo `go test ./... -timeout 300s` green: 39 packages including pkg/swarm (0.234s),
  pkg/tenant (1.822s), pkg/audit (0.250s), pkg/server (2.292s), tests/critical (0.821s),
  test/e2e (56.0s), tools/loomlint.

## [5.1.0] — 2026-04-29 — AIMUX-12 multi-tenant isolation

### Added (AIMUX-12 multi-tenant-isolation, 9 phases P0-P8)

- **In-process tenant isolation within ONE daemon** — public multi-tenant production target. Drop separate-daemons-per-tenant pattern.
- **`pkg/tenant/`** — TenantContext (immutable), TenantConfig (UID/Role/RateLimit/Quota), TenantRegistry (atomic.Pointer hot path per NFR-1), loader (yaml.v3 with duplicate detection), bootstrap (`--bootstrap-operator-uid` flag, mode 0600), hot-reload (SIGHUP coalesce + atomic Swap), legacy-default fallback.
- **`pkg/audit/`** — non-blocking AuditLog + FileAuditLog (4096 buffer, atomic dropped counter, mode 0600). 6 event types: allow, deny, rate_limited, cross_tenant_blocked, tenant_config_change, loom_submit_rejected.
- **`pkg/session/repo.go`** — TenantScopedStore + sessions.db tenant_id V4 migration. Cross-tenant Get returns ErrNotFound (NEVER 403) per CHK079.
- **`loom/tenant_engine.go`** — TenantScopedLoomEngine + loom.tasks tenant_id column. Cross-tenant Get/Cancel returns ErrTaskNotFound. FR-17 quota enforcement via live SQL CountForTenant.
- **`pkg/server/dispatch_middleware.go`** — DispatchMiddleware с ResolveContext возвращает ErrTenantUnenrolled в multi-tenant mode (NEVER LegacyDefault privilege escalation). NFR-10 dispatch overhead 44-49 ns/op (1000× под 50 µs target).
- **`pkg/logger/log_partitioner.go`** — per-tenant log routing via NotificationHandlerWithSessionMeta. sanitizeTenantID rejects path traversal payloads (route to fallback).
- **`pkg/ratelimit/tenant_limiter.go`** — atomic.Int64 token buckets, CAS double-refill protection, OnFrameReceived hook (<50 ns/op, well под 1 ms muxcore budget).
- **`pkg/server/authorize_session.go`** — AuthorizeSession callback adapter. Single-shot per-session admission gate. Drain check (FR-12) wired live.
- **`docs/PRODUCTION-TESTING-PLAYBOOK.md`** — 20 customer-mode scenarios across 7 phases (rule #11).
- **`tests/critical/`** — 15 @critical tests covering session/loom/log/authZ/rate-limit/dispatch isolation (rule #10).

### Changed

- **muxcore v0.23.0-alpha.1 → v0.24.0** — multi-tenant extensions ConnInfo, SessionMeta, AuthorizeSession, OnFrameReceived. Backward-compat byte-identical via nil-default. Closes upstream issues #109/#110/#111/#112.
- **`go.mod`** — `replace github.com/thebtf/aimux/loom => ./loom` for local dev.

### Fixed

- **CR-002 (11 blockers)** — earlier in day, sendWithTimeoutVia leak, IPCSink Close race, channelBuf 1024→4096, DrainSaturated counter, sanitizeTag injection prevention, FR-12 honesty rewrite.
- **engram#180** — EnsureSessionID UUID generation for empty input в 6 stateful think patterns.
- **AIMUX-12 PRC v3 blockers (8 findings, 6 fix commits)**: B1 priv-esc (ResolveContext multi-tenant denial), B2 drain bypass (IsDraining wired), B3 sigCh footgun, B4 token bucket CAS starvation, B5 ctx stub removal, B6 drain TODO contract violation, B7 div-zero panic, B8 BeginDrain leak.

### Resolved (engram issues)

- **#173** progress_tail loom regression — works-as-designed for post-purge surface.
- **#174** hot-swap structurally impossible from child upstream — path-3 honest-deferred.
- **#176** log rotation 146 GB OOM — lumberjack + sole-writer FR-2 + per-line cap shipped.
- **#180** structured_argumentation session_id="" collision — UUID helper applied across 6 patterns.

### Fixed (PRC v5 W-warnings — все closed в v5.1.0, не deferred)

- **W1 [S3]** `pkg/logger/log_partitioner.go` — sanitizeTenantID NFC normalization + strict ASCII allowlist `[a-zA-Z0-9_-]`. Closes Unicode visual-spoof: Cyrillic homoglyphs (`аcme` U+0430), RTL override (U+202E), zero-width joiner, denormalized combining marks. Path traversal stays blocked. (commit 6461258)
- **W2 [S3]** `loom/loom.go` + `loom/tenant_engine.go` — per-tenant submit lock (sync.Map[tenantID]→sync.Mutex) closes quota TOCTOU. N concurrent Submits no longer all pass depth=cap-1 check. Test: TestLoomSubmit_BurstConcurrentSubmits_RespectsQuota PASS на 5 repeats. (commit 70b9a7c)
- **W3 [S4]** `pkg/tenant/hotreload.go` — Swap snapshot before BeginDrain (was inverted). Closes sub-ms admission window for removed tenants. (commit 0b38c38)
- **W4 [P2]** `pkg/ratelimit/tenant_limiter.go::SetAuditLog` — reflect-based typed-nil guard. `var x *FileAuditLog; SetAuditLog(x)` no longer panics in Emit. (commit 188be7f)
- **W5 [MINOR]** `pkg/server/authorize_session.go::Authorize` — negative PeerUid → AuthDeny before uint32 cast. Closes Windows edge-case wraparound. (commit 2872f22)
- **W6 [MINOR]** `pkg/tenant/tenant.go` — `PeerUid` field → `PeerUID` (Go initialism convention). Cross-package rename via Serena. (commit 223ba99)
- **N1 [P3]** `pkg/server/server_session.go` — explicit deny via `EmitUnenrolledBlocked` + JSON-RPC -32000 on unknown ResolveContext error class. Was log.Warn fallthrough — defense-by-default violation. (commit 550dde0)

### Removed

- `AIMUX_NO_ENGINE` env var fully removed. It was deprecated in AIMUX-6 and already ignored; aimux now has no remaining env check, warning, or test branch for that legacy path.

### Added (AIMUX-10 loom-task-scoping)

- **`sessions(action="list", all=true)`** — new boolean parameter on the `sessions`
  MCP tool. When `all=true`, returns a cross-engine global view of all tasks sharing
  the database (tasks from every daemon). Default (`all=false` or param omitted)
  preserves the existing per-daemon scoped behaviour.
- **`engine_name` field on session rows** — every task row in `sessions` responses
  now includes `engine_name` identifying which daemon owns it. Populated for all
  rows including cross-engine results from `all=true`.
- **`loom.scope.global_query` audit log event** — emitted at INFO level (slog stderr)
  on every `sessions(action="list", all=true)` call. Fields: `engine_name` (calling
  daemon), `rows` (count returned). Stable key for log aggregation pipelines.

### Changed (AIMUX-10 loom-task-scoping)

- **`sessions(action="list")` default behaviour is now scoped** — returns only tasks
  belonging to the calling daemon's engine. Previously returned tasks from all daemons
  sharing the same database. Operators running a single daemon are unaffected.
  Multi-daemon operators: use `all=true` to restore the previous unscoped view.
- **Loom module updated to v0.2.0** — `NewTaskStore` and `NewEngine` signatures now
  require an `engineName` argument. `List`, `Count`, and `MarkCrashed` are scoped by
  `engine_name`. New `ListAll`, `CountAll` methods for cross-engine queries.
  Schema migration v3 applied automatically; existing databases upgrade in-place.
  Full details in `loom/CHANGELOG.md` v0.2.0 section.

## [4.8.1] — 2026-04-24

### Fixed

- Updater zip extraction: `go-selfupdate` `UpdateTo` searched for binary by temp file
  name (`aimux-update-*.tmp`) instead of the real name (`aimux.exe`). Fix uses a temp
  directory with the correct binary basename so archive extraction succeeds.

## [4.8.0] — 2026-04-24

Minor release: **hot-swap upgrade activation** (AIMUX-7 follow-up) + **intelligent work routing** (AIMUX-8) — BM25 semantic agent discovery, think pattern advisor + enforcement gates, `critique` MCP tool.

### Added

#### AIMUX-8: Intelligent Work Routing

- **BM25 semantic agent discovery (`pkg/routing/`)** — `Scorer` interface + `TermIndex` in-memory
  inverted index with IDF. `Score` p50 676 ns, `Rank200` p50 49 ms (both well under NFR thresholds).
  `BM25Scorer` replaces the previous keyword-intersection `scoreMatch` function.
- **`agents(action="find")` semantic ranking** — `ListCandidates` now uses BM25 ranking with
  feedback-adjusted scores. Response: `{query, matches, count}` with ranked candidates.
- **`agents(action="run")` auto-select + `selection_rationale`** — when no `agent` is specified,
  `SemanticSelect` (BM25 + `FeedbackTracker`) picks the best match and injects
  `selection_rationale: {agent_name, semantic_score, success_rate, adjusted_score, reason}` into
  the response envelope.
- **`FeedbackTracker` (`pkg/agents/feedback.go`)** — decay-weighted success-rate tracker.
  `adjustedScore = 0.7*semanticScore + 0.3*successRate`. 7-day half-life. Project-scoped.
- **`DispatchHistory` (`pkg/agents/history.go`)** — SQLite-backed dispatch history table with
  `success_rate(agentName, taskCategory)` returning decay-weighted rates.
- **Think enforcement gates (`pkg/think/gates.go`)** — `EnforcementGate` checks per-pattern
  thresholds (min steps, min evidence, max confidence without evidence) for 10 patterns.
  Every think response carries `gate_status: "complete"|"incomplete"` + `gate_reason`.
- **Pattern advisor (`pkg/think/advisor.go`)** — `PatternAdvisor` evaluates result content via
  BM25 against all pattern descriptions. Returns `advisor_recommendation: {action, target, reason}`.
  Detects domain shift and suggests pattern switch (max 3 per session).
- **Pattern stack (`pkg/think/session.go`)** — `PushPattern/PopPattern/CurrentPattern` with
  state snapshot/restore on push. Max depth 5.
- **`critique` MCP tool (`pkg/server/server_critique.go`)** — structured code/design review.
  4 built-in lenses: `security`, `api-design`, `spec-compliance`, `adversarial`. Delegates to any
  available CLI, parses output into `findings: [{severity, location, issue, suggested_fix}]`.
  Falls back to `raw_output` when CLI returns non-JSON.
- **`agent` tool deprecation** — `agent(agent="X", prompt="...")` now emits `deprecated: true` in
  its response and routes internally through `agents(action="run")`. New callers should use
  `agents(action="run", agent="X", prompt="...")` directly.

#### AIMUX-8: Integration Tests

- `test/e2e/agent_semantic_test.go` — verifies `agents(action=find)` returns `{query, matches, count}`,
  and `agents(action=run)` returns either `selection_rationale` (when agents registered) or a
  structured `{action:"choose_agent", candidates}` fallback.
- `test/e2e/think_advisor_test.go` — verifies `debugging_approach` response carries
  `gate_status` and `advisor_recommendation.action` under the `result` key of the guidance envelope.
- `test/e2e/critique_test.go` — verifies `critique(lens="security")` response carries `lens` and
  `cli_used`; validates `findings` array or graceful `raw_output` fallback.

#### Hot-swap upgrade activation

- Hot-swap e2e coverage on all supported platforms: `test/e2e/upgrade_hot_swap_test.go`, `test/e2e/upgrade_hot_swap_windows_test.go`, and fallback-path coverage in `test/e2e/upgrade_fallback_test.go`.
- Structured upgrade completion logging in `pkg/upgrade/coordinator.go`: every apply emits `module=server.upgrade event=upgrade_complete prev_version=... new_version=... method=... duration_ms=... transferred_ids=[...]`, with `WARN` on deferred fallback (`handoff_error`) and `ERROR` on hard failure.

### Changed

- `upgrade(action="apply")` now supports explicit `mode=auto|hot_swap|deferred` semantics end-to-end. `auto` tries daemon-side graceful restart first, `hot_swap` fails hard if live handoff is unavailable, and `deferred` skips live handoff entirely.
- Upgrade responses now distinguish live success from fallback: `updated_hot_swap` on successful daemon handoff, `updated_deferred` when `auto` falls back after a live-path failure, and `updated` for explicit deferred mode to preserve the legacy contract.
- `pkg/server/server.go` no longer carries stale v4.3.0 references in upgrade-path comments; legacy behavior is described semantically instead of by old release number.

### Migration

- Operators that want the old behavior explicitly should call `upgrade(action="apply", mode="deferred")`.
- Automation that calls `upgrade(action="apply")` in engine mode should accept both `updated_hot_swap` and `updated_deferred`; inspect `handoff_error` on fallback before deciding whether to alert or retry.
- Release version finalization remains a release-step concern; current dev work continues to use the build-time `pkg/build.Version` source of truth.

## [4.7.0] - 2026-04-22

Minor release: DX Self-Documentation (AIMUX-7) — agent-first discovery experience.

### Added

- **Dynamic instructions (FR-1..FR-4).** Replaced static `const aimuxInstructions` with `buildInstructions()` that generates MCP instructions at connect time using live server state. Instructions include live CLI list with roles, value proposition ("free for you"), start-here directive (`sessions(action=health)` + `agents(action=find)`), guide prompt reference, and tool category explanations. Warmup-incomplete fallback shows configured profiles with "(warmup in progress)" suffix.
- **23 separate MCP tools per think pattern (FR-5).** Split single `think` tool into 23 individual tools (`debugging_approach`, `decision_framework`, `sequential_thinking`, etc.), each with typed `inputSchema` derived from `PatternHandler.SchemaFields()`. Agents discover required/optional fields from `tools/list` — zero trial-and-error. Total tool count: 36.
- **Situation-based tool descriptions (FR-8).** Every tool description follows the pattern: `[category tag] agent situation + function + trigger`. Categories: `[solo — free]`, `[delegate — external CLI, free for you]`, `[manage — server state, no cost]`. Descriptions address agent STATE ("When you're stuck..."), not just tool function — unique approach not found in reference projects.
- **MCP ToolAnnotations on all 36 tools (FR-6).** `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint` set per tool. Solo patterns: readOnly + idempotent. Delegate tools: openWorld. Uses mcp-go v0.47.0 `WithToolAnnotation()`.
- **`SchemaFields()` and `Category()` on PatternHandler interface.** All 23 patterns export their field schemas (type, required, description, enum values) and category ("solo"). Enables dynamic tool registration without hardcoded schemas.
- **Actionable CLI unavailability errors (FR-7).** When exec/consensus/debate/dialog routes to an unavailable CLI, error now includes: requested CLI name, reason, list of available alternatives, and suggested action. Multi-CLI tools (consensus, debate) report "Requires 2+ CLIs" when only 1 available.

## [4.6.1] - 2026-04-21

Patch release fixing the shim stdin-EOF race that blocked `/mcp reconnect aimux-dev` on v4.6.0.

### Fixed

- **Shim stdin-EOF race (engram mcp-mux#153).** Bumped muxcore `v0.21.4 → v0.21.6` and wired `StdinEOFPolicy: owner.StdinEOFWaitForDisconnect` in `cmd/aimux/shim.go`. The shim no longer exits when CC closes its stdin pipe during lifecycle events (reconnect, session end). Root cause: MCP spec mandates stdin close as shutdown signal, but the shim's stdin is CC's internal pipe — not a user-initiated shutdown. muxcore v0.21.5 (PR #96) added the two-mode policy; v0.21.6 (PR #98, engram #157) exposed the passthrough via `engine.Config`.
- Updated 2 e2e test skip reasons (`TestE2E_Agent_AsyncProgressNotification`, `TestRegression_SC9_NilErrorWrap`): no longer blocked by muxcore#153 (stdin-EOF race resolved), now skipped due to separate async job lifecycle issue in daemon+shim e2e mode. Unit-level regression guards in `pkg/executor/fallback_test.go` remain active.

## [4.6.0] - 2026-04-21

Minor release bundling (1) AIMUX-6 mode-aware startup gate (shim vs daemon mode detection before heavy init — fixes the "aimux tools disappear / think hangs / reconnect fails" symptom class) and (2) the v4.5.3 codex-reliability hotfix (breaker reset on refresh-warmup, correct classification of `503 auth_unavailable`, and default codex model bumped to `gpt-5.4` per OpenAI's March-April 2026 deprecation of the `gpt-5.3-codex` family). The v4.5.3 PATCH release was consolidated into v4.6.0 rather than shipped separately.

### Added

- **Mode-aware startup gate (AIMUX-6).** `aimux.exe` now detects daemon vs shim mode via `detectMode()` in `cmd/aimux/mode.go` **before** any heavy init. Shim processes skip `aimuxServer.New*`, `driver.NewRegistry/Probe`, `driver.RunWarmup`, LoomEngine boot, and SQLite open entirely — they construct only the minimum needed to serve as a stdio↔IPC bridge via muxcore. Typical shim startup target: <200ms p95 (NFR-1). Eliminates the shim-induced `sessions.db` reconcile that caused the observed "aimux tools disappear / think hangs / reconnect fails" symptom class (investigation `019dac5a-7cdf-79b3-9bfb-e73c6c7b2134`).
- `aimuxServer.NewDaemon(cfg, log, reg, router) *Server` — the explicitly-named daemon-mode constructor. All production callers migrate here. `aimuxServer.New` remains as a deprecated delegator (one-time `log.Warn` via `sync.Once`) so existing tests compile unchanged.
- `pkg/build.Version` — thin package exposing the build-time `Version` constant with zero dependencies. Allows shim binaries to reference version info without pulling in the full daemon dependency graph via `pkg/server`.
- `cmd/aimux/shim.go`:
  - `runShim(ctx, cfg, log) error` — shim-mode entry point. Reads `AIMUX_ENGINE_NAME` with default `"aimux"` to honour dev/prod socket isolation (same contract as PR #71).
  - `stubSessionHandler` — defence-in-depth `muxcore.SessionHandler` for the shim branch. Never invoked in normal operation (muxcore `runClient` is a pure stdio↔IPC bridge). If ever dispatched to (indicates a future muxcore regression), returns JSON-RPC `INTERNAL_ERROR -32603` with an actionable hint pointing the operator to `aimux.log`; logs the method/id/stack-trace ONCE per process via `sync.Once` to prevent log flood.
- FR-8 audit log line emitted on every `aimux.exe` invocation, immediately after `aimux v<ver> starting`: `aimux v<ver> mode=<daemon|shim> signal=<arg|default>`. Postmortem-friendly — first two lines of any log identify the startup path taken.
- `test/e2e/shim_startup_test.go` — mechanical regression gate via fsnotify on `sessions.db{,-wal,-shm}`. Asserts zero write events during shim lifetime (NFR-3). Catches any future drift where daemon-only init leaks into the shim path.
- `test/e2e/shim_latency_test.go` — NFR-1 p95 latency gate (20-invocation sample against warm daemon; p95 < 200ms, p50 < 100ms).
- `cmd/aimux/main_test.go` — 8-row table test for `detectMode` covering all combinations of daemon-flag / `MCP_MUX_SESSION_ID` / `AIMUX_NO_ENGINE` (NFR-4 determinism).

### Changed

- `go.mod`: `github.com/thebtf/mcp-mux/muxcore v0.21.1 → v0.21.4`. Brings PR #95 upgrade-restart split-state fix, PR #92 `Daemon()/Mode()/Ready()/ControlSocketPath()` accessors, and confirmed-public `engine.Config.DaemonFlag` field (consumed by `detectMode`). Zero API breakage.
- `go.mod`: adds `github.com/fsnotify/fsnotify v1.8.0` as direct test-only dep (pinned via `test/e2e/deps.go` tools-tag stub) for NFR-3 regression gate.
- `pkg/server/server.go`: constructor split. `New` is now a deprecated delegator to `NewDaemon`. Existing callers remain functional; migrations to `NewDaemon` are a follow-up PR concern.

### Deprecated

- `MCP_MUX_SESSION_ID` env var (proxy-mode bypass). Setting this variable now causes `aimux` to refuse to start with an explicit stderr error pointing to this release's notes. Rationale: the previous proxy-mode code path was built without a clear integration semantic and the correct integration puts mcp-mux in the shim role (see AIMUX-6 spec FR-4 "Future Integration" note). For emergency local debugging, set `AIMUX_ALLOW_LEGACY_PROXY=1` to bypass the rejection — undocumented escape hatch that may be removed without notice.
- `aimuxServer.New()` callers outside `cmd/aimux/main.go` — emits a one-time `log.Warn` at first use. Migrate to `aimuxServer.NewDaemon()` in your next PR.

### Removed

- `AIMUX_NO_ENGINE=1` env-var bypass (stdio-direct path). Setting the variable now emits a single deprecation `log.Warn` to `aimux.log` and an `fmt.Fprintf` stderr notice, then is otherwise ignored — `aimux` always runs via muxcore engine (daemon or shim branch per `detectMode`). Rationale: reduces startup path surface from 4 potential branches (daemon / shim / proxy / no-engine) to exactly 2.

### Fixed

- Root cause of the "aimux tools disappear / think hangs / reconnect fails" symptom class. Prior to v4.6.0, every `aimux.exe` invocation (daemon OR shim) called `aimuxServer.New`, which opened `sessions.db` and ran `ReconcileOnStartup` with a fresh daemon UUID — flipping the daemon's active jobs to `aborted` in persistence and causing CC agents to lose visibility of `mcp__aimux__*` tools mid-session. v4.6.0 routes shim invocations past `aimuxServer.New`, eliminating the shim-induced reconcile corruption.
- Shim startup no longer contends for SQLite WAL with the daemon (NFR-3). Prior multi-shim-startup restore timings of ~7.5s (vs v4.0.1's ~19ms) are gone.
- **Circuit breaker reset on refresh-warmup** (consolidated from v4.5.3 hotfix PR #120). `BreakerRegistry.ResetAll()` added; `refresh-warmup` handler now clears stuck-Open breakers so a prior quota-triggered `BreakerOpen` state recovers on the next probe. Response gains `breakers_reset` + `binary_only_fallback_applied` fields.
- **Classify `503 auth_unavailable` as `ModelUnavailable`** (consolidated from v4.5.3 hotfix PR #120). `modelUnavailablePatterns` in `pkg/executor/classify.go` now matches `auth_unavailable` and `no auth providers`. Previously the substring `authentication` matched `fatalPatterns` first, mis-routing these errors as Fatal and bypassing the suffix-strip fallback chain. Now correctly routes to `ErrorClassModelUnavailable` so `gpt-X-codex-spark → gpt-X-codex` fallback fires. Covered by `TestClassifyError_AuthUnavailableIsModelUnavailable` (3 cases).

### Codex

- **Default codex model bumped to `gpt-5.4`** (consolidated from v4.5.3 hotfix PR #120). OpenAI is phasing out the `gpt-5.3-codex` family (March-April 2026); coding capabilities are absorbed into `gpt-5.4`. Updated `config/cli.d/codex/profile.yaml` (`default_model`), `config/default.yaml` (role `coding`), `test/e2e/testdata/config/cli.d/codex/profile.yaml`, README role-routing examples, production-mirroring test fixtures, and the `cmd/testcli/codex` emulator default. Scenario tests and code comments illustrating the suffix-strip mechanism (`gpt-X-codex-spark → gpt-X-codex`) intentionally left on spark — the model name is a label documenting the behavior, not a production contract.

### Migration

No action required for operators who do not touch the documented env vars (`MCP_MUX_SESSION_ID`, `AIMUX_NO_ENGINE`). Expected observable changes:
- Faster `/mcp reconnect aimux` (from ~8s worst-case under contention → <1s consistent)
- Fewer spurious `status` reports of `aborted` on async jobs
- One additional log line per invocation (FR-8 mode/signal audit)

---

## [4.5.2] - 2026-04-21

### Fixed

- Non-blocking warmup: `driver.RunWarmup` moved to a background goroutine. Shim startup no longer blocks on CLI health probes; `/mcp reconnect aimux` returns within CC's 20s handshake window even under concurrent multi-session contention.
- Warmup fallback: when every CLI probe returns `passed=false` (common in spawned daemon env where PATH is not inherited), daemon now falls back to binary-only detection instead of hard-failing. Adds `log.Warn` line `"all CLI probes failed — falling back to binary-only detection (health-gate bypassed)"`.

### Added

- `cmd/ctl/main.go` — new `aimux-ctl` diagnostic binary. Speaks muxcore's `control.SendWithTimeout` protocol against the aimux daemon's control socket. Commands: `status`, `shutdown`, `graceful-restart`. Usage: `aimux-ctl -cmd graceful-restart -drain-ms 10000`.

---

## [4.5.1] - 2026-04-20

Patch release: **CR-1 (US1)** — reliable delegation, cooldown observability, secret scrubbing.

### Fixed

- `RunWithModelFallback` switch missing `default:` case: `ErrorClassUnknown` (exit=5) left `lastErr=nil`, producing `%!w(<nil>)` corruption in error messages. Now sets structured `"unknown error on {cli}:{model} (exit={N}): {excerpt}"` with redacted excerpt.
- `BuildModelChain` appended suffix-stripped model variants on every call regardless of error class. Now only appends when `errClass ∈ {ErrorClassQuota, ErrorClassModelUnavailable}`.

### Added

- `pkg/executor/redact` package: `RedactSecrets(string) string` scrubs API keys (OpenAI legacy/project/svcacct, Anthropic sk-ant-api, Google AIza, Bearer tokens, Authorization headers) before persistence. `PatternVersion = "2026-04-20"`.
- `sessions(action="cooldown_list")` — lists all active (non-expired) cooldown entries with `seconds_left` field.
- `sessions(action="cooldown_flush", cli, model)` — removes a specific cooldown entry immediately without daemon restart.
- `sessions(action="cooldown_set", cli, model, seconds)` — overrides the cooldown duration for a (cli, model) pair, effective on next `MarkCooledDown` call.
- `AIMUX_COOLDOWN_SECONDS` env var — global cooldown duration override for all CLIs.
- `AIMUX_COOLDOWN_OVERRIDES` env var — per-pair overrides (`cli:model:seconds` comma-separated).
- INFO log on every `MarkCooledDown` call: `{cli, model, duration, trigger_stderr}` (trigger_stderr redacted).

### Changed

- `MarkCooledDown` now accepts `triggerStderr string` as 4th argument (stored in `CooldownEntry.TriggerStderr`).
- `BuildModelChain` signature extended with `errClass ErrorClass` parameter.
- `pkg/session/sqlite.go` `SnapshotJob` and `SnapshotAll` redact `error_json` before SQLite write (`redact.RedactSecrets`).
- `loom/store.go` `SetResult` redacts `tasks.error` before SQLite write (inline patterns, loom is a standalone module).
- `buildFallbackCandidates` replaces side-effecting `Allow()` call with three read-only filter branches (FR-1.4):
  1. `BreakerOpen` state check (`reason=breaker_open`) — non-tripping, safe for observation-only callers.
  2. Rolling failure rate ≥95% over ≥10 requests via `metrics.FailureRate()` (`reason=failure_rate`).
  3. `CLIProfile.RequiresTTY && !conpty.Available()` on non-Windows platforms (`reason=no_tty`).
  Each skipped candidate emits an INFO log line. Primary CLI is never filtered.
- `metrics.Collector.FailureRate(cli, minRequests)` — new method; returns cumulative error rate when `reqs ≥ minRequests`, else 0.0 (fail-open; new CLIs never penalised without data).
- `CLIProfile.RequiresTTY bool` — new field (`yaml:"requires_tty,omitempty"`); set to `true` for TTY-dependent CLIs (aider, gptme, qwen).
- `conpty.Available()` — new package-level function using `sync.Once` to cache the ConPTY probe result for the process lifetime; avoids repeated `runtime.GOOS` checks in hot paths.

### Tests

- **T015b** `pkg/server/server_exec_fallback_test.go` — 5 unit tests covering all `buildFallbackCandidates` filter branches: no-role shortcut, healthy fallback included, breaker_open skip, failure_rate skip, no_tty skip (platform-conditional).
- **SC-9 regression** `test/e2e/regression_cross_cli_test.go` — `TestRegression_SC9_NilErrorWrap` dispatches a quota-like failing exec and asserts the recorded error message does not contain `%!w(` (the nil-wrap sentinel from the pre-fix code path).

---

## [4.5.0] - 2026-04-20

Minor release bundling four merged PRs: tools visibility (PR #111, resolves engram #136), agent cache hygiene (PR #112, resolves engram #139), session durability Phase 2+4 (PR #113, resolves engram #111), and muxcore v0.21.1 with F2 shim reconnect passthrough (PR #114).

Silent-failure classes closed:

- False `completed` status after aimux restart (Phase 2 PersistTransition wires `SnapshotJob` on every state change)
- Stale agent entries after source file deletion (registry stat-validates sources at read time and purges missing entries)
- Invisible tools after shim reconnect or daemon restart (`notifications/tools/list_changed` emitted on every project connect event)
- Accidental probe-agent dispatch (`agents(action=list)` tool description + response `hint` field steer callers to `find`/`run` instead of name-match)

### Changed

- Bumped `github.com/thebtf/mcp-mux/muxcore` from v0.21.0 → v0.21.1 (additive patch
  release with F2 shim-reconnect — engine attempts up to 3 token refreshes after
  shim disconnect before falling back to full-spawn). `rotlog` package NOT wired
  (aimux has its own logging).

### Added

- `sessions(action="health")` response now includes three F2 shim-reconnect
  counters from the aimux daemon: `shim_reconnect_refreshed`,
  `shim_reconnect_fallback_spawned`, `shim_reconnect_gave_up`. Counters are
  read via the muxcore control socket and gracefully degrade to absent when
  the socket is unreachable. **TEMPORARY implementation:** uses control-socket
  loopback in daemon mode too, pending upstream muxcore API for in-process
  access (tracked as engram mcp-mux#146). When `engine.MuxEngine.Status()`
  lands, aimux will branch on mode and use direct in-memory access from the
  daemon path.

- Session durability opt-out: `AIMUX_SESSION_STORE=memory` skips SQLite persistence
  entirely (tests and embedded use cases where durability is not required).
  Default (`sqlite` or unset) preserves v4.3.0 behavior (engram #111 Phase 4).

### Fixed

- Session durability Phase 2: every async job state transition
  (Created → Running → Completed/Failed/Aborted) now persists to SQLite immediately
  via `SnapshotJob`. Previously `StartJob` and `CancelJob` skipped the snapshot,
  leaving mid-transition states invisible to startup reconciliation. Resolves the
  final acceptance criterion of engram #111 Phase 1+3 remediation scope.
- Tools visibility: daemon now always emits `notifications/tools/list_changed` on
  project connect and reconnect, so Claude Code re-queries tools after shim
  reconnect, daemon restart, or binary upgrade (engram #136).
- Orchestration hygiene: `agents(action=list)` tool description now warns against
  name-match selection and steers callers to `action=find(prompt=...)` or
  `action=run` without agent (both return relevance-ranked candidates). Response
  body includes a `hint` field with the same guidance. Prevents experimental/probe
  agents (e.g., `codex-self-delegate`) from being accidentally dispatched to
  production tasks. Investigation: `.agent/investigations/codex-self-delegate-hallucination-2026-04-20.md`.
- Agent registry: deleted agent source files no longer appear in `agents(action=list/find/info)` results. Registry stat-validates agent sources at read time and purges stale entries from the in-memory map (engram #139).

### Changed

- `TECHNICAL_DEBT.md` moved from repo root to `.agent/TECHNICAL_DEBT.md` — aligns
  with the convention that all agent-managed artifacts live under `.agent/`.

## [4.6.0] - Unreleased

Minor release: **CR-2 (US2)** — honest persisted record, live progress on async path, post-hoc audit tool.

### Added

- `actual_cli TEXT DEFAULT NULL` column in `jobs` table; reads prefer `COALESCE(actual_cli, cli)`.
- `OnOutput func(cli, line string)` field on `loom.TaskRequest`; threaded through `subprocess_base.go` to executor.
- `sessions(action="audit_secrets", since=<rfc3339>)` — scans `jobs.error_json` and `tasks.error` rows through `redact.SecretPatterns`, returns `{total_rows_scanned, suspected_leaks, sample, pattern_version}`.
- `UpdateJobCLI(jobID, actualCLI string) error` on `JobManager`.

### Changed

- Async Loom jobs now populate `progress_lines` / `last_output_line` via `OnOutput` callback (previously sync-path only).

---

[4.6.1]: https://github.com/thebtf/aimux/compare/v4.6.0...v4.6.1
[4.5.2]: https://github.com/thebtf/aimux/compare/v4.5.1...v4.5.2
[4.5.1]: https://github.com/thebtf/aimux/compare/v4.5.0...v4.5.1
[4.5.0]: https://github.com/thebtf/aimux/compare/v4.4.0...v4.5.0

## [4.4.0] - 2026-04-19

Minor release: **hot-swap upgrade structural prep** (Phase 1 of engram #129).

Establishes the `pkg/upgrade.Coordinator` type, splits `pkg/updater` into composable Download/VerifyChecksum/Install, and rewires `handleUpgrade` to delegate through the Coordinator. Zero behavior change vs v4.3.0 — all upgrade modes still route to the deferred path (daemon restart when all CC sessions disconnect).

Phase 2-4 (active hot-swap via muxcore handoff) is deferred to v4.5.0+ pending upstream mcp-mux public export of `PerformHandoff`/`ReceiveHandoff` (tracked as engram cross-project issue #130).

### Added

- **`pkg/upgrade` package** — new. `Coordinator` type orchestrates upgrade lifecycle. `Mode` enum: `auto` | `hot_swap` | `deferred`. `Result` struct includes `HandoffTransferred`, `HandoffDurationMs`, `HandoffError` fields reserved for Phase 3.
- **`pkg/upgrade.SessionHandler` interface** — minimal `SetUpdatePending()` contract satisfied by `aimuxHandler`.
- **`updater.Download(ctx, currentVersion, targetPath) (*Release, error)`** — downloads and checksum-verifies to any path (not just current exe). Foundation for Phase 3 hot-swap where binary is staged to temp before install.
- **`updater.VerifyChecksum(binaryPath, release) error`** — post-download existence + metadata check hook.
- **`updater.Install(newBinaryPath, currentExePath) error`** — atomic cross-platform install via `go-selfupdate/update.Apply` (Windows-safe running-exe replacement).

### Changed

- `updater.ApplyUpdate` — now a thin wrapper calling Download → VerifyChecksum → Install. Uses `os.CreateTemp` for staging (collision-safe).
- `pkg/server/server.go:handleUpgrade("apply")` — delegates to `upgrade.Coordinator.Apply(ctx, ModeAuto)`. Response envelope adds `hot_swap` branch (unreachable in v4.4.0, activated in v4.5.0). `deferred` response preserves v4.3.0 wire format: `status: "updated"`.

### Deferred to v4.5.0+

- Phase 2 — successor daemon mode (`--handoff-from` / `--handoff-token` flags in `cmd/aimux/main.go`).
- Phase 3 — predecessor `tryHotSwap` flow via `muxcore.daemon.PerformHandoff`.
- Phase 4 — cross-platform parity, structured logging, integration tests.

**Blocker:** muxcore handoff entry points are package-private (`performHandoff`, `receiveHandoff`, token helpers). aimux cannot integrate from outside the `muxcore/daemon` package. Engram cross-project issue #130 requests public export in muxcore v0.21.0+.

## [4.3.0] - 2026-04-19

Minor release consolidating three engram-driven features: session durability (Phase 1+3),
codex fallback observability (Phase 0), and status()/UI activity signals. Also bumps
muxcore dependency to v0.20.4 for FR-28 token handshake + FR-29 socket 0600 hardening.

### Added

#### status() visibility (#116, PR #105)
- **`progress_tail` field on `status()`** — last non-empty line of the job's
  accumulated progress buffer, UTF-8-safe truncated to ≤100 bytes. Gives
  Claude Code UI and debugging operators a compact real-time activity signal
  without pulling the full progress buffer. Empty string when no output yet.
- **`progress_lines` field on `status()`** — total newline count in the
  accumulated progress buffer (monotonically increasing). Lets callers detect
  that work is advancing even when `progress_tail` text stays the same.
- Both fields added to `budget.FieldWhitelist["status"]` (non-breaking addition).
- `pkg/session.Job.LastOutputLine` and `Job.ProgressLines` — maintained O(1) on
  every `AppendProgress` call; no buffer scan on status poll.
- `pkg/util.TruncateUTF8` — shared UTF-8-safe byte-budget truncation helper.

#### Fallback observability (#115 Phase 0, PR #106)
- **Structured per-attempt log** and **`aimux_fallback_attempts_total{cli,model,result}`** counter in every `RunWithModelFallback` call (pkg/executor/fallback.go, pkg/metrics/fallback_metrics.go).
- **Opt-out via `AIMUX_FALLBACK_VERBOSE=false`** — counter always increments; logs suppressed.
- Prerequisite for Phase 2 codex account×model routing fix (deferred pending ≥1w telemetry data).
- Optimizations (Gemini review): atomic.Bool verbose-flag cache (zero syscall on hot path), atomic.Int64 O(1) `Total()`.

#### Session durability Phase 1+3 (#111, PR #107)
- **Schema v1→v2**: `sessions.daemon_uuid`, `sessions.aborted_at`, `jobs.daemon_uuid`, `jobs.last_seen_at`, `jobs.aborted_at` (all nullable, additive migration).
- **Schema v2→v3**: `sessions.aborted_job_ids TEXT` (JSON array).
- **`pkg/session/daemon.go`** — `GetDaemonUUID()` via crypto/rand sync.Once (32-char hex, in-memory per process).
- **`pkg/session/reconcile.go`** — `ReconcileOnStartup(ctx, db, currentUUID)` scans for orphaned jobs/sessions (different daemon UUID or NULL UUID), marks running orphans as aborted in a single atomic transaction, rolls up session-level `aborted_job_ids` and `status=aborted` when all child jobs aborted.
- **`sessions(action=list, status=aborted)` filter** — new MCP tool filter (pkg/server/server_session.go).
- **`types.JobStatusAborted`** and **`types.SessionStatusAborted`** enum constants.
- **`BenchmarkReconcile10k`** — 10k orphaned jobs reconcile in 116ms (NFR-1 requires < 5s).
- **Deferred to v4.4.0**: Phase 2 (PersistTransition on every state change), Phase 4 (`AIMUX_SESSION_STORE=memory` opt-out + tool descriptions).

### Changed

- **muxcore v0.20.2 → v0.20.4** (#113, PR #104): drop-in dependency bump for FR-28 token handshake enforcement + FR-29 socket 0600 permissions hardening. Zero code changes required on aimux side.

## [4.2.0] - 2026-04-19

Minor release: **response-budget-policy**. Default MCP tool response bodies are
bounded to ~4 KiB so multi-step orchestrators do not blow their MCP context on
large listings or job transcripts. Shipped across four PRs:
#99 (budget package foundation), #100 (sync tools),
#101 (dual-source sessions + agents info), #102 (investigate + orchestrate +
descriptions + NFR-1 suite).

Contains one **BREAKING** change to `sessions(action=list)` response shape
(FR-11 intentional break). See Migration notes below.

### Added

- **`pkg/server/budget/`** package — pagination helpers, field whitelists,
  content-bearing field guards, truncation envelope, dual-source pagination.
- **`include_content=true`** parameter on content-bearing tools — opts out of brief
  mode and returns the full payload (job output, agent system prompt, investigation
  report, orchestrator transcript).
- **`tail=N`** parameter on `status` — returns the last N chars of job output
  without pulling the full content.
- **`sessions_limit` / `sessions_offset` / `loom_limit` / `loom_offset`** — independent
  pagination cursors for the two sources surfaced by `sessions(action=list)`. Legacy
  `limit` / `offset` still work and apply to both sources as a fallback.
- **`content_length`** field on every brief response that omits content — byte count
  of what was withheld, so callers can decide whether to fetch full content.
- **NFR-1 per-tool budget test suite** — table-driven test asserts every non-exempt
  tool's default brief response ≤ 4096 bytes on realistic fixtures.

### Changed

- **BREAKING — `sessions(action=list)` response shape** (FR-11 intentional break):
  Loom tasks are now returned under a dedicated top-level `loom_tasks` key instead of
  being folded into the same list as legacy sessions. The response now has
  `{sessions, loom_tasks, sessions_pagination, loom_pagination}` with independent
  pagination per source. Callers that previously iterated a single flat list of rows
  MUST update to read both `sessions[]` and `loom_tasks[]`. Legacy `limit` still
  works (caps both sources equally); use `sessions_limit` / `loom_limit` for
  asymmetric caps.
- **`agents(action=info)` default response** — large `Content` field (system prompt,
  can be 500 KB+) is no longer returned by default. Use `include_content=true` to
  retrieve it.
- **`sessions(action=info)` per-job rows** — `content` field no longer returned by
  default; `content_length` reports the byte count. Use `include_content=true` to
  retrieve full content.
- **`investigate(action=list)` response shape** — brief rows with
  `session_id, topic, domain, status, finding_count`, paginated via `limit`/`offset`.
  The former `active_count`/`saved_reports`/`saved_count` keys are removed.
- **`investigate(action=status)` response shape** — brief fields
  `session_id, topic, domain, status, finding_count, coverage_progress`.
  The former `iteration`/`findings_count`/`corrections_count`/`coverage_unchecked`/`last_activity`
  keys are removed. `coverage_progress` is a 0..1 ratio of checked vs. total coverage areas.
- **`investigate(action=recall)` response shape** — brief fields
  `found, session_id, topic, date, finding_count, content_length` (`session_id` here
  is the saved report filename, not an in-memory investigation ID — kept stable
  across server restarts). `recall` omits the full report by default; use
  `include_content=true` to retrieve it.
- **All 14 tool descriptions** — now document the brief/full contract and surface
  the relevant budget knobs. `deepresearch` is explicitly flagged as exempt from the
  4k default budget.

### Migration notes

If your orchestrator reads `sessions(action=list)` and iterates results:

```diff
- for row in response.result["sessions"]:
-     ... # previously included loom tasks
+ for row in response.result["sessions"]:
+     ... # legacy session rows only
+ for row in response.result["loom_tasks"]:
+     ... # loom task rows
```

If you previously relied on full content in sessions/agents/investigate briefs,
pass `include_content=true` explicitly. If you need partial output for long jobs,
use `tail=N` on `status`.

## [4.1.1] - 2026-04-18

Patch release: muxcore dependency bump to v0.20.2. Drop-in upgrade — no API changes,
no consumer code modifications.

### Fixed

- **muxcore v0.20.0 → v0.20.2** — ships two upstream patch releases back-to-back:
  - **v0.20.1** (PR thebtf/mcp-mux#66, #67) — 10 concurrency/race fixes observed in
    aimux usage: shared-owner dedup race in `findSharedOwner`, upstream
    `Wait`-vs-`ReadLine` race, counter bugs in owner lifecycle.
  - **v0.20.2** — **critical** supervisor restart-loop storm fix. `owner.Serve()` used
    to return `nil` on a closed `o.done` channel, which `suture` interpreted as a clean
    exit AND scheduled a restart in the same tick — resulting in flapping or
    permanently-dead MCP servers in live Claude Code sessions after
    `mcp-mux upgrade --restart`. v0.20.2 returns `suture.ErrDoNotRestart` on
    intentional shutdown, closing the flap.

### Internal

- `go.mod`: `github.com/thebtf/mcp-mux/muxcore v0.20.0 → v0.20.2`
- `go.sum` refreshed via `go mod tidy`. Full test suite (857 tests, incl. 31 e2e) green.

## [4.1.0] - 2026-04-18

Minor release: aimux internal prompts/descriptions audit + routing health gate + CLI warmup probe.

Ships all 38 tasks + 8 gates of the `aimux-internal-descriptions-audit` spec across 3 PRs:
PR #94 (Phase 1 runtime strings), PR #95 (Phases 2-5 agent struct + skill map + role prompts + CLI profiles),
PR #96 (Phase 7 routing + warmup).

### Added

- **`sessions(action="refresh-warmup")` tool action** (#96) — runtime refresh of CLI warmup state.
  Returns `{refreshed, available, excluded}` or `{refreshed:false, reason}` when opt-out is active.
- **`Agent.When` field** (#95) — struct field with JSON/YAML `omitempty` tag. Populated for all
  5 builtin agents (researcher, reviewer, debugger, implementer, generic). Surfaced in
  `agents(action="find")` response so orchestrators can pick agents based on when-to-use guidance.
- **`Config.Server.CLIPriority []string`** (#96) — operator-ordered CLI priority list in
  `config/default.yaml` (`cli_priority: [codex, claude, gemini, qwen, ...]`). Replaces implicit
  alphabetical ordering. CLIs absent from the list are appended after in stable load order.
- **`Config.Server.WarmupEnabled bool` + `WarmupTimeoutSeconds int`** (#96) — global warmup
  config. Per-profile `warmup_timeout_seconds` + `warmup_probe_prompt` overrides on `Profile`.
- **`pkg/driver/warmup.go`** (#96) — `driver.RunWarmup(ctx, reg, cfg)`. Structured JSON probe
  (`reply with JSON: {"ok": true}`). Per-CLI defaults: codex/claude=8s, gemini/qwen=10s,
  aider/continue=20s, droid/cline/crush=15s. `AIMUX_WARMUP=false` env opt-out.
- **`Router.KnownRoles() []string`** (#96) — returns the full set of understood role
  names: configured defaults plus every capability declared by enabled CLI profiles.

### Changed

- **Routing fail-fast on unknown role** (#96) — `handleExec` rejects unknown roles with a
  validation error before any CLI is spawned. Previously silently fell back to alphabetical-first
  enabled CLI (caused `exec(role="reviewer")` typos to hit `aider`).
- **CLI priority is explicit, not alphabetical** (#96) — `Router.Resolve` uses `cli_priority`
  config for tiebreaks. `enabledCLIsSorted` kept only for test determinism with explicit comment;
  NOT used in production routing paths.
- **`generic` builtin agent routes via `analyze`** (#95) — previously routed via `coding` which
  triggered expensive codex CLI for "follow instructions literally" tasks.
- **`testgen` role is IMPLEMENTER, not ADVISOR** (#95) — resolves contradictory identity.
- **ADVISOR roles have handoff paragraph** (#95) — refactor/planner/codereview/analyze/debug
  now include `exec(role="coding")` handoff guidance in Output Format.
- **Tool description expansions** (#94) — `status`, `exec`, `agent`, `agents`, `sessions`,
  `deepresearch`, `workflow` descriptions expanded with state machines, async contracts,
  poll-wrapper-subagent references, pagination notes.
- **Stall guidance includes pre-filled `cancel_command`** (#94) — `sessions/status` response
  contains literal `sessions(action="cancel", job_id="<jobID>")` string for LLMs to use directly.

### Fixed

- **F7.1 silent misrouting** (#96) — unknown role names no longer silently fall back to alphabetical-first CLI.
- **F7.4 implicit alphabetical CLI priority** (#96) — replaced with explicit `cli_priority` config.
- **Warmup re-enables recovered CLIs** (#97) — `RunWarmup` now iterates every CLI whose
  binary resolved (`Registry.ProbeableCLIs()`) and sets availability explicitly from the
  probe outcome, so a passing probe restores a CLI that an earlier warmup marked
  unavailable. `sessions(action="refresh-warmup")` inherits this recovery behavior.
- **`Router.KnownRoles()` reports capability roles** (#97) — capability-routed role names
  (e.g. roles supported by a CLI profile but absent from `roles:` defaults) now appear
  in `KnownRoles()` output instead of only defaults-configured names.
- **CLI binary validated before routing accepts it** (#96) — warmup probe (F7.5) verifies
  `reply with JSON: {"ok": true}` so a resolvable binary alone is not enough to be routed to.
- **CLI profile documentation** (#95) — codex `account_gating` + version note, gemini
  intentionality comment on omitted `model_fallback`, continue hub-slug format warning elevated.
- **`config/p26/classification.v1.json`** (#96) — added `sessions/refresh-warmup` action entry.

### Internal

- **New `pkg/driver/warmup.go`** + `pkg/driver/warmup_test.go` with 17+ tests covering AllSucceed,
  OneFails, OneTimesOut, OptOut, ConfigDisabled, JSONParse (table-driven), DaemonWarmupExcludes.
- **`pkg/routing/routing_test.go`** new tests: `TestResolve_UnknownRole_ReturnsError`,
  `TestResolve_KnownRole_UsesPriorityOrder`.
- **`pkg/server/server_session_test.go`** new test: `TestServerSession_RefreshWarmup`.
- **`pkg/server/handler_test.go`** new test: `TestExec_UnknownRoleReturnsValidationError`.
- **Structured JSON probe parsing** — `json.NewDecoder` replaces manual brace-counting;
  correctly handles brace characters inside JSON string literals.
- **`Registry.AllCLIs()`** — deterministically sorted slice.
- **`Registry.ProbeableCLIs()`** (#97) — sorted list of CLIs with resolved binaries; used
  by warmup so previously-unavailable CLIs can be retried and re-enabled.

### Compatibility

- **Breaking-ish behavior change**: `exec(role="<unknown>")` returns a validation error instead
  of silently routing. Callers that relied on the silent fallback must use a valid role name
  (see `routing.AdvisoryRoles` + `config/default.yaml` `roles:`).
- **No API breakage**: all exported APIs remain source-compatible. `Config` struct gains new
  fields that default to sensible values via `config.Load()`.
- **Warmup probe is opt-out**: set `AIMUX_WARMUP=false` env to skip warmup (binary-only detection).
  Also `warmup_enabled: false` in `config/default.yaml`.

[4.1.1]: https://github.com/thebtf/aimux/compare/v4.1.0...v4.1.1
[4.1.0]: https://github.com/thebtf/aimux/compare/v4.0.3...v4.1.0

## [4.0.3] - 2026-04-18

Patch release: model-level fallback for inaccessible models.

### Added

- **`executor.ErrorClassModelUnavailable`** (#92) — new error classification
  distinguishes model-level access failures from CLI-level auth failures. When
  a model is inaccessible to the caller (e.g. `gpt-5.3-codex-spark` for a
  ChatGPT account without spark access), aimux now falls through to the next
  model in the `model_fallback` chain on the same CLI instead of skipping the
  CLI entirely. Addresses the "model not found" Fatal misclassification that
  caused instant failures for ChatGPT-account delegation.
- **Sentinel errors `executor.ErrQuotaExhausted` and `executor.ErrModelUnavailable`**
  (#92) — exported sentinel errors for reliable detection via `errors.Is`,
  replacing fragile `strings.Contains` checks on error message text.

### Fixed

- **13 new ModelUnavailable patterns** in `pkg/executor/classify.go`:
  `model not found`, `not available for your account`, `not authorized for model`,
  `not authorized for this model`, `model not enabled`, `access denied to model`,
  `access denied to this model`, `model not available`, `this model is not available`,
  `you do not have access to model`, `you do not have access to this model`,
  `you don't have access to model`, `you don't have access to this model`.
- **Priority reordering**: `ErrorClass` iota now matches `ClassifyError` priority
  (Quota → ModelUnavailable → Transient → Fatal → Unknown). Previous iota order
  had ModelUnavailable appended at the end, which was internally inconsistent
  with the switch check order.
- **Regression protection**: bare `"unauthorized"` (without `for model` qualifier)
  and bare `"access denied"` (without `to model` qualifier) remain classified as
  Fatal — credential/permission problems, not model-level.

### Internal

- 17 new unit tests across `pkg/executor/classify_test.go` and the new
  `pkg/executor/fallback_test.go`, covering every new pattern, priority edge
  cases, cross-field collisions, empty inputs, uppercase variants, and the
  transient-retry → ModelUnavailable nested path.
- 2 new integration tests in `pkg/server/model_fallback_test.go`:
  `TestModelFallback_ModelUnavailableThenSuccess` and
  `TestModelFallback_AllModelsUnavailable_ReturnsRateLimitError`.
- Spec: `.agent/specs/model-fallback-chain/spec.md` Amendment 2026-04-18 (FR-7/8/9).
- Added `.claude/` to `.gitignore` (local Claude Code session state should not be
  committed).

### Compatibility

- No API changes. `ErrorClass` enum is internal.
- Downstream callers that check error strings for `"rate limit"` continue to work —
  the outer error wrapper preserves that substring so CLI-fallback routing
  behaves identically.
- `ErrQuotaExhausted` / `ErrModelUnavailable` are newly exported but additive.

[4.0.3]: https://github.com/thebtf/aimux/compare/v4.0.2...v4.0.3

## [4.0.2] - 2026-04-18

Patch release: muxcore dep bump to v0.20.0 and aimux internal skill prompt fixes.

### Changed

- **Bumped `github.com/thebtf/mcp-mux/muxcore` v0.19.4 → v0.20.0** (#90) — upstream
  improvements: zombie-listener cleanup, async SendNotification, keepalive removal.
  The v0.20.0 upstream API break is in `upstream.Start`, which aimux does not consume
  (we use `SessionHandler` per CLAUDE.md architecture). Zero code-side changes in
  aimux; only `go.mod` + `go.sum` updated. Closes engram issue aimux#84.

### Fixed

- **Skill prompt fix: `think(pattern="critic")` → `critical_thinking`** (#89) — four
  occurrences across `config/skills.d/guide.md` and `config/skills.d/consensus.md`
  referenced an invalid think-pattern enum value. Any orchestrator following those
  skills verbatim got a runtime tool-validation error. The canonical pattern name is
  `critical_thinking` (per `pkg/think/patterns/`).
- **Skill prompt fix: `workflow` skill uses poll-wrapper fragment** (#89) —
  `config/skills.d/workflow.md` previously instructed orchestrators to poll
  `mcp__aimux__status` directly, contradicting the mandatory `poll-wrapper-subagent`
  pattern enforced in `background`, `delegate`, and `agent-exec` skills. Replaced
  with `{{template "poll-wrapper-subagent" .}}` include and registered the fragment
  in `config/skills.d/_map.yaml`'s `workflow:` entry.
- **Skill prompt fix: `consensus.md` uses `issue=` not `artifact=`** (#89, post-review
  follow-up) — the `critical_thinking` validator requires the `issue` field; the
  previous `artifact=` would fail at runtime.
- **Skill registry consistency** (#89, post-review follow-up) — added `agent` tool
  to `workflow.tools` in `_map.yaml` and registered `poll-wrapper-subagent` in the
  shared `fragments:` section with `used_by: [workflow]`.

### Internal

- Marked 11 completed `loom-library` tasks as `[x]` in
  `.agent/specs/loom-library/tasks.md` (P001-P003, T035, G005-G007, T047-T050). All
  shipped as part of `loom/v0.1.0` and `loom/v0.1.1` releases; tracker was stale.
- Produced internal audit report `.agent/reports/aimux-prompts-audit-2026-04-18.md`
  with 18 findings across 6 categories of internal prompts, CLI profiles, role
  prompts, MCP skill prompts, agent registry, and runtime guidance strings. Three
  findings are fixed in this release (A1: critic pattern enum, A2: workflow
  poll-wrapper, A3b: consensus `issue=` field + registry consistency — all via
  #89); the remaining 15 are tracked as follow-ups.

## [4.0.1] - 2026-04-17

Patch release fixing a version-constant split missed in v4.0.0. Introduces a single
source of truth for the aimux version string.

### Fixed

- **Version mismatch between log lines and MCP handshake** — `cmd/aimux/main.go` had
  a separate `const version = "3.0.0-dev"` that was not bumped when
  `pkg/server.serverVersion` was updated to `"4.0.0"` during the v4.0.0 release.
  Result: binaries compiled from the v4.0.0 commit responded with
  `serverInfo.version: "4.0.0"` in the MCP handshake (from `server.serverVersion`)
  but logged `aimux v3.0.0-dev starting` on startup (from `main.version`).
  Observable via `~/.config/aimux/aimux.log` tail. Diagnosed during a production
  delegation hang investigation where the bogus log version gave the false
  impression that an old binary was running.

### Changed

- **Single source of truth for version** — `pkg/server.serverVersion` renamed to
  exported `pkg/server.Version` (`pkg/server/server.go:42`). `cmd/aimux/main.go`
  removes its own `const version` and references `aimuxServer.Version` directly.
  `pkg/server/mux_compat_test.go` fixture now reads `server.Version` instead of
  a hardcoded string. All 11 prior `serverVersion` call sites (server.go: 6,
  server_transport.go: 4, mux_compat_test.go: 1) and all 6 `version` log sites
  in main.go now resolve to the same constant. Future releases touch exactly
  one line (`pkg/server/server.go:42`) and both handshake + logs + test fixture
  update atomically.
- **`Version` bumped to `"4.0.1"`** for this patch release.

### Verification

- `go build ./...` PASS, `go vet ./...` PASS.
- `go test ./... -timeout 300s` PASS (27 packages + 137s e2e suite).
- Smoke test: freshly built binary responds with `"version":"4.0.1"` in MCP
  initialize handshake AND logs `aimux v4.0.1 ready` / `MCP server starting on
  stdio (aimux v4.0.1)` in `~/.config/aimux/aimux.log` — confirming SSOT bound
  all three surfaces.

[4.0.1]: https://github.com/thebtf/aimux/compare/v4.0.0...v4.0.1

## [4.0.0] - 2026-04-16

Major release. Establishes `loom` as a standalone Go module and closes the 2026-04-15
production readiness security audit. Renames the nested module boundary, introduces a
CLI-profile-driven environment allowlist, and hardens the multi-user SSE/HTTP transport.

### Added

- **`loom` nested Go module** (`github.com/thebtf/aimux/loom`) — central task mediator extracted from aimux with its own semver tag prefix (`loom/vX.Y.Z`). Exposes `LoomEngine.Submit/Get/List/Cancel/RecoverCrashed`, callback-based `EventBus`, `TaskEvent` with 6 fields (Type, TaskID, ProjectID, RequestID, Status, Timestamp), 8 `EventType` values, and a three-tier worker model (`Worker` interface → `SubprocessBase`/`HTTPBase`/`StreamingBase` → concrete adapters). SQLite persistence with WAL crash recovery. See `loom/README.md`, `loom/CONTRACT.md`, `loom/PLAYBOOK.md`, `loom/TESTING.md`, `loom/RECOVERY.md`. Four runnable examples under `loom/examples/`. (#74, #76, #77, #78, #79, #80, #81)
- **`loomlint` CI boundary tool** — static check that forbids aimux-internal imports inside the `loom/` subtree, enforcing the module contract at PR time. (#74)
- **OpenTelemetry Meter integration** in loom — 8 instruments (`loom.tasks.submitted`, `loom.tasks.completed`, `loom.tasks.failed`, `loom.tasks.cancelled`, `loom.gate.pass`, `loom.gate.fail`, `loom.submit.duration_ms`, `loom.task.duration_ms`) with `worker_type` + `project_id` attributes. Noop meter by default, zero-cost when unused. (#80)
- **Canonical 8-field structured logging** — `module`, `task_id`, `project_id`, `worker_type`, `task_status`, `duration_ms`, `error_code`, `request_id` — wired through `deps.Logger` injection. (#80)
- **NFR-10 p99 submit-duration bounds test** — verifies `loom.Submit` completes in ≤100ms at p99 under a 100-task burst. Measured: 1.005ms (≈99× margin). (#80)
- **`resolve.BuildEnv`** — new helper that builds a spawned CLI's environment from an OS-essential baseline plus a per-profile `env_passthrough` allowlist plus session overrides. Any parent env var not in the baseline or allowlist is dropped. (#85, SEC-HIGH-1)
- **`CLIProfile.EnvPassthrough` field** — YAML `env_passthrough:` list declaring which parent env vars a given CLI may inherit. All 12 bundled profiles (codex, gemini, claude, qwen, aider, goose, crush, gptme, cline, continue, droid, opencode) ship with explicit allowlists matching their documented API key requirements. (#85, SEC-HIGH-1)
- **`SpawnArgs.EnvList`** — pre-built complete env list bypassing executor-side merging. When set, the executor uses it verbatim instead of calling `os.Environ()`. (#85, SEC-HIGH-1)
- **`server.FilterSensitive`** — filters env vars whose name ends in `_API_KEY`, `_TOKEN`, `_SECRET`, `_PASSWORD`, or `_KEY` from persisted `loom.task.Env`. Exception: `SSH_AUTH_SOCK`. In-memory spawn env is unchanged; only the SQLite-persisted copy is sanitised. (#85, SEC-MED-3)
- **`validateCWD`** — strict cwd validator: non-empty, absolute path, no NUL/newline/CR characters, must exist, must be a directory. Used in `handleAudit` and propagated through investigate handlers. (#85, SEC-HIGH-3)
- **`AIMUX_ENGINE_NAME`** — environment variable override for the muxcore engine instance name, enabling `aimux-dev` vs `aimux` isolation when running development and production binaries on the same host. (#71)
- **`govulncheck` CI workflow** (`.github/workflows/security.yml`) — soft-fail `govulncheck ./...` on every push/PR to establish a vulnerability baseline. (#85)
- **`.agent/TECHNICAL_DEBT.md`** — formal deferral register, seeded with the `pkg/ratelimit` wiring deferral. (#85, SEC-MED-1; moved to `.agent/` in v4.5.0)

### Changed

- **`CancelAllForProject` replaces per-task `Cancel` for bulk project shutdown** in loom — new dedicated API, snapshot-iterate-signal pattern, emits `EventTaskCancelled` per task. (#78)
- **`EventBus.Subscribe` is now callback-based** (`Subscribe(handler func(TaskEvent)) (unsubscribe func())`) instead of returning a channel. Synchronous fan-out with panic recovery. Zero aimux-side callers existed at migration time, so no downstream migration needed. (#78)
- **`TaskEvent` struct** replaces the legacy `Event{Type, TaskID, Data}` with a typed 6-field shape `{Type, TaskID, ProjectID, RequestID, Status, Timestamp}`. (#78)
- **`EventType` enum** replaces 7 legacy values (`created`, `dispatched`, `progress`, `gate.pass`, `gate.fail`, `completed`, `failed`) with 8 canonical values (`Created`, `Dispatched`, `Running`, `Completed`, `Failed`, `FailedCrash`, `Retrying`, `Cancelled`). `progress` / `gate.*` become internal `deps.Logger` calls, not public events. (#78)
- **`CLIWorker`** rewritten as a 22-LOC adapter around `workers.SubprocessBase`, down from 91 LOC of bespoke executor glue. (#79)
- **`bearerAuthMiddleware` signature** now takes a `*logger.Logger` parameter and emits `WARN` on both mismatched and missing `Authorization` headers. Call sites in `ServeHTTP` and `ServeSSE` updated. (#85, SEC-LOW-1)
- **`AIMUX_AUTH_TOKEN` environment variable now takes precedence over `cfg.Server.AuthToken`** (YAML). When YAML supplies a token, a startup `WARN` is emitted directing the operator to move the secret to the env var. (#85, SEC-MED-2)
- **`handleAudit` rejects empty `cwd`** and quotes the cwd in its prompt template (`%q` instead of `%s`) to neutralise path-injection and prompt-poisoning via caller-supplied paths. (#85, SEC-HIGH-3)
- **`workers.StreamingBase.Logger` field type** changed from `func(string)` to `deps.Logger` for DI consistency. (#79)
- **loom `SubprocessBase.Run` timeout handling** — relies on Go stdlib `context.WithTimeout` min-deadline semantics; explicit `hasDeadline` guard removed. (#83, BUG-004)
- **loom retry path** returns on every `UpdateStatus` / `IncrementRetries` error instead of swallowing with `log.Printf`. Tasks no longer get stuck in `retrying` on persistence errors. (#83, BUG-002)
- **`LoomEngine.Close(ctx)`** drains in-flight dispatch goroutines via a `sync.WaitGroup` and emits `ErrEngineClosed` from post-close `Submit`s. Race-safe via mutex-guarded `closed.Load()` + `wg.Add(1)` pairing. (#83, BUG-001)
- **`loom.RequestIDKey`** exported as a TYPE (`type RequestIDKey struct{}`) rather than a var of an unexported struct. Callers must use `ctx.Value(loom.RequestIDKey{})`. (#83, CR-HIGH-3)
- **14× `log.Printf` error sites in `loom.go`** replaced with `l.logger.ErrorContext` using canonical 8-field format. (#83, CR-HIGH-2)
- **Task completed log** now includes `duration_ms`. (#83, CR-MED-1)
- **`serverVersion` constant** updated from stale `"3.0.0-dev"` to `"4.0.0"` — reflects actual release version in MCP server handshake.
- **`github.com/thebtf/mcp-mux/muxcore`** bumped from `v0.19.0` to `v0.19.4`. Picks up four upstream fixes: v0.19.1 hardcode cleanup, v0.19.2 `daemon.Spawn` stuck-placeholder fix, v0.19.3 concurrency correctness (7 post-audit findings — owner death result, notifier lock release, shared-mode multi-session), and v0.19.4 `runProxy` fallthrough fix that unblocks aimux being wrapped by an external `mcp-mux` shim (production regression where `mcp-mux D:/Dev/aimux/aimux.exe` from Claude Code produced an immediate "failed" badge). No aimux code changes required — `cmd/aimux/main.go` already follows the "both Handler and SessionHandler set" contract per v0.19.4 release notes.

### Fixed

- **BUG-003: empty pipe session ID** — `pipeSession.id` field was declared but never assigned; every pipe session returned `""` from `.ID()`, causing session-keyed registry collisions. Fixed by assigning `uuid.NewString()` in the session constructor. (#84)
- **Executor `Handle.alive` race** — `atomic.Bool` happens-before ordering fix for `TestProcessManager_IsAliveReturnsFalse` flakiness under `-race`. (#73)
- **Orchestrator strategy params** now packed under `Metadata.extra` for the `OrchestratorWorker` — resolves crashes in `consensus`, `debate`, `audit`, `workflow`, and `pair_coding` flows. Includes e2e polling migration for async loom responses. (#72)
- **`status` MCP tool** now has a Loom fallback path for task lookup, and `.mcp.json` is no longer tracked in git. (#70)
- **NEW-001: `retrying → failed` state transition** — the BUG-002 retry fix exposed a state machine violation where `failTask(retrying)` called `UpdateStatus(retrying → failed)` but `validTransitions[retrying]` only listed `{dispatched}`. Added `failed` to the valid transitions with a deterministic regression test. (#83)

### Security

- **SEC-HIGH-1 cross-CLI env leakage** — executors no longer merge `os.Environ()` into the spawned CLI's environment. Each CLI now sees only the OS baseline plus its profile-declared `env_passthrough` allowlist plus per-session injected values. Prevents `ANTHROPIC_API_KEY` leaking to `gemini`, `GEMINI_API_KEY` leaking to `codex`, etc. (#85)
- **SEC-HIGH-2 workflow template injection via step output** — `WorkflowStrategy.interpolate` now escapes `{{` in both `sr.Content` and `sr.Status` before substitution, not only in user `input`. A compromised CLI returning `{{other_step.content}}` can no longer poison downstream steps in the same workflow. (#85)
- **SEC-HIGH-3 cwd-based path injection in audit** — `handleAudit` now calls `validateCWD` (absolute path, existing directory, no control characters) before forwarding the cwd to the LLM or spawning the audit subprocess. Unquoted `%s` replaced with `%q`. (#85)
- **SEC-MED-2 auth token precedence flip** — `AIMUX_AUTH_TOKEN` env var now takes precedence over `cfg.Server.AuthToken` YAML, matching the documented convention. Startup `WARN` emitted when YAML supplies a token. (#85)
- **SEC-MED-3 API-key persistence** — `FilterSensitive` strips keys ending in `_API_KEY`, `_TOKEN`, `_SECRET`, `_PASSWORD`, `_KEY` from `loom.task.Env` before SQLite persistence. Crash recovery does not resurrect secrets to disk; clients re-inject on reconnect. (#85)
- **SEC-LOW-1 auth failure logging** — `bearerAuthMiddleware` emits `WARN` on both 401 paths (mismatched token, missing Authorization header) with remote address and path, replacing the previous silent rejection. (#85)
- **SEC-HIGH S2-001 shell concat in test helper** — loom `platformEcho` test helper uses positional `sh -c "echo \"$1\"" -- text` instead of unsafe string concatenation. (#83)

### Removed

- **`pkg/loom/` has moved to the nested module** — the old in-tree path is gone; all imports now reference `github.com/thebtf/aimux/loom`. The outer module's `go.mod` consumes the tagged `loom/v0.1.1` release directly (no `replace` directive). (#82, #84)
- **Legacy `Event` struct and channel-based `EventBus.Subscribe`** — replaced by `TaskEvent` and callback subscription respectively. No backward compatibility shim. (#78)
- **`progress`, `gate.pass`, `gate.fail` public `EventType` values** — demoted to internal `deps.Logger` calls in the quality gate path; no longer emitted as public events. (#78)

### Deprecated

- **`SpawnArgs.Env` (legacy map path)** — still honoured by all three executors for backward compatibility with existing tests, but new spawn code should use `SpawnArgs.EnvList` built via `resolve.BuildEnv`. Planned removal in a future major version once all call sites have migrated.

### Loom companion releases

- `loom/v0.1.0` — initial extraction (Phases 0-6). Tag: 2026-04-15. All 14 FRs + 9 NFRs + 10 user stories + 18 edge cases implemented. T050 external smoke test PASS.
- `loom/v0.1.1` — post-ship polish (9 findings + 2 second-opinion PRC blockers). Includes `Close(ctx)` drain, `ErrEngineClosed`, retry path repair, state-machine assertion, DI hygiene fixes. Tag: 2026-04-15.

[4.0.0]: https://github.com/thebtf/aimux/compare/v3.10.0...v4.0.0

## [3.10.0] - 2026-04-15

### Added

- Added LoomEngine v3 as central task mediator for all CLI dispatch (#69)

### Changed

- Upgraded muxcore v0.18.1 → v0.19.0 for full environment variable passthrough

[3.10.0]: https://github.com/thebtf/aimux/compare/v3.9.0...v3.10.0

## [3.9.0] - 2026-04-14

### Added

- Added muxcore engine integration for daemon mode — single binary, IPC-based multi-session (#66)
- Added SessionHandler integration for direct JSON-RPC dispatch, per-project sessions, and agent scoping (#67)
- Added Phase 2 rate limiter archival, per-session metrics, and notifier broadcast (#68)

### Changed

- Upgraded muxcore v0.17.1 → v0.18.0 (dependency update)
- Upgraded muxcore v0.18.0 → v0.18.1 for stale socket cleanup

### Fixed

- Fixed engine test regressions introduced by engine integration

[3.9.0]: https://github.com/thebtf/aimux/compare/v3.7.1...v3.9.0

<!-- v3.8.x intentionally skipped: direct jump from v3.7.1 to v3.9.0 on 2026-04-14 -->

## [3.7.1] - 2026-04-13

_Hotfix release: executor cooldown correction and two feature cherry-picks from stash._

### Added

- Added dynamic model fallback via suffix stripping (#65)
- Added generic agent, prompt-ranked candidates, and DRY progress helpers (cherry-picked from stash) (#64)

### Fixed

- Fixed executor cooldown to 24 hours for Spark weekly limits

[3.7.1]: https://github.com/thebtf/aimux/compare/v3.7.0...v3.7.1

## [3.7.0] - 2026-04-13

### Changed

- Refactored server.go into 6 focused files; consolidated think pattern metadata (#63)

### Fixed

- Fixed audit investigation, safe pair review, and partial consensus handling in orchestrator (T059-T069) (#62)
- Fixed think tool: isComplete logic, shared fallback, domain immutability, generic extract, and state policies (T041-T058) (#61)
- Fixed infrastructure issues: hooks corruption, FlagValueTemplate, config immutability, routing determinism (Phase 4) (#60)
- Fixed server stall thresholds, grace period, security checks, and async lifecycle handling (T018-T029) (#59)
- Fixed session layer: busy_timeout, atomic WAL, locked recovery, schema versioning (Phase 2) (#58)
- Fixed executor: lifetime reader, stderr draining, and configurable timeouts (Phase 1) (#57)
- Updated CLAUDE.md documentation for v3.7.0 server split and Result.Stderr field

[3.7.0]: https://github.com/thebtf/aimux/compare/v3.6.0...v3.7.0

## [3.6.0] - 2026-04-13

### Added

- Added human-readable summary field to think tool response
- Added guidance Phases 8-11: stall detection policy, think/consensus/debate/dialog/workflow response policies (#56)

### Fixed

- Fixed executor transient retry logic, nil-error fatal wrapping, unused parameter, struct key, and duplicate error classification (#55)

### Changed

- Updated CLAUDE.md documentation for v3.6.0

[3.6.0]: https://github.com/thebtf/aimux/compare/v3.5.0...v3.6.0

## [3.5.0] - 2026-04-13

_Single-feature release: model fallback chain._

### Added

- Added model fallback chain with per-model cooldown for rate-limited models (#54)

[3.5.0]: https://github.com/thebtf/aimux/compare/v3.4.0...v3.5.0

## [3.4.0] - 2026-04-13

_Two targeted improvements following v3.3.0._

### Added

- Added LLM-driven agent selection, replacing keyword-based auto-select (#53)

### Fixed

- Fixed resolve layer to always pipe prompt via stdin, removed length threshold logic (#52)

[Unreleased]: https://github.com/thebtf/aimux/compare/v5.5.0...HEAD
[5.5.0]: https://github.com/thebtf/aimux/compare/v5.4.0...v5.5.0
[5.4.0]: https://github.com/thebtf/aimux/compare/v5.3.0...v5.4.0
[5.3.0]: https://github.com/thebtf/aimux/compare/v5.2.2...v5.3.0
[5.2.2]: https://github.com/thebtf/aimux/compare/v5.2.1...v5.2.2
[5.2.1]: https://github.com/thebtf/aimux/compare/v5.2.0...v5.2.1
[5.2.0]: https://github.com/thebtf/aimux/compare/v5.1.0...v5.2.0
[5.1.0]: https://github.com/thebtf/aimux/compare/v5.0.3...v5.1.0
[3.4.0]: https://github.com/thebtf/aimux/compare/v3.3.0...v3.4.0

## [3.3.0] - 2026-04-12

### Added

- Added structured tool description guidance layer (Phases 1-6) (#49)
- Added Phase 7 structured guidance completion (#50)
- Added P26 tool classification artifact and guard (#48)
- Added mcp-mux v0.11.0 busy protocol support for async jobs (#41)
- Added P26 constitution entry: long-running tool calls must be interruptible (#39)

### Changed

- Removed `.agent/` from git tracking; added to .gitignore (#46)
- Updated skills documentation: mandated polling wrapper subagent for long delegations (#40)
- Added tool-response-guidance architecture and spec document (#42)

### Fixed

- Fixed session restore to recover non-terminal jobs regardless of age (#47)
- Fixed agent async progress streaming via OnOutput (#44)
- Fixed agent run to wire OnOutput through resolveArgs for async progress reporting (#45)
- Fixed orchestrator to thread AIMUX_ROLE_* model and effort through StrategyParams (#43)
- Updated README and CLAUDE.md for v3.3.0 (#51)

[3.3.0]: https://github.com/thebtf/aimux/compare/v3.2.0...v3.3.0

## [3.2.0] - 2026-04-10

_Two targeted fixes following v3.1.0._

### Added

- Added Claude Code plugin subagent discovery (#37)

### Changed

- Added goreleaser configuration for automated cross-platform binary releases
- Updated README for v3.1.0: 777 tests, ProcessManager/IOManager architecture

### Fixed

- Fixed session Store.RestoreJobs to unbreak master build (#38)
- Fixed MCP prompt names: removed redundant `aimux-` prefix

[3.2.0]: https://github.com/thebtf/aimux/compare/v3.1.0...v3.2.0

## [3.1.0] - 2026-04-09

### Added

- Added agent-first architecture with auto-select, primary tool designation, and CWD discovery (#33)
- Added live output streaming for async jobs via OnOutput callback
- Added immediate job persistence to survive process restarts
- Added graceful shutdown: drain running CLI processes before kill
- Added reasoning effort tiers for all roles
- Added think patterns flat schema with step progression for stateful patterns (#29)
- Added think patterns intelligence tiers: text analysis, forced reflection, and sampling (#28)
- Added think patterns graceful intelligence for useful output with minimal input (#27)
- Added restore of v2 computational logic for 8 think patterns (#26)
- Added skill engine with deep workflow system and 13 skills (#25)
- Added dynamic embedded skills that fetch live data (#23)
- Added embedded server instructions via WithInstructions() (#22)
- Added MCP prompts: agent guide, investigate protocol, workflow builder (#21)
- Added agent tool v2 with CLI-native architecture and MCP enum fix (#20)
- Added session persistence and deepresearch recall integration (#19)
- Added CLI auto-fallback on failure (#18)
- Added workflow tool for declarative multi-step pipelines (#17)
- Added agent tool for running project agents through any CLI (#16)
- Added 6 research think patterns (Feynman-inspired) (#15)
- Added investigate enhancements v2: auto-detect domain and cross-tool dispatch (#14)
- Added CLI binary discovery beyond PATH (#13)
- Added metrics collection and monitoring resources (#11)
- Added HTTP/SSE transport support (#10)
- Added hooks system, turn validator, and quality gate (#9)
- Added dialog context management and conflictingAreas enhancement (#8)
- Added investigate recall and 17 role prompts (#7)
- Added think tool with full 17-pattern system (#6)
- Added investigate tool: full port from v2 with 6 enhancements (#5)
- Added Dockerfile and server/PTY coverage (#4)
- Added parser wiring and production CLI profiles (#3)
- Added anti-stub verification: taxonomy, review checklist, coding rules
- Added pre-commit stub detection hook and golangci-lint config
- Added YAML config loader for audit-rules.d/ with project override merging
- Added CI stub detection job with grep scanner and deadcode analysis
- Added CI mutation testing config and weekly workflow (gremlins, 75% threshold)

### Changed

- Separated executor control/data plane into ProcessManager and IOManager (#35)
- Fixed O(n²) complexity in IOManager; shared ProcessManager across strategies (#36)
- Cleaned up unused code in think patterns; modernized maps.Copy usage
- Reverted removal of ExtractKeywords stubs after review
- Updated background skill with delegation pattern and task-based monitoring protocol

### Fixed

- Fixed scientific_method think pattern to accept observation/question/analysis/conclusion as entry_type
- Fixed codex CLI profile: add --json flag and parse modern JSONL output format
- Fixed gemini CLI profile: add -y (yolo) headless flag for auto-approve
- Fixed minimum timeout enforcement (120s) for high/xhigh reasoning tasks
- Fixed codex reasoning_effort template: removed extraneous shell quotes
- Fixed agents list tool to return summaries rather than full agent content
- Fixed docs audit findings: tools table, env var format, Dockerfile Go version
- Fixed PRC P1 blockers: panic guards, resource leaks, session GC (#30)
- Fixed PRC blockers: auth, persistence, leaks, concurrency (#12)
- Fixed ExtractKeywords call sites to wire results into autoAnalysis.keywords
- Fixed e2e tests for async exec default
- Fixed exec test for async default in TestHandleExec_AsyncWithPrompt
- Fixed CLI profile audit: session resume, reasoning_effort, model documentation
- Fixed INBOX items: async exec, built-in agents, Find enhancement (#32)

### Security

- Added server hardening: safe json.Marshal helper, rate limiting, SSE authentication (#31)

[3.1.0]: https://github.com/thebtf/aimux/compare/v3.0.0...v3.1.0

## [3.0.0] - 2026-04-06

Complete rewrite from TypeScript (v2) to Go. Single binary, zero external runtime dependencies.

### Added

- **12 CLI profiles**: codex, gemini, claude, qwen, aider, goose, crush, gptme, cline, continue, droid, opencode — all verified against CLI source code
- **11 MCP tools**: exec, status, sessions, consensus, dialog, debate, audit, think, investigate, agents, deepresearch
- **5 orchestration strategies**: PairCoding, SequentialDialog, ParallelConsensus, StructuredDebate, AuditPipeline
- **3 executor backends**: ConPTY (Windows), PTY (Linux/Mac), Pipe (fallback)
- **Profile-aware command resolution** (`pkg/resolve/`) — correct binary, prompt flags, stdin piping per CLI
- **Output parsing** (`pkg/parser/`) — JSONL, JSON, and text parsers wired into response path
- **Role-based CLI routing** — 14 roles (coding, codereview, thinkdeep, secaudit, debug, etc.)
- **Circuit breakers** per CLI with exponential backoff
- **SQLite persistence** (pure Go via modernc.org/sqlite) with session resume
- **Composable prompt templates** via `prompts.d/` with includes
- **Agent registry** with multi-source discovery (Loom Agents)
- **Deep research** via Google Gemini API with response caching
- **Dockerfile** for containerized deployment
- **306 tests** including 62 e2e tests via real MCP protocol
- **CI**: build + test on push/PR, weekly mutation testing (gremlins, 75% threshold)

### Changed

- Rewritten from TypeScript to Go for single-binary deployment
- CLI profiles moved from monolithic TOML to per-CLI YAML directories (`config/cli.d/`)
- Executor selection: ConPTY > PTY > Pipe (automatic best-available)

### Fixed

- CLI profile mismatches: codex `-p` was config profile selector not prompt flag
- claude `-p` was print mode flag not prompt flag
- droid/opencode needed `exec`/`run` subcommands for non-interactive mode
- Agents handler used raw `Command.Base` instead of `CommandBinary()`
- audit.go `validate` mutated input slice (immutability violation)
- Synthesis errors in consensus/debate silently swallowed

### Security

- All CLI processes sandboxed via `exec.Command` (no shell interpretation)
- No hardcoded secrets in codebase (verified by PRC audit)
- Config-driven flags only (no hardcoded CLI names in server code)

[3.0.0]: https://github.com/thebtf/aimux/releases/tag/v3.0.0
