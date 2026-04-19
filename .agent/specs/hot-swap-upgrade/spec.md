# Feature: Transparent Hot-Swap Upgrade via muxcore Handoff

**Slug:** hot-swap-upgrade
**Created:** 2026-04-19
**Status:** Draft
**Author:** AI Agent (reviewed by user)
**Source:** engram issue #129

> **Provenance:** Specified by claude-opus-4-7 on 2026-04-19.
> Evidence from: engram issue #129, muxcore v0.20.4 source inspection
> (`D:/Dev/mcp-mux/muxcore/{upgrade,daemon,owner}/`), existing aimux updater
> (`pkg/updater/updater.go`, `pkg/server/server.go:1426-1490`).
> Confidence: VERIFIED — muxcore handoff primitives inspected directly.

## Overview

Rewrite `mcp__aimux__upgrade(action="apply")` to use muxcore's daemon-to-daemon handoff protocol instead of the current "deferred restart" mechanism. Result: upgrade applies transparently — active CC sessions keep working, see the new version immediately, no reconnect required. Current behavior requires all CC sessions to disconnect before the daemon actually restarts with the new binary.

## Context

### Current state (v4.3.0)

`pkg/server/server.go:1426-1490` implements `handleUpgrade`:
- `check` action: queries GitHub releases via `creativeprojects/go-selfupdate`, returns version info
- `apply` action: downloads new binary, replaces the executable on disk via `updater.ApplyUpdate` (`pkg/updater/updater.go:62`), calls `sessionHandler.SetUpdatePending()`, returns `"Daemon will restart when all CC sessions disconnect."`

The daemon only actually restarts after all CC sessions have disconnected. Active sessions continue running the OLD binary until they close. This means:
- New fixes/features are NOT available to running sessions
- User has to manually close and reopen ALL CC instances to pick up the upgrade
- "Upgrade applied" signal lies — the upgrade is staged, not live

### What muxcore already provides (v0.20.4)

Inspection of `D:/Dev/mcp-mux/muxcore/`:

| Module | Capability |
|--------|-----------|
| `upgrade/upgrade.go` | `Swap(currentExe, newExe) (oldPath, err)` — atomic binary replacement; Windows-safe via rename-running-exe trick; `CleanStale(exePath) int` removes `.old.{pid}` artifacts from prior upgrades |
| `daemon/handoff.go` | `performHandoff(...)` — transfers OS file descriptors from predecessor to successor daemon via SCM_RIGHTS (Unix) or DuplicateHandle (Windows); pre-shared token authentication via `ErrTokenMismatch`; returns `HandoffResult{Transferred, Aborted, Phase}` |
| `daemon/handoff_proto.go` | Wire protocol: JSON metadata + FD transfer messages |
| `owner/handoff.go` | Owner-side participation — each per-project IPC socket can be handed off |

The primitives exist, are tested, and are already consumed by aimux master via `go.mod`. They are not yet called from `handleUpgrade`.

### Why this matters

The claim "aimux supports transparent upgrades" is currently false. Users who run `upgrade apply` get a graceful-no-drop experience but must close CC to see the new version. Wiring muxcore handoff makes the claim true: upgrade is fully live without any user action.

## Functional Requirements

### FR-1: Download and verify new binary

`upgrade(action="apply")` MUST download the latest release asset, verify its checksum via `checksums.txt`, and write it to a temp path (NOT overwrite the running binary directly). Supports same GitHub release source as v4.3.0 (`thebtf/aimux`).

### FR-2: Atomic binary swap via muxcore.upgrade

Binary replacement MUST use `muxcore.upgrade.Swap(currentExe, newExe)`. The running-exe rename technique is non-negotiable on Windows — `os.Rename` directly on a running executable fails with `ERROR_ACCESS_DENIED` without it.

### FR-3: Spawn successor daemon from new binary

After Swap, the handler MUST spawn a successor `aimux` process from the new binary with a handoff-mode flag (e.g., `--handoff-from=<socket-path> --handoff-token=<token>`). The successor must be parented correctly so its lifecycle is independent of the current daemon.

### FR-4: Execute handoff protocol

The predecessor MUST call `muxcore.daemon.performHandoff(successor_addr, token, upstreams)` to transfer:
- The engine control socket
- Per-project IPC owner sockets (one per active ProjectContext)
- Any in-flight request state that muxcore handoff supports

The handoff MUST use pre-shared token authentication (FR-11/FR-28 in muxcore terms). Token MUST be generated fresh per upgrade (no reuse across upgrade cycles).

### FR-5: Graceful predecessor exit after successful handoff

When `performHandoff` returns successfully (`HandoffResult.Phase == "complete"`, all `Transferred` IDs present), the predecessor MUST:
1. Stop accepting new connections
2. Drain in-flight requests (max 30s timeout)
3. Exit with code 0

Active CC sessions MUST remain connected — their socket path is inherited by the successor.

### FR-6: Fallback to graceful-deferred on handoff failure

If ANY of FR-1..FR-5 fails, the handler MUST fall back to the current v4.3.0 behavior:
1. Keep predecessor running
2. Set `sessionHandler.SetUpdatePending()` flag (existing code path)
3. Return message: `"Handoff failed (<reason>): falling back to deferred restart. Daemon will restart when all CC sessions disconnect."`

Fallback triggers include: binary swap failure, successor spawn failure, handoff protocol timeout (>15s), token mismatch, partial handoff (`HandoffResult.Aborted` non-empty), predecessor exit blocked by in-flight requests.

No upgrade scenario should leave the daemon in a broken state — fallback is always safe.

### FR-7: Response envelope

Successful hot-swap response MUST match schema:

```json
{
  "status": "updated_hot_swap",
  "previous_version": "4.3.0",
  "new_version": "4.4.0",
  "handoff_transferred_ids": ["engine", "project-abc", "project-xyz"],
  "handoff_duration_ms": 847,
  "message": "Upgrade applied transparently. Active sessions now running v4.4.0."
}
```

Fallback response MUST match schema:

```json
{
  "status": "updated_deferred",
  "previous_version": "4.3.0",
  "new_version": "4.4.0",
  "handoff_error": "<reason>",
  "message": "Handoff failed — falling back to deferred restart. Daemon will restart when all CC sessions disconnect."
}
```

`status` discriminates between hot-swap success and deferred fallback.

### FR-8: Stale binary cleanup

After handoff completes AND predecessor exits, `muxcore.upgrade.CleanStale(exePath)` MUST run on the successor startup to remove `.old.{pid}` artifacts from prior upgrades. This is already invoked by muxcore on daemon start; verify the aimux binary path is what's passed in.

### FR-9: Cross-platform parity

Behavior MUST be identical on Windows, Linux, macOS. Differences are implementation (SCM_RIGHTS vs. DuplicateHandle, `os.Rename` semantics) not behavior (transparent swap works everywhere).

### FR-10: Token security

The handoff token:
- MUST be generated via `crypto/rand` — 32 bytes, hex-encoded
- MUST be passed to the successor only via an ephemeral mechanism (env var or stdin), NEVER logged or written to disk
- MUST be invalidated after handoff completes (one-shot use)
- Mismatch MUST be a hard error (`ErrTokenMismatch`), never a warning

## Non-Functional Requirements

### NFR-1: Handoff latency

Total `upgrade apply` MCP call (from request to response) MUST complete in <5s on typical hardware (daemon with 1-3 active project contexts). p99 <10s. Includes: download (cached), swap, spawn, handoff, response.

### NFR-2: Zero session disruption

Active CC sessions MUST NOT see any of:
- Connection drop
- Stall longer than 2s
- Error response to any MCP tool call
- Loss of in-flight job state (async exec jobs survive the handoff)

Measured via: integration test spawns CC session, starts long-running `agent(async=true)`, triggers upgrade, polls `status(job_id)` across the handoff, verifies (a) status responses never fail, (b) `new_version` observable in next `status()` brief after handoff completes.

### NFR-3: No regression on deferred-fallback path

Current v4.3.0 behavior MUST remain intact as fallback. All tests against `SetUpdatePending()` + "restart when all CC disconnect" semantics MUST continue to pass.

### NFR-4: Test coverage

- Unit test: handoff token generation (crypto/rand, 32 bytes, hex)
- Unit test: response envelope shape for both hot-swap and fallback
- Integration test: full upgrade cycle on Linux (e2e test in CI)
- Integration test: full upgrade cycle on Windows (e2e test in CI)
- Integration test: fallback path (inject handoff failure, verify deferred behavior + error reporting)
- Test: active async job survives handoff (seed job → upgrade → verify job still queryable)

### NFR-5: Platform security

- Handoff socket on Unix MUST be created with 0600 permissions (FR-29 in muxcore)
- Handoff token MUST NOT appear in process environment after spawn completes
- Successor daemon MUST refuse handoff without valid token (already enforced by muxcore)

### NFR-6: Rollback safety

If handoff fails mid-protocol, the predecessor daemon state MUST remain valid. Partial state (some FDs transferred, some not) MUST be detectable — predecessor either completes fully or reverts to serving all its existing sessions.

## User Stories

### US1: Operator applies transparent upgrade (P1)

**As a** developer running aimux with an active CC session,
**I want** to apply a new release without closing my CC session,
**so that** I can pick up bug fixes and features without disrupting my work.

**Acceptance Criteria:**
- [ ] `mcp__aimux__upgrade(action="check")` returns `update_available` with new version
- [ ] `mcp__aimux__upgrade(action="apply")` returns `status: updated_hot_swap` within 5s
- [ ] Immediately after the call returns, `mcp__aimux__status()` from the same CC session reports the new version
- [ ] No CC session disconnect, no error response on any MCP call during or after upgrade
- [ ] swap body → return nil ⇒ the test above MUST fail

### US2: Automated release pipeline applies upgrade non-interactively (P1)

**As a** release automation script running in background,
**I want** upgrade apply to complete or fail deterministically within 10s,
**so that** I can sequence upgrade + validation checks without manual intervention.

**Acceptance Criteria:**
- [ ] `upgrade apply` returns JSON with discriminator field `status` (one of: `updated_hot_swap`, `updated_deferred`, `up_to_date`, `error`)
- [ ] On handoff failure, returns `status: updated_deferred` with `handoff_error` populated
- [ ] Never hangs past 30s — partial progress returns clearly
- [ ] swap body → return nil ⇒ tests MUST fail

### US3: Operator audits upgrade history (P3)

**As a** developer auditing infrastructure changes,
**I want** upgrade events logged with version, timestamp, duration, method (hot-swap vs. deferred), and handoff-transferred IDs,
**so that** I can verify transparent upgrades occurred as expected.

**Acceptance Criteria:**
- [ ] Every upgrade emits a structured log line: `module=server.upgrade event=upgrade_complete prev_version=X new_version=Y method=<hot_swap|deferred> duration_ms=N transferred_ids=[...]`
- [ ] Log level: INFO on success, WARN on deferred-fallback, ERROR on complete failure
- [ ] swap body → return nil ⇒ log-grep test MUST fail

## Edge Cases

- **Successor fails to start** (new binary corrupt, missing dependency): handoff aborts BEFORE FDs transferred → predecessor keeps running → fallback to deferred, error reported.
- **Token mismatch** (handoff protocol compromised or race condition): `ErrTokenMismatch` → handoff aborts → fallback.
- **Handoff times out** (successor hangs during FD negotiation): 15s timeout → predecessor reclaims state → fallback.
- **Multiple concurrent upgrade calls**: Second call sees `update_pending` flag already set → returns `already_in_progress` error, does not initiate second handoff.
- **CC session connects to mid-handoff socket** (race): muxcore handoff protocol serializes socket accept during handoff window; new connections see connection refused for <500ms, client reconnects transparently.
- **Predecessor has uncompleted async job at handoff time**: Job state is in per-project context, transferred via muxcore handoff. Test case NFR-4 verifies this.
- **Checksum verification fails** (download corrupted or tampered): Handler aborts before any swap; no state change; returns `error` status with `checksum_failed` reason.
- **Disk full during binary swap**: `muxcore.upgrade.Swap` returns error → handler falls back to graceful-deferred without swapping (predecessor keeps running on old binary). No partial-swap state.
- **.old.{pid} files accumulate** if predecessor crashes instead of cleanly exiting: Next successor startup's `CleanStale` clears them. Old-binary cruft self-heals.

## Out of Scope

- Downgrade support (intentionally — v4.4.0 ships forward-only)
- Signed binary verification (beyond checksum) — separate security spec
- Rollback after successful upgrade (different scope — see `release --rollback` workflow)
- Cross-version session state migration (e.g., v4.3.0 → v5.0.0 with breaking schema change) — deferred to future spec
- UI for upgrade (CLI-only via MCP tool)
- Non-GitHub release sources (custom feed, self-hosted) — future spec

## Dependencies

- **muxcore v0.20.4+** — already on master via go.mod
- **creativeprojects/go-selfupdate v1.3+** — retained for download + checksum path
- No new external dependencies

## Success Criteria

- [ ] Integration test: upgrade from a running v4.3.0 daemon to a mock v4.4.0 binary succeeds in <5s with no session drop, on all 3 CI platforms (Linux/macOS/Windows)
- [ ] Integration test: injected handoff failure triggers clean fallback to deferred restart
- [ ] Unit test coverage on new code ≥85%
- [ ] NFR-1 p99 latency <10s measured via benchmark
- [ ] CHANGELOG documents v4.3.0 → v4.4.0 upgrade behavior change
- [ ] Engram #129 resolved with link to merged PR

## Clarifications (Session 2026-04-19, --auto)

- Q1: handoff-per-upgrade vs opt-in → A1: default `mode="auto"` (try hot-swap, fall back to deferred on any failure). Optional `mode="deferred"` to force legacy behavior. `mode="hot_swap"` to skip fallback (fails hard if handoff fails — for testing).
- Q2: AIMUX_NO_ENGINE behavior → A2: handoff is no-op when not in engine mode. Handler detects engine mode from `sessionHandler` type assertion; non-engine falls through to graceful-deferred directly.
- Q3: successor env/CWD → A3: inherit all env vars from predecessor process including `AIMUX_*` and process CWD. Use `os.Environ()` + `exec.Cmd.Env = os.Environ()` pattern; never synthesize partial env.

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Functional | Handoff-per-upgrade vs opt-in | mode param with `auto` default | 2026-04-19 |
| C2 | Functional | Non-engine-mode behavior | no-op; fall through to deferred | 2026-04-19 |
| C3 | Functional | Successor env/CWD inheritance | inherit all (os.Environ + CWD) | 2026-04-19 |
