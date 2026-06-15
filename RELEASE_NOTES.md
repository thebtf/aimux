## v5.16.4 — Native muxcore status and update proof

Patch release for the aimux-owned muxcore hot-update contract tracked by
Engram #283.

### Added

- `sessions(action="health")` and `aimux://health` now expose native muxcore
  status evidence: `engine_name`, daemon/owner generation fields, owner restore
  source, handoff counters, and shim reconnect counters.
- `upgrade(action="apply")` responses now include `update_method` and
  `update_topology`, making the update path explicit for graceful restart,
  fallback shutdown, and Windows post-exit recovery flows.
- Added a Windows customer-mode e2e smoke that starts an old installed daemon
  session, applies a local-source update through aimux, proves the already-open
  MCP session can make a post-update health request, then proves a fresh session
  reports the replacement version.

### Documentation

- Updated the production testing playbook so `mcp-launcher -mode install`
  remains the fresh reconnect gate, while the same-session old-client proof is
  covered by the normal MCP-host e2e gate or an equivalent client that keeps the
  connection open.

### Verification

- `go build ./...`
- `go test ./... -count=1 -timeout 300s`
- `go vet ./...`
- `go mod verify`
- `go test ./tests/critical -count=1 -timeout 300s`
- `go test ./test/e2e -run 'TestE2E_Upgrade_(Fallback_InvalidModeRejectedButDaemonLives|OldSessionRequestThenFreshSessionNewVersion)' -count=1 -timeout 240s`
- `go test ./test/e2e -run 'TestE2E_(AIMUX21|ReviewEntry|TaskRouter|Resume)' -count=1 -timeout 600s`
- `go test ./... -count=1` from `loom`
- `govulncheck ./...`

### Notes

- Windows SessionHandler deployments still report deferred/post-exit semantics
  truthfully. This release makes that topology explicit in the update payload
  instead of treating a successful deferred path like a silent live handoff.
- Engram #283 should be closed only after the published artifact passes the
  released-artifact installed-daemon smoke.
