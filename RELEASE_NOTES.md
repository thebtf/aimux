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
