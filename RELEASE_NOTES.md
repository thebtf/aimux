## v5.10.1 — 2026-05-08 (hotfix)

### Bug Fixes

- **fix(codex): codex executor no longer broken on first call** (#170). v5.10.0 shipped a critical regression — every `codex_task` / `codex_review` / `task` (with codex selection) call failed with `Invalid request: missing field clientInfo`. Root cause: Go `InitializeParams` struct lacked the `ClientInfo` field that codex 0.128.0 requires. Also added `ExperimentalApi` field to `InitializeCapabilities` for full schema completeness. Codex executor now operational.

### Test Coverage

- **test(codex): wire-format snapshot tests** (#171). All 65+ existing codex unit tests mocked JSONLClient — none verified Go struct JSON output against codex protocol schema. That's how the v5.10.0 regression shipped. Added 17 schema-snapshot tests that JSON-marshal each protocol param type and assert required fields per `.agent/codex-types-generated/v2/`. Catches codex schema drift at compile-test time without needing the `codex` binary.

- **test(e2e): codex initialize integration test against real binary** (#170). New `TestE2E_CodexInitialize_RealBinary` (gated by `CODEX_E2E=1` env). Spawns real `codex app-server`, calls `Start()`, asserts no error. The test that would have caught the v5.10.0 regression.

### Security

- **chore: Go 1.25.10 + golang.org/x/net v0.53.0**. Upgraded Go toolchain from 1.25.9 → 1.25.10 and `golang.org/x/net` from v0.47.0 → v0.53.0 to resolve 4 govulncheck findings (GO-2026-4982, GO-2026-4980, GO-2026-4971 in stdlib; GO-2026-4918 in x/net). All 4 findings report 0 vulnerabilities after upgrade. No API changes — govulncheck confirmed `No vulnerabilities found`.

### Note for v5.10.0 users

If you upgraded to v5.10.0 and saw `codex_task` failing with `clientInfo` errors — upgrade to v5.10.1.

---

## v5.10.0 — 2026-05-07

### New Features

- **AIMUX-3 — CLI Default Picker** (#166). New package `pkg/executor/picker/` provides routing function `Picker.Pick(ctx, TaskSpec) (CLIName, error)` selecting optimal CLI by config override + health check + capability score. Static score table with codex/claude/gemini per task class. `CapabilityScore.Score(cli, task) int` ∈ [0,100] for human-readable; `Scoref(cli, task) float64` ∈ [0,1] for downstream composite arithmetic. HealthChecker with 60s TTL cache. ErrNoHealthyCLI hard fail (no silent degradation).

- **AIMUX-4 — Cross-CLI Fallback Runtime Engine** (#168). New package `pkg/executor/fallback/` provides runtime re-rank when picker-selected CLI fails at dispatch. Components: FailureClassifier (typed CLIErrorCode switch), Orderer (composite score capability+success+latency+recency, all `[0,1]` normalized), InMemoryScoreStore (sync.Map, v2 Loom SQLite deferred), PassThroughTranslator, Fallback engine, FallbackPicker. Generic `task` MCP tool registered (32 → 33 MCP tools). Per-task `fallback: false` opt-out. Bounded `max_attempts` (default 2).

- **AIMUX-18 CR-003 — Codex Thread Compaction** (#169). AppServerProcess.Compact RPC sends `thread/compact/start { threadId }`, waits for `turn/completed`. Worker-side threshold trigger at 181,880 input tokens (70% of 258,400 context window) with 5-turn throttle. CodexTaskMeta gains LastInputTokens + CompactionCount visible via codex_status when include_content=true. userPromptSubmit hook side-effect documented in tool descriptions (no suppression).

### Internal Refactors

- **AIMUX-18 CR-004 — Typed CLIError Contract** (#167). New package `pkg/executor/types/CLIError` + 9-variant `CLIErrorCode` enum (Unknown/RateLimit/AuthExpiry/Timeout/CapabilityMismatch/UserInputError/SandboxDenial/BinaryNotFound/Canceled). All public CodexWorker errors now return `*types.CLIError` — callers use `errors.As(err, &cliErr)` to switch over typed codes. Replaces brittle string matching on stderr. Required by AIMUX-4 FailureClassifier.

### MCP Surface

32 → 33 tools. New: `task` (generic CLI router with fallback).

### Quality

- 3 MAJOR concurrency bugs caught + fixed in CR-003 review (`Compact` state-leak, stale notification filter, TOCTOU race in `maybeCompact`)
- 9 CRIT/MAJOR review fixes across PRs (Picker config-override scope leak, Score[]InMemorySa clamp, OutputSchema wiring, env handling)
- All test gates green: 48 packages, race detector enabled, critical suite, loom standalone

---

## v5.9.0 — 2026-05-07

### Breaking Changes

- **CLI surface trimmed from 13 to 3** (AIMUX-19, #162). Active: `codex`, `claude`, `gemini`. Archived (preserved at `archive/v5.8.2-pre-cli-trim/`): `aider`, `cline`, `codex-int`, `continue`, `crush`, `droid`, `goose`, `gptme`, `opencode`, `qwen`. Restoration recipe in archive README.

### New Features

- **CLIRuntimeProfile abstraction** (AIMUX-20, #163). New package `pkg/executor/runtime/` provides per-CLI virtual environment configuration: HomeOverride, AuthScope, StateScope, VirtualInstructionFiles, MCPMode, EnvVars. Per-CLI factories for codex (full virtualization via CODEX_HOME), claude (--bare + --strict-mcp-config), gemini (degraded mode per upstream issue #8440). Includes Spawn() integration with pkg/executor/pipe/SpawnArgs, EphemeralCleanupHook with root-path guard. 24 unit tests.

- **Codex executor — Phase 1-3 of AIMUX-18** (#164). New package `pkg/executor/codex/` provides:
  - `types.go` — Go structs mirroring v2/* protocol types (verified against codex-cli 0.128.0)
  - `jsonl_client.go` — JSON-RPC 2.0 over stdio JSONL transport
  - `appserver.go` — `AppServerProcess` state machine driving codex app-server subprocess
  - `pool.go` — `CodexPool` keyed by ProjectContext.ID with idle eviction
  - `worker.go` — Loom worker adapter for end-to-end Codex task execution
  - `sandbox.go` — SandboxConfig.ForClass strategy (review/task/write-task/danger)
  - 65 unit tests
  - Phase 4-6 (5 MCP tools, Resumer, integration tests, compaction) deferred to next release

### Specifications

- **spec(response-budget)** — MCP default-brief / opt-in-full response budget policy (#161). Defines uniform parameter grammar (`fields`, `limit`, `offset`, `include_content`, `tail`) across all aimux MCP tools with ~4k default budget. Spec only — implementation tracked separately.

### Documentation

- Codex plugin audit: `.agent/reports/2026-05-07-codex-plugin-cc-audit.md` (1614 LOC) — engineering reference for Codex app-server protocol with verbatim TS types from `codex app-server generate-ts`.
- CLI Runtime Profile research: `.agent/reports/2026-05-07-cli-runtime-profile-research.md` (902 LOC) — per-CLI startup-state inventory, override matrix, design proposal.

### Quality

- 5/5 empirical tests of codex app-server protocol PASS (cwd handling, sandbox enforcement, concurrent isolation, resume across restart, termination)
- All test gates green: 48 packages, race detector, critical suite, loom standalone module
