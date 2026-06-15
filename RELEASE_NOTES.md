## v5.16.1 — Windows installed post-exit upgrade recovery

Patch release for the Windows installed-daemon update path after `v5.16.0`
published remotely but failed the local installed smoke gate.

### Fixed

- Daemon startup now wires muxcore `SessionHandler` update-pending shutdown to
  the process cancel function, so the old daemon can exit after the final
  project disconnect.
- The Windows post-exit installer now runs from a distinct helper executable
  rather than from the staged payload path.
- The helper writes through cache/temp/cwd helper directories when creating its
  executable copy, avoiding observed `Access is denied` failures beside staged
  PE payloads.
- The helper installs by moving the already staged payload into `current.exe`
  instead of copying/restaging it again.

### Verification

- `go build ./...`
- `go test ./pkg/upgrade -run "PostExit|MoveStaged" -count=1`
- `go test ./pkg/server -run "UpdatePendingCancels|Session" -count=1`
- `go test ./pkg/upgrade -count=1`
- `go test ./pkg/server -count=1`
- `go test ./... -count=1 -timeout 300s`
- Isolated installed Windows smoke passed with delayed reconnect:

```powershell
D:\Dev\mcp-launcher\mcp-launcher.exe `
  -binary D:\tmp\aimux-postexit-current.exe `
  -cwd D:\Dev\aimux `
  -env-mode clean `
  -mode install `
  -source D:\tmp\aimux-postexit-next.exe `
  -force `
  -expect-tools 28 `
  -expect-version 5.16.1-smoke-next `
  -timeout 90 `
  -reconnect-delay 15 `
  -cleanup-binary-processes
```

### Notes

- `mcp-launcher` default install reconnect is still too early for Windows
  post-exit replacement; Engram #278 tracks making that gate adaptive. Until
  then, use `-reconnect-delay 15` for installed Windows smoke evidence.
- A daemon already running the broken `v5.16.0` installed path may need a
  one-time process stop before it can take `v5.16.1`. The missing cancel wiring
  is in the old daemon process; after `v5.16.1` is running, future post-exit
  installs use the fixed lifecycle.
- A real `bin\aimux.exe` smoke can be contaminated by live local clients using
  the same executable path. Use a unique installed smoke binary for release
  evidence, or stop all same-path clients before validating the real installed
  name.
