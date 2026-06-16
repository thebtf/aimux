## v5.17.0 — muxcore registry descriptors and legacy install recovery

This release ships the muxcore registry descriptor adoption work and closes the
Windows installed-update compatibility gap exposed by the `mcp-launcher`
source-side smoke.

### Added

- Aimux now advertises the muxcore registry descriptor contract with product
  metadata and owner-list capability declarations.
- Windows post-exit install helpers can write opt-in diagnostic traces through
  `AIMUX_POST_EXIT_TRACE`, making detached helper failures inspectable during
  release/debug smokes.

### Fixed

- Upgrades from historical `5.16.1-bin-current` style launchers now work again.
  Aimux bootstraps the missing `.post-exit-active` generation marker when a
  legacy helper copy is launched as `<staged>.post-exit-helper.*.exe`.
- The post-exit installer now treats staged replacement image locks as bounded
  retryable conditions, matching current-binary and old-slot lock handling.
- Aimux now consumes muxcore `v0.26.2` for the registry descriptor contract.

### Verification

- `go test .\pkg\upgrade -count=1`
- `go build ./...`
- `go test ./... -count=1`
- Historical installed-daemon smoke passed with the local patched
  `mcp-launcher`:

```text
current: 5.16.1-bin-current
next:    5.16.4-3-gb950218-dirty
installed binary changed: cb4ae8081b64 -> 6a0938500fd6
verified server version: 5.16.4-3-gb950218-dirty
aimux://health.version: 5.16.4-3-gb950218-dirty
[install] PASS
```

### Notes

- The `mcp-launcher` clean-env/install harness fix remains owned by the
  separate `D:\Dev\mcp-launcher` project/session. This release proves the aimux
  side of the historical install hop using the local patched launcher.
