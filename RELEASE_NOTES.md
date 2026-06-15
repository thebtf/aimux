## v5.16.3 — Direct staged writes for release zip upgrades

Patch release superseding `v5.16.2` for the GitHub release zip upgrade path.
`v5.16.2` fixed archive member selection, but post-release smoke still exposed
Windows rename failures while staging the downloaded PE payload.

### Fixed

- Remote `upgrade(action="apply")` now keeps `go-selfupdate` release detection,
  checksum validation, and archive member matching, but no longer uses its
  self-replacement writer for generated staged update paths.
- The updater now downloads the published archive, validates it against
  `checksums.txt`, extracts the canonical `aimux` / `aimux.exe` member, and
  writes the executable bytes directly to the unique staged `.bin` target used
  by the upgrade coordinator.
- Added regression coverage for the production-shaped path: HTTP asset download,
  checksum validation, zip member `bin/aimux.exe`, and generated staged target.

### Verification

- `go build ./...`
- `go test ./... -count=1 -timeout 300s`
- `go vet ./...`
- `go mod verify`
- `go test ./tests/critical/... -count=1 -timeout 300s`
- `go test ./... -count=1` from `D:\Dev\aimux\loom`
- `govulncheck ./...`
- `go test ./pkg/updater -run "TestDownloadReleaseBinaryExtractsCanonicalArchiveBinaryToGeneratedTarget|TestDownload_MockUpdateExtractsReleaseBinaryToGeneratedWindowsTarget|TestDownload_MockUpdateRejectsUnexpectedSingleEntryZip" -count=1`
- `go test ./pkg/updater -count=1`
- `go test ./pkg/upgrade -count=1`
- Candidate remote-zip smoke reached `status: "updated_deferred"` from
  `upgrade(action="apply", force=true)` against the published `v5.16.2` zip,
  proving download/checksum/extract/stage no longer fails on generated staged
  names.

### Notes

- `mcp-launcher -mode install` currently requires `-source`, so final release
  validation uses two gates: remote zip apply for #277's archive/staging path
  and install-mode smoke for post-exit lifecycle/reconnect.
- Issue #277 should be closed only after the published `v5.16.3` artifact
  passes the same released-artifact gates.
- Issue #283 remains a separate native status/update evidence-contract follow-up.
