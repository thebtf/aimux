## v5.16.2 — Loom startup recovery and release zip upgrades

Patch release for the installed-daemon release path after `v5.16.1`. This
release keeps Loom-backed task routing recoverable after transient SQLite
startup locks and fixes the GitHub release zip upgrade path that blocked the
published-artifact smoke for issue #277.

### Fixed

- Daemons that temporarily fall back to memory-only startup because SQLite is
  locked now retry the configured SQLite store later from `sessions(action="health")`
  and `task` routing, then initialize Loom instead of staying permanently at
  `loom_status: "unavailable"`.
- Installed smoke gates now fail on post-reconnect Loom health regressions and
  task-routing `CapabilityMismatch` states rather than accepting a daemon where
  only non-Loom tools respond.
- Remote `upgrade(action="apply")` now accepts GitHub release archives that
  contain canonical binary names such as `aimux.exe` or `aimux`, even when the
  install coordinator stages to a generated `.aimux-update-*` path.

### Verification

- `go build ./...`
- `go test ./... -count=1 -timeout 300s`
- `go vet ./...`
- `go mod verify`
- `go test ./tests/critical/... -count=1 -timeout 300s`
- `go test ./... -count=1` from `D:\Dev\aimux\loom`
- `govulncheck ./...`

### Notes

- Issue #277 should stay open until the `v5.16.2` GitHub Release asset is
  published and a released Windows artifact install smoke proves the zip upgrade
  path end to end.
- Issue #283 remains a follow-up evidence-contract/debug track and is not
  bundled into this patch release.
