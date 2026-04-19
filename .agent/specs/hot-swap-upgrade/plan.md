# Implementation Plan: Transparent Hot-Swap Upgrade

**Spec:** .agent/specs/hot-swap-upgrade/spec.md
**Created:** 2026-04-19
**Status:** Draft

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Binary swap | `muxcore/upgrade.Swap()` | Already shipped, Windows-safe, tested in muxcore suite |
| FD handoff | `muxcore/daemon.performHandoff()` | SCM_RIGHTS/DuplicateHandle abstracted, token auth built-in |
| Release download | `creativeprojects/go-selfupdate` (keep) | Already used, checksum validator integrated |
| Token | `crypto/rand` stdlib | 32 bytes hex; no new deps |
| Process spawn | `os/exec.Cmd` stdlib | Standard Go primitive |

## Architecture

### Current flow (v4.3.0)

```
handleUpgrade("apply")
  → updater.ApplyUpdate (download + checksum + os.Rename)
  → sessionHandler.SetUpdatePending()
  → return "will restart when all CC disconnect"
[... days later, user closes all CC ...]
daemon exits → mcp-mux respawns → new binary loads
```

### New flow (v4.4.0)

```
handleUpgrade("apply", mode="auto")
  1. updater.CheckUpdate  (version detection, same as today)
  2. detect engine mode:
     - NOT engine → fall through to v4.3.0 deferred path (FR-6)
     - engine → continue
  3. download new binary to temp path (updater.DownloadToPath — new helper)
  4. verify checksum via updater.VerifyChecksum (extracted from ApplyUpdate)
  5. generate token: crypto/rand 32 bytes → hex
  6. muxcore.upgrade.Swap(currentExe, tempPath) → binary on disk replaced
     - on failure → fallback per FR-6
  7. spawn successor: os/exec.Command(currentExe, "--handoff-from", sockPath, "--handoff-token", token)
     - inherit env/cwd per C3
     - on failure → fallback per FR-6
  8. muxcore.daemon.performHandoff(successorAddr, token, upstreams, ctxWithTimeout(15s))
     - on failure → predecessor keeps running, return error → fallback per FR-6
  9. on success: HandoffResult.Phase == "complete", all IDs in Transferred
     - stop accepting new conns (sessionHandler.SetDraining)
     - drain in-flight (max 30s)
     - os.Exit(0)
  10. return hot-swap response envelope (FR-7) BEFORE exit (MCP response must land)
```

### Component diagram

```
           MCP call
              │
              ▼
   ┌──────────────────────┐
   │  handleUpgrade       │
   │  (pkg/server/...)    │
   └──────────┬───────────┘
              │
              ▼
   ┌──────────────────────┐
   │  upgrade.Coordinator │  ← NEW: new file pkg/upgrade/coordinator.go
   │  - TryHotSwap        │    owns the full flow + fallback logic
   │  - FallbackDeferred  │
   └──────────┬───────────┘
              │
      ┌───────┼───────┐
      ▼       ▼       ▼
   updater  muxcore  os/exec
   (check,  (Swap +  (spawn
   download  Handoff) successor)
   verify)
```

### Reversibility

| Decision | Tag | Rollback |
|----------|-----|----------|
| New file `pkg/upgrade/coordinator.go` | REVERSIBLE | `git revert` |
| `handleUpgrade` behavior change | REVERSIBLE | Fallback path preserves v4.3.0 semantics completely |
| `mode` parameter added to upgrade tool | REVERSIBLE (additive) | Default `auto` — omitted param = `auto` = graceful |
| Successor-mode CLI flags (`--handoff-from`, `--handoff-token`) | PARTIALLY REVERSIBLE | Flags become part of binary surface — to remove cleanly requires v4.5.0 migration |

## Data Model

Tool request extension (backwards-compatible):

```go
// In pkg/server/server.go — upgrade tool schema
{
  "action": "apply",
  "mode": "auto"         // NEW, optional; enum: "auto" | "hot_swap" | "deferred"
}
```

Response envelope extensions (FR-7 above in spec).

## API Contracts

### Internal contract: `upgrade.Coordinator`

```go
// pkg/upgrade/coordinator.go (NEW)

type Mode string
const (
  ModeAuto     Mode = "auto"      // try hot-swap, fall back to deferred
  ModeHotSwap  Mode = "hot_swap"  // hot-swap only, hard fail on error
  ModeDeferred Mode = "deferred"  // skip hot-swap, always deferred
)

type Coordinator struct {
  Version        string              // current version
  BinaryPath     string              // absolute path to current executable
  SessionHandler sessionHandlerIface // for SetUpdatePending + SetDraining
  EngineMode     bool                // false → hot-swap disabled
  Logger         *logger.Logger
}

type Result struct {
  Method             string   // "hot_swap" | "deferred" | "up_to_date"
  PreviousVersion    string
  NewVersion         string
  HandoffTransferred []string // populated on hot-swap success
  HandoffDurationMs  int64    // populated on hot-swap success
  HandoffError       string   // populated on deferred-with-error
  Message            string
}

// Apply downloads, verifies, and applies the upgrade according to mode.
// Returns Result describing what happened. Error returned only on catastrophic
// failure (e.g., checksum mismatch — never applied) — all other paths produce
// a valid Result with Method=deferred and HandoffError populated.
func (c *Coordinator) Apply(ctx context.Context, mode Mode) (*Result, error)
```

### External contract: MCP tool `upgrade`

```json
{
  "name": "upgrade",
  "inputSchema": {
    "type": "object",
    "properties": {
      "action": { "type": "string", "enum": ["check", "apply"] },
      "mode":   { "type": "string", "enum": ["auto", "hot_swap", "deferred"], "default": "auto" },
      "include_content": { "type": "boolean", "default": false }
    },
    "required": ["action"]
  }
}
```

### Successor daemon flags

```
aimux --handoff-from <unix-socket-path> --handoff-token <32-byte-hex> [existing flags]
```

On these flags, `cmd/aimux/main.go` runs a successor-mode bootstrap:
1. Connect to `--handoff-from` socket
2. Send handoff request with `--handoff-token`
3. Receive FDs via `RecvFDs`
4. Reconstruct session state
5. Start serving as normal engine daemon

Implementation belongs to muxcore — aimux just wires the CLI flag parsing.

## File Structure

```
pkg/
  upgrade/                         — NEW package
    coordinator.go                 — Coordinator + Apply + TryHotSwap + FallbackDeferred
    coordinator_test.go            — unit tests
    handoff.go                     — thin wrapper around muxcore handoff (isolates test mocks)
    handoff_test.go
  updater/
    updater.go                     — MODIFIED: split ApplyUpdate into DownloadToPath + VerifyChecksum + Install
  server/
    server.go                      — MODIFIED: handleUpgrade delegates to pkg/upgrade/Coordinator
cmd/
  aimux/
    main.go                        — MODIFIED: add --handoff-from + --handoff-token flag handling
test/
  e2e/
    upgrade_hot_swap_test.go       — NEW: end-to-end hot-swap test (mock release server)
    upgrade_fallback_test.go       — NEW: injected handoff failure → deferred fallback
```

## Phases

### Phase 1: Foundation — extract updater + skeleton Coordinator

- Split `pkg/updater/updater.go` — separate `Download`, `VerifyChecksum`, `Install` (the current monolithic `ApplyUpdate` covers all 3)
- Create `pkg/upgrade/` package with `Coordinator` type + skeleton methods
- Wire `handleUpgrade` to call `Coordinator.Apply` with current semantics preserved (delegate to updater — no behavior change yet)

Goal: zero behavior change, just structural preparation. Tests for updater split pass.

### Phase 2: Successor daemon mode

- Parse `--handoff-from` + `--handoff-token` flags in `cmd/aimux/main.go`
- Implement successor bootstrap: connect, request handoff, receive FDs via muxcore, start engine
- Unit test: flag parsing + token validation
- Integration test: spawn successor with test handoff server, verify successful handoff

Goal: new binary knows how to receive a handoff. Predecessor logic in Phase 3.

### Phase 3: Predecessor handoff flow

- Implement `Coordinator.TryHotSwap`: engine-mode detection → Swap → spawn successor → performHandoff → drain → exit
- Implement `Coordinator.FallbackDeferred`: call existing `SetUpdatePending` path
- Mode routing: `auto` → try hot-swap, fallback on error; `hot_swap` → hard fail; `deferred` → skip straight to fallback
- Integration test: end-to-end upgrade on a running daemon

Goal: hot-swap works on Linux/macOS. Windows may need DuplicateHandle follow-up.

### Phase 4: Cross-platform parity + polish

- Verify on Windows CI: muxcore DuplicateHandle should already work, but e2e test must prove it
- Handle edge cases from spec: multiple concurrent upgrade calls, mid-handoff connect, disk full
- Structured log lines per US3 / FR-3
- Documentation: README/CHANGELOG updates for v4.4.0
- Version bump: 4.3.0 → 4.4.0 in `pkg/server/server.go`

## Library Decisions

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Binary Swap | `muxcore/upgrade.Swap` | Already shipped, Windows-safe, tested |
| FD Handoff | `muxcore/daemon.performHandoff` | Cross-platform abstraction with token auth |
| Download/checksum | `creativeprojects/go-selfupdate` (keep) | Zero migration cost, already integrated |
| Token gen | `crypto/rand` stdlib | No new deps |
| Process spawn | `os/exec` stdlib | No new deps |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|---------------------|
| muxcore `performHandoff` API completeness for aimux's multi-project socket setup | HIGH | Phase 2 integration test will stress this; expect minor muxcore patches |
| Windows successor spawn — does `exec.Command` from a renamed running exe work? | MEDIUM | Phase 4 Windows CI validation. Fallback: muxcore `upgrade.Swap` already handles this via rename trick — the new binary is at the original path by the time spawn runs. |
| In-flight async jobs survival across handoff | MEDIUM | Phase 3 integration test: start job, upgrade, poll job. If muxcore handoff doesn't carry in-flight state, fall back to waiting for drain. |
| `mcp-mux` (aimux consumer side) — does it recognize the successor daemon? | MEDIUM | The successor keeps the same control socket path; mcp-mux should see it as the same daemon. Phase 3 test validates. |

## Constitution Compliance

- **Multi-user**: FR-9 (cross-platform parity) + NFR-5 (socket permissions) align with constitution's multi-user assumption.
- **Gradual, not rushed**: 4 phases, each independently testable. No "big bang" rewrite.
- **Correct-engineering**: Wraps existing muxcore primitives; does not reinvent handoff protocol.
- **Reversibility**: All decisions tagged; fallback preserves v4.3.0 behavior for any error class.
