## v5.21.0 — Codex runtime profile startup isolation

This release ships AIMUX-20: Codex-backed startup now uses a stable project-scoped runtime home, so app-server launches stop inheriting ambient global Codex state while auth, config, and persistent state continue to work inside the virtual home. The same release also carries the AIMUX-20 safety amendment that blocks clear cross-product `mcp-mux` -> `aimux-stage-*` active-pointer writes during upgrade flows.

### Highlights

- Codex app-server startup now derives and applies a stable project-scoped `CODEX_HOME` instead of relying on the caller's global Codex home.
- Project virtual homes preserve auth, config, and persistent state, so isolated startup no longer drops those capabilities.
- Upgrade fallback tests now quarantine inherited `MCPMUX_*` / `MCP_MUX_*` environment values and preserve only explicit test-owned overrides.
- The active-pointer write path now rejects clear cross-product `mcp-mux` -> `aimux-stage-*` successor writes before touching the live pointer.

### Compatibility

No MCP-client migration is required. Existing `aimux` server registration stays the same, and the live MCP tool surface remains 28 tools. The release changes Codex startup-state isolation behind the current task/runtime path rather than adding a new public MCP entry point.

### Verification

- PR #188 merged into `master` at `ac7d4b7930d132b4a2d6bc95467200e71a44c603` after green CI, Security, and CodeRabbit status on the release-bearing diff.
- Local release-readiness gates passed on clean `master`: `go build ./...`, clean-cache `go test ./... -count=1 -timeout 120s`, `go vet ./...`, clean-cache `go mod verify`, `go test ./tests/critical -count=1 -timeout 300s`, `go test ./pkg/server -run 'TestCritical_StallDetection_' -count=1 -timeout 120s`, `AIMUX21_E2E=1 go test ./test/e2e -run 'TestE2E_(AIMUX21|ReviewEntry|TaskRouter|Resume)' -count=1 -timeout 600s`, `go test ./... -count=1` from `loom/`, and `govulncheck ./...`.
- Seq19 safety review confirmed the cross-product pointer guard and muxcore-env quarantine without touching external pointers, user homes, plugin caches, or sibling repositories.

### Notes

This release advances from the already-published `v5.20.0` to the next available minor release because the shipped delta contains a new Codex runtime-profile capability (`feat:`) plus follow-up fixes. Manual consumer updates remain out of scope for this release flow.
