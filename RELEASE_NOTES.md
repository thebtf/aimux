## v5.21.1 — Warm fallback picker health caches

This patch release ships the AIMUX-3 CR-003 startup-prewarm closeout: the task fallback picker now warms its health caches at construction time, so the first fallback decision no longer depends on a cold binary probe path. The change stays scoped to the existing fallback picker wiring and does not alter the public MCP surface.

### Highlights

- `buildFallbackPicker` now warms both the primary picker and fallback orderer health caches immediately after constructing their health checkers.
- Added a focused regression test that removes fake CLI binaries after construction and proves warmed cache behavior through both `PickPair` and `RunPrimary`.
- The shipped diff stays limited to the two reviewed files from AIMUX-3 CR-003: `pkg/server/task_tool.go` and `pkg/server/task_tool_test.go`.

### Compatibility

No migration is required. The live MCP tool surface remains unchanged, and the release tightens fallback startup behavior behind the current task/runtime path rather than introducing new public tools or config.

### Verification

- PR #189 merged into `master` as `b1b4b204d99c6babcf4a9fc36ea1010761fb3493` after approved review evidence plus green CI and Security checks on the reviewed diff.
- Local release gates are being run from clean `master` for the `v5.21.1` tag candidate before publication.
- The pre-release housekeeping pass removed the clean, already-absorbed AIMUX-3 linked worktree so release git readiness now evaluates the default branch from one active checkout.

### Notes

This is a PATCH release from `v5.21.0` because the shipped delta is a scoped fallback-startup fix plus regression coverage, not a new public capability. Manual consumer updates remain out of scope for this release flow.
