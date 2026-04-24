# Feature: Transparent Hot-Swap Upgrade via muxcore Handoff

**Slug:** hot-swap-upgrade
**Created:** 2026-04-19
**Status:** Draft
**Author:** AI Agent (reviewed by user)

> **Provenance:** Originally specified on 2026-04-19, amended on 2026-04-23.
> Evidence from: `.agent/specs/hot-swap-upgrade/user_job_statement.md`, `pkg/server/server.go`, `pkg/upgrade/coordinator.go`, `cmd/aimux/main.go`, `cmd/ctl/main.go`, `github.com/thebtf/mcp-mux/muxcore@v0.21.6/{daemon/control,owner,engine}`.
> Confidence: VERIFIED — current codebase and vendored muxcore API surface were inspected directly.

## Overview

Make `upgrade(action="apply")` use muxcore's real daemon-side live-handoff capability when aimux runs under the engine daemon, specifically through the existing daemon control-socket restart path, instead of stopping at a session-side deferred-restart adapter. The feature must preserve today's safe deferred behavior when live handoff is unavailable, but it must stop treating "hot swap unavailable" as the target end state.

## Context

The current aimux codebase already contains two important pieces:
1. A working successor-side bootstrap path in `cmd/aimux/main.go` that can receive handoff payloads and relay them into muxcore's restore flow.
2. A truthful server-side boundary in `pkg/upgrade/coordinator.go` that now refuses to fake live handoff from a session-local adapter.

What is still missing is the real daemon-side wiring. muxcore already exposes the control-plane and owner-handoff primitives required for a live daemon restart:
- daemon restart entrypoint via `HandleGracefulRestart`
- predecessor handoff orchestration via `attemptHandoff`
- owner detachment via `ShutdownForHandoff`
- successor receive/reattach via `ReceiveHandoff` and `NewOwnerFromHandoff`

The amended scope is therefore no longer "prepare the shape for future hot swap." The new priority is to connect aimux's `upgrade apply` flow to the real muxcore daemon path so that the existing capability becomes user-visible. Windows is a first-class priority platform for this work, not a follow-up polish stage.

> **Evidence anchor:** The user asked to "plan the full implementation of muxcore's capabilities", to "expand the specification with this task", and to "make this the priority" (`user_job_statement.md:10-13`). The user also explicitly asked whether aimux is failing to implement capabilities muxcore already provides (`user_job_statement.md:19-20`).

## Domain Modeling

DDD evaluated — not needed. This feature is infrastructure control-flow work around daemon lifecycle, handoff orchestration, and upgrade semantics, not a business-domain entity model.

## Functional Requirements

### FR-1: Live upgrade must use the daemon-side muxcore restart path

When aimux is running under the muxcore engine daemon, `upgrade(action="apply", mode="auto"|"hot_swap")` must request a daemon-side live restart/handoff path instead of relying only on the session-local `SetUpdatePending()` mechanism.

This requirement is satisfied only when the live daemon path owns the restart orchestration and the predecessor/successor exchange goes through muxcore's real owner-handoff flow.

### FR-2: The live path must preserve the real muxcore predecessor contract

The predecessor side must hand off using daemon-owned resources, not synthesized logical descriptors. Any implementation claiming live hot swap must be compatible with owner-detach payloads that contain the information required by muxcore's successor restore path.

### FR-3: The successor path must remain compatible with the current bootstrap flow

The existing successor bootstrap in `cmd/aimux/main.go` must continue to accept handoff credentials, receive muxcore handoff payloads, and feed them into the successor restore path without requiring a separate binary or alternate startup mode.

### FR-4: Mode semantics must be user-visible and truthful

`upgrade(action="apply")` must keep these externally visible semantics:
- `mode="deferred"` skips live handoff and stages the existing deferred restart path.
- `mode="hot_swap"` uses only the live daemon path and returns an error if it cannot complete.
- `mode="auto"` tries the live daemon path first, then falls back to deferred behavior on any recoverable live-handoff failure.

### FR-5: Response envelopes must distinguish live success from deferred fallback

The response must clearly distinguish:
- live handoff success (`status: updated_hot_swap`)
- deferred fallback after a live-path failure (`status: updated_deferred`)
- already up to date (`status: up_to_date`)

The response must never imply that a live handoff succeeded when only a deferred restart was staged.

### FR-6: Active sessions must survive a successful live handoff

A successful live upgrade must preserve active client sessions through the daemon transition. A caller that triggered the upgrade from an active session must be able to continue using the same session after the live handoff completes.

### FR-7: Async work must remain queryable after live handoff

If an async job exists when the upgrade is applied, the caller must be able to query it after a successful live handoff. The upgrade feature is incomplete if the daemon survives but active async work becomes inaccessible or orphaned.

### FR-8: Non-engine mode must remain safe and explicit

If aimux is not running under the engine daemon, the upgrade flow must not attempt live handoff. It must use the deferred path directly and surface that behavior clearly.

### FR-9: Existing deferred behavior must remain backward-compatible

The current safe behavior — download/update the binary and restart only after clients disconnect — must remain available as the compatibility path and fallback path. Existing automation that depends on deferred behavior must keep working.

### FR-10: Live handoff failure classes must be observable

The feature must surface meaningful failure classes for live handoff attempts, including at minimum:
- engine/daemon path unavailable
- successor bootstrap failure
- handoff authentication failure
- protocol/timeout failure
- fallback taken after live-path failure

### FR-11: Internal control access must be explicit in aimux architecture

The upgraded design must introduce an explicit internal path from aimux's upgrade flow to the live daemon restart capability. The architecture must not depend on accidental access to local variables, hidden globals, or fake session-only adapters.

### FR-12: The feature priority is full muxcore integration, not boundary-only honesty

The amended goal is to finish the actual muxcore-backed integration path. Boundary enforcement and truthful fallback behavior remain required, but they are now a foundation step, not the target deliverable.

## Non-Functional Requirements

### NFR-1: Truthful behavior

No code path may report live hot-swap success unless the daemon-side muxcore handoff actually completed.

### NFR-2: Session continuity

A successful live handoff must not require the user to reconnect their client session manually.

### NFR-3: Fallback safety

Any live-path failure must leave the process in a safe, recoverable state and preserve the existing deferred-restart fallback.

### NFR-4: Control-path determinism

The live upgrade path must rely on one explicit daemon control route only. Multiple partially-overlapping restart paths are not acceptable.

### NFR-5: Testability

The feature must be verifiable at three levels:
- unit tests for mode routing and truthful fallback behavior
- integration tests for daemon-side restart/handoff success and failure
- end-to-end tests proving session/job continuity across live handoff

### NFR-6: No fake plumbing

Session-local adapters, synthetic logical upstream descriptors, or mocked success paths are acceptable in tests only when they explicitly verify boundaries or failures. They are not acceptable as production substitutes for daemon-side handoff.

### NFR-7: Windows-first parity

Windows support is mandatory for this priority implementation. The design, implementation path, and verification plan must treat Windows as a first-class target from the start, including named-pipe or DuplicateHandle handoff behavior where muxcore uses platform-specific transport semantics.

## User Stories

### US1: Live upgrade from an active client session (P1)
**As a** developer using aimux from an active client session,
**I want** `upgrade apply` to use the real live daemon handoff path,
**so that** I receive the new binary without closing and reopening my session.

**Acceptance Criteria:**
- [ ] `upgrade(action="apply", mode="auto")` attempts the daemon-side live path when engine mode is active.
- [ ] On success, the response returns `status: updated_hot_swap`.
- [ ] The same client session remains usable after the live handoff completes.
- [ ] swap body → return nil ⇒ the live-upgrade verification must fail.

### US2: Honest fallback when live handoff cannot complete (P1)
**As a** developer applying an upgrade during active work,
**I want** auto mode to fall back safely and truthfully,
**so that** I do not get a false success signal when the live daemon path cannot complete.

**Acceptance Criteria:**
- [ ] `mode="auto"` falls back to deferred behavior on a recoverable live-path failure.
- [ ] The response returns `status: updated_deferred` with a non-empty `handoff_error`.
- [ ] The current process remains usable after the fallback.
- [ ] swap body → return nil ⇒ fallback-path verification must fail.

### US3: Strict hot-swap mode for validation and operator confidence (P2)
**As an** operator validating live-upgrade readiness,
**I want** `mode="hot_swap"` to fail hard when the live daemon path is unavailable,
**so that** I can distinguish incomplete integration from a real live-handoff success.

**Acceptance Criteria:**
- [ ] `mode="hot_swap"` does not silently fall back.
- [ ] The returned error identifies the live-path blocker or handoff failure class.
- [ ] swap body → return nil ⇒ strict-mode verification must fail.

### US4: Async work survives the live daemon transition (P2)
**As a** user with background work in progress,
**I want** outstanding async jobs to remain queryable after a successful live upgrade,
**so that** the upgrade does not orphan work already in flight.

**Acceptance Criteria:**
- [ ] An async job started before upgrade remains queryable after live handoff.
- [ ] Status polling from the same client session continues to work after the upgrade.
- [ ] swap body → return nil ⇒ continuity verification must fail.

### US5: Windows operator gets the same live-upgrade semantics first-class (P1)
**As a** Windows user running aimux locally,
**I want** the priority hot-swap implementation to support my platform as a first-class target,
**so that** live upgrade is not treated as "done" on Unix while Windows remains a later afterthought.

**Acceptance Criteria:**
- [ ] The design explicitly chooses a daemon-side control seam that works for Windows too.
- [ ] Windows-specific handoff behavior is part of the main verification story, not only a later polish note.
- [ ] Feature completion is blocked until Windows passes the agreed live-upgrade gate.

## Edge Cases

- The daemon control path exists but refuses `graceful-restart`.
- The successor bootstrap starts but the handoff authentication fails.
- The handoff protocol starts but times out before completion.
- The upgrade is requested in non-engine mode.
- The upgrade is requested while another upgrade attempt is already in progress.
- The daemon restarts but async work is no longer queryable.
- The live handoff succeeds on one platform but silently degrades on another.
- Windows named-pipe or DuplicateHandle behavior differs from Unix assumptions and breaks parity.
- An unrelated e2e regression obscures feature verification; feature-specific verification must remain identifiable and separable.

## Out of Scope

- Designing new muxcore handoff primitives from scratch.
- Replacing muxcore's existing graceful-restart/handoff model with a separate aimux-owned protocol.
- Shipping a user-facing UI for upgrade management.
- Unrelated e2e/testcli cleanup outside what is strictly required to verify the live-upgrade feature.

## Dependencies

- `github.com/thebtf/mcp-mux/muxcore v0.21.6` public daemon/control/handoff APIs.
- Current successor bootstrap in `cmd/aimux/main.go`.
- Current update download/checksum/install flow in `pkg/updater/updater.go`.
- Existing `upgrade` MCP tool in `pkg/server/server.go`.

## Success Criteria

- [ ] The spec no longer describes muxcore capability as externally blocked when the capability already exists in the current vendored version.
- [ ] The implementation introduces a real daemon-side control path from aimux upgrade flow to muxcore live restart/handoff, using the existing daemon control socket protocol as the internal integration seam.
- [ ] `mode="auto"`, `mode="hot_swap"`, and `mode="deferred"` are all truthful and test-enforced.
- [ ] A successful live upgrade is demonstrated with session continuity and async-job continuity.
- [ ] A failed live path demonstrably falls back to deferred behavior without false success reporting.
- [ ] Windows passes the same agreed live-upgrade completion gate as Linux and macOS before the feature is considered complete.

## Open Questions

- [NEEDS CLARIFICATION] The user clarified that Windows is the priority platform. We therefore treat Windows as first-class and keep all three platforms in the completion gate; remaining ambiguity is implementation sequencing, not scope.
- [NEEDS CLARIFICATION] Should the first execution slice after T008 target the daemon-control integration seam directly, or should we first stabilize the currently failing Goose e2e lane so feature verification can proceed without unrelated timeouts?
