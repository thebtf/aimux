# Tasks: Transparent Hot-Swap Upgrade

**Spec:** .agent/specs/hot-swap-upgrade/spec.md
**Plan:** .agent/specs/hot-swap-upgrade/plan.md
**Generated:** 2026-04-19

## Phase 1: Foundation — updater split + Coordinator skeleton

- [ ] T001 [EXECUTOR: sonnet] Split `pkg/updater/updater.go`: separate `Download(ctx, version, targetPath) (*Release, error)`, `VerifyChecksum(path, expected) error`, `Install(newExePath) error` from monolithic `ApplyUpdate`
  AC: ApplyUpdate becomes thin wrapper calling Download + VerifyChecksum + Install · backwards compat: existing handleUpgrade still works · unit tests for each split function · swap body→return nil ⇒ tests fail

- [ ] T002 [EXECUTOR: sonnet] Create `pkg/upgrade/coordinator.go` with Coordinator type + Mode enum + Result struct (per plan "Internal contract") — skeleton only, methods return `Result{Method: "deferred"}` for now
  AC: Coordinator{Version, BinaryPath, SessionHandler, EngineMode, Logger} compiles · Mode enum with ModeAuto/ModeHotSwap/ModeDeferred · Apply(ctx, mode) returns placeholder Result · unit test constructs Coordinator + calls Apply · swap body→return nil ⇒ tests fail

- [ ] T003 [EXECUTOR: sonnet] Rewire `pkg/server/server.go:handleUpgrade("apply")` to delegate to `upgrade.Coordinator.Apply(ctx, ModeAuto)` — preserving current `SetUpdatePending` behavior via the Deferred path for now
  AC: handleUpgrade builds Coordinator, calls Apply · response envelope derived from Result · existing "daemon will restart..." message preserved for deferred path · unit tests for handleUpgrade pass unchanged · swap body→return nil ⇒ tests fail

- [ ] G001 [EXECUTOR: MAIN] VERIFY Phase 1 — BLOCKED until T001-T003 all [x]
  RUN: `go build ./... && go test ./pkg/updater/... ./pkg/upgrade/... ./pkg/server/... -count=1 -timeout 120s`. Skill("code-review", "lite") on phase files.
  CHECK: zero behavior change vs v4.3.0 · handleUpgrade still produces deferred restart message
  ENFORCE: Zero stubs in production path (Coordinator skeleton with delegation is OK since Phase 1 goal is structural). Zero TODOs.

---

**Checkpoint Phase 1:** updater split + Coordinator wiring. No hot-swap yet, v4.3.0 behavior preserved.

## Phase 2: Successor daemon mode

- [ ] T004 [EXECUTOR: sonnet] Add `--handoff-from <socket>` and `--handoff-token <hex>` flag parsing to `cmd/aimux/main.go`
  AC: flags parsed via standard `flag` package · validation: token is 64-char hex, socket path exists · if either flag set, both must be set · unit test for flag parsing + validation errors · swap body→return nil ⇒ tests fail

- [ ] T005 [EXECUTOR: sonnet] Implement successor bootstrap in `cmd/aimux/main.go`: when `--handoff-from` set, connect to that socket, present token, receive FDs via `muxcore/daemon.performHandoff` (successor side), then start engine serving on received FDs
  AC: connection to predecessor succeeds · token mismatch → exit 2 with clear error log · successful handoff transitions to normal engine serve loop · e2e: spawn mock predecessor, verify successor inherits FDs · swap body→return nil ⇒ tests fail

- [ ] G002 [EXECUTOR: MAIN] VERIFY Phase 2 — BLOCKED until T004-T005 all [x]
  RUN: `go build ./cmd/aimux/ && go test ./cmd/... -count=1 -timeout 60s`. Integration test spawning successor.
  CHECK: successor mode activated only by flags · non-handoff start path unchanged · token validation enforced
  ENFORCE: NFR-5 socket perms 0600 on Unix.

---

**Checkpoint Phase 2:** new binary knows how to receive a handoff. Predecessor flow next.

## Phase 3: Predecessor handoff flow

- [ ] T006 [EXECUTOR: sonnet] Implement `pkg/upgrade/handoff.go` — thin wrapper around `muxcore/daemon.performHandoff` with aimux-specific upstream-enumeration (engine socket + per-project owner sockets from session handler)
  AC: `PerformHandoff(ctx, successorAddr, token, upstreams) (*HandoffResult, error)` · collects list of upstreams from SessionHandler · isolated enough to mock in tests · swap body→return nil ⇒ tests fail

- [ ] T007 [EXECUTOR: sonnet] Implement `Coordinator.tryHotSwap(ctx)` in `pkg/upgrade/coordinator.go`: (1) engine-mode detection, (2) Download via updater to temp path, (3) VerifyChecksum, (4) crypto/rand 32-byte token, (5) muxcore.upgrade.Swap(binaryPath, tempPath), (6) os/exec.Command spawn successor with env inherited, (7) PerformHandoff wrapper with 15s timeout, (8) on success: SetDraining → drain 30s → os.Exit(0) deferred; on failure: return error for caller to fall back
  AC: each step has distinct error class (quota → deferred fallback, token mismatch → hard fail in ModeHotSwap) · structured log emit per US3 · integration test on fake release source · swap body→return nil ⇒ tests fail

- [ ] T008 [EXECUTOR: sonnet] Implement `Coordinator.Apply` mode routing: ModeDeferred → direct fallback, ModeHotSwap → tryHotSwap only (no fallback, return error), ModeAuto → tryHotSwap then fallback on any error
  AC: three modes produce three distinct paths · fallback preserves v4.3.0 deferred behavior byte-for-byte · response envelope shape matches FR-7 · unit test for each mode branch · swap body→return nil ⇒ tests fail

- [ ] T009 [EXECUTOR: sonnet] Add `mode` parameter to `upgrade` MCP tool schema in `pkg/server/server.go`: optional string, enum `auto|hot_swap|deferred`, default `auto`
  AC: schema validates via mcp-go library · handleUpgrade passes parsed mode to Coordinator.Apply · backwards compat: missing param defaults to auto · unit tests for all three modes via MCP contract tests · swap body→return nil ⇒ tests fail

- [ ] T010 [EXECUTOR: sonnet] E2E integration test `test/e2e/upgrade_hot_swap_test.go`: spin up daemon on v1.0.0 binary, serve mock release for v1.0.1, start async `agent` job, call upgrade apply, verify (a) response has status=updated_hot_swap, (b) async job remains queryable across handoff, (c) mcp__aimux__status shows new version within 1s of response
  AC: test runs on ubuntu-latest + macos-latest in CI · timeout 60s per test · swap body→return nil ⇒ test fails

- [ ] T011 [EXECUTOR: sonnet] E2E integration test `test/e2e/upgrade_fallback_test.go`: inject handoff failure (force-kill successor mid-handoff, or use `mode=hot_swap` on non-engine daemon), verify (a) response has status=updated_deferred with handoff_error populated, (b) predecessor keeps running unchanged
  AC: fallback triggered in predictable way · test asserts deferred state is clean · swap body→return nil ⇒ test fails

- [ ] G003 [EXECUTOR: MAIN] VERIFY Phase 3 — BLOCKED until T006-T011 all [x]
  RUN: `go build ./... && go test ./... -count=1 -timeout 300s`. Skill("code-review", "lite").
  CHECK: NFR-2 (zero session disruption) verified by T010 · NFR-3 (no regression on deferred path) verified by existing handleUpgrade tests + T011 · FR-6 fallback triggers all specified failure classes
  ENFORCE: hot-swap on linux+macos working.

---

**Checkpoint Phase 3:** hot-swap works end-to-end. Windows polish in Phase 4.

## Phase 4: Cross-platform + polish

- [ ] T012 [EXECUTOR: sonnet] Verify Windows path: add `upgrade_hot_swap_windows_test.go` (build tag windows) mirroring T010 but with Windows-specific muxcore DuplicateHandle path
  AC: test passes on windows-latest CI · timeouts may be longer · swap body→return nil ⇒ test fails

- [ ] T013 [EXECUTOR: sonnet] Edge-case handling in `Coordinator.Apply`: (a) concurrent upgrade calls return `already_in_progress`, (b) disk full during Swap → fallback with `disk_full` error class, (c) checksum fail → hard error (no fallback)
  AC: each edge case has dedicated test · response envelope `handoff_error` field names match edge-case list · swap body→return nil ⇒ tests fail

- [ ] T014 [EXECUTOR: sonnet] Structured logging per US3: every apply emits `module=server.upgrade event=upgrade_complete prev_version=X new_version=Y method=<hot_swap|deferred> duration_ms=N transferred_ids=[...]`. INFO on success, WARN on deferred-with-error, ERROR on hard failure.
  AC: log lines greppable with exact field names · test captures logger output · swap body→return nil ⇒ tests fail

- [ ] T015 [EXECUTOR: MAIN] Update `pkg/server/server.go` const Version from "4.3.0" to "4.4.0-dev" (release PR will finalize to "4.4.0")
  AC: grep test for "4.3.0" in server.go finds nothing · swap body→return nil ⇒ grep test fails

- [ ] T016 [EXECUTOR: MAIN] Update CHANGELOG.md [Unreleased] section with v4.4.0 hot-swap upgrade notes
  AC: CHANGELOG has Added section for hot-swap, Changed section for upgrade tool schema, migration note for operators · swap body→return nil ⇒ grep test fails

- [ ] T017 [EXECUTOR: MAIN] Update AGENTS.md or project docs: new upgrade behavior, `mode` param, fallback semantics
  AC: docs mention hot-swap vs deferred · link to engram #129 resolution · swap body→return nil ⇒ grep test fails

- [ ] G004 [EXECUTOR: MAIN] VERIFY Phase 4 — BLOCKED until T012-T017 all [x]
  RUN: Full test suite + e2e + lint + stub-detection. Skill("code-review") full review.
  CHECK: FR-1..FR-10 all verified · NFR-1..NFR-6 all verified · CHANGELOG accurate · version bumped
  ENFORCE: zero stubs · zero TODOs · Windows CI green · no regressions

## Dependencies

- Phase 1 blocks all (Coordinator + updater split are foundation)
- Phase 2 blocks Phase 3 (successor mode needed before predecessor flow can e2e)
- Phase 4 depends on Phase 3 (polish after core works)
- G001 → G002 → G003 → G004

## Execution Strategy

- **MVP scope:** Phases 1-3 (hot-swap on Linux/macOS with fallback). Ship as pre-release.
- **Full scope:** All 4 phases. Target v4.4.0 release.
- **Parallel opportunities:**
  - T004 and T006 (different files) can run parallel in Phase 2/3 overlap
  - T010 and T011 parallel (different test files)
  - T016 and T017 parallel (docs)
- **Commit strategy:** one commit per T-task; GATE tasks marked [x] only after all CHECK items pass

## Clarifications resolved

Per spec.md §Clarifications:
- C1: `mode="auto"` default, tryHotSwap+fallback
- C2: non-engine-mode → skip hot-swap, go direct to deferred
- C3: successor inherits full env + CWD from predecessor
