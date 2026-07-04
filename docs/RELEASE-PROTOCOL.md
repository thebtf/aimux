# Release Protocol

## Applies When

This protocol applies to aimux versioned releases cut from the protected default branch (`master`) and published by pushing an annotated SemVer tag matching `v*`.

## Additional Release Surfaces

| Surface | Version source | Publish command | Verification |
| --- | --- | --- | --- |
| Git tag | Annotated `vX.Y.Z` tag | `git push origin master --follow-tags` or `git push origin vX.Y.Z` | `git ls-remote --tags origin refs/tags/vX.Y.Z` |
| GitHub Release + archives | `.github/workflows/release.yml` and `.goreleaser.yaml` | Tag-triggered GitHub Actions Release workflow | `gh release view vX.Y.Z`, release workflow success, checksums present |
| Binary version string | GoReleaser/build ldflags into `github.com/thebtf/aimux/pkg/build` | GoReleaser builds on tag | Release artifacts are produced from the tag SHA; local build smoke may use `scripts/build.ps1`/`scripts/build.sh` |

## Required Gates

| Gate | Command / evidence | Blocks release when |
| --- | --- | --- |
| Release Git Readiness | `git fetch --prune origin`; default branch is `master`; local `master` clean and synchronized with `origin/master`; only one registered worktree or all stale worktrees cleaned/preserved | Dirty/default-branch drift, unpreserved stale worktree/branch, or unreached release SHA |
| PR / branch review evidence | For non-trivial changes, merged PR with green CI/Security or equivalent protected-branch evidence | Missing review/CI evidence for release commits |
| Local build | `go build ./...` | Non-zero exit |
| Local full test suite | `go test ./... -count=1 -timeout 120s` | Non-zero exit |
| Vet | `go vet ./...` | Non-zero exit |
| Module verification | `go mod verify` | Non-zero exit |
| Critical suite | `go test ./tests/critical -count=1 -timeout 300s` | Non-zero exit |
| Stall-detection playbook | `go test ./pkg/server -run 'TestCritical_StallDetection_' -count=1 -timeout 120s` after confirming matching tests exist | Missing matching tests or non-zero exit |
| AIMUX-21 deterministic product gate | `AIMUX21_E2E=1 go test ./test/e2e -run 'TestE2E_(AIMUX21|ReviewEntry|TaskRouter|Resume)' -count=1 -timeout 600s` | Missing matching tests or non-zero exit |
| Loom module tests | `go test ./... -count=1` from `loom/` | Non-zero exit |
| Vulnerability scan | `govulncheck ./...` | Non-zero exit when the tool is available for release gate |
| Release workflow | Tag-triggered `.github/workflows/release.yml` | Workflow failure or missing GitHub release/artifacts |

## Release Autonomy

| Mutation class | Autonomy | Approval trigger | Evidence |
| --- | --- | --- | --- |
| Local release prep commit | automatic | sensitive content, incoherent dirty state, or unrelated files | `git diff`, gate output, release-owned file list |
| PATCH/MINOR version and tag from clean `master` | automatic under the active roadmap/release goal after gates pass | MAJOR/breaking change, ambiguous version, tag collision, failed/missing gate | SemVer calculation, release report, remote tag verification |
| GitHub Release publication via tag workflow | automatic for PATCH/MINOR private aimux releases after gates pass | marketplace/customer production publication outside this workflow, failed release workflow, missing credentials | `gh run view`, `gh release view`, artifact/checksum evidence |
| Destructive cleanup | approval-required unless preserve-first evidence proves a stale local worktree/branch and the command is scoped to this repository | dirty/unique/ambiguous worktree, outside-workspace filesystem deletion | release-git inventory and cleanup log |

Default autonomy: `auto_private_patch_minor` for PATCH/MINOR aimux releases from clean synchronized `master` with green required gates. MAJOR or breaking releases require explicit operator approval.

## Version Alignment

Aimux does not maintain a committed VERSION file. The release tag is the authoritative version source. GoReleaser injects `.Version`, `.ShortCommit`, and `.Date` into `pkg/build`; local scripts mirror the same ldflags using `git describe`.

## Release Notes

Update `CHANGELOG.md` and `RELEASE_NOTES.md` before tagging. Changelog entries use Keep a Changelog sections; release notes are operator/user-facing and include gate evidence plus upgrade notes.

## Publish / Smoke / Handoff

1. Create a release prep commit containing only release-owned docs/version artifacts.
2. Create an annotated tag `vX.Y.Z` on `master`.
3. Push `master` and the tag with `--follow-tags` or push the tag explicitly.
4. Watch `.github/workflows/release.yml` to completion.
5. Verify the remote tag and GitHub Release exist, and that archives/checksums are attached.
6. Record manual consumer update boundaries separately; release does not mutate real consumer homes.

## Terminal Verdict

- `PROJECT_RELEASE_PROTOCOL_PASS`: all required gates and release surfaces above have current evidence.
- `PROJECT_RELEASE_PROTOCOL_BLOCKED`: any required gate, tag, workflow, artifact, or release-note surface is missing, stale, failed, or cannot be verified.
- `PROJECT_RELEASE_PROTOCOL_DRY_RUN`: intended actions are described and no release mutation was performed.
