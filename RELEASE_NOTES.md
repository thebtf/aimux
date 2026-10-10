## v5.25.1 — release archive runtime configuration

This PATCH release fixes the published v5.25.0 Windows archive: its checksum and embedded version checks passed, but a clean MCP start failed with `failed to read config\\default.yaml` because GoReleaser omitted the required runtime `config/` tree.

### Fix

- Package the runtime configuration alongside the binary in release archives, preserving archive-relative paths so `config/` stays adjacent to the extracted executable.
- Resolve executable symlinks before locating adjacent configuration, so the documented Linux/macOS `~/.local/bin/aimux` link starts against `~/.local/lib/aimux/config`.

### Upgrade

Download the v5.25.1 archive and extract its full contents. On Windows, extract the complete ZIP into `$env:LOCALAPPDATA\\aimux`. On Linux/macOS, extract the complete tarball into `~/.local/lib/aimux` and use `~/.local/bin/aimux` as a symlink to `~/.local/lib/aimux/aimux`; do not extract only the binary. This preserves the required adjacent `config/` tree. v5.25.0 history and its existing feature behavior are unchanged.

## v5.25.0 — thinking-only MCP mode

This MINOR release adds an operator-selectable restricted MCP surface for periods when Aimux task/session orchestration should be unavailable but the in-process reasoning patterns must remain usable.

### Highlights

- Set `AIMUX_MCP_THINKING_ONLY=1` before daemon startup to expose exactly the 22 low-level cognitive move tools.
- The restricted mode does not advertise MCP Tasks, Resources, or Prompts and does not register `think`, `task`, `review`, `status`, `sessions`, `deepresearch`, `upgrade`, built-in skill prompts, or recipe resources.
- Thinking-only mode starts successfully without an external AI CLI on `PATH`; unset or non-`1` values retain the existing 29-tool surface and CLI requirement.
- Upgraded muxcore to `v0.28.0` and tightened daemon readiness around canonical muxcore health and cancellation.
- Raised the minimum build toolchain to Go `1.26.9`, with matching CI, security, release, loomlint, mutation, Docker, source-build, contributor, and production-testing pins.
- Upgraded `golang.org/x/net` to `v0.60.0` and `google.golang.org/grpc` to `v1.83.2`, including the compatible transitive versions selected by the Go module solver.

### Compatibility

The feature is opt-in and reversible. Existing MCP clients see no change unless the environment key is exactly `1`. Changing the key requires a daemon restart and MCP reconnect. In thinking-only mode, startup may proceed without an external CLI and the MCP registration/handshake surface is reduced; Loom tasks, session storage, tenant data, provider profiles, and routing state are not migrated or rewritten.

### Review and verification

- PR [#202](https://github.com/thebtf/aimux/pull/202) merged the restricted MCP surface as `c6e21c2309c0cc11be4f749b7bcde1620a8a047d` after cross-platform CI, security scanning, built-binary 22/29-tool smoke checks, and independent review.
- PR [#203](https://github.com/thebtf/aimux/pull/203) merged the CLI-less startup correction as `4f05b8935c4a412274fdd149402270ffe7fbd61b` after Linux, macOS, and Windows CI, `govulncheck`, CodeRabbit approval, local full-suite/build/vet checks, and a built-binary smoke with external CLI discovery removed from `PATH`.
- The shared E2E initialize helper now handles only the exact transient `-32603 project not connected: <project ID>` response under one common deadline; other initialization failures remain immediate.
- The fresh-clone pre-release scan of the binary built with Go `1.25.11` reported 21 callable vulnerabilities. This security repair adopts the scan's minimum fixed Go, x/net, and gRPC versions without weakening the hard `govulncheck -mode=binary` release gate. Source-mode scanning remains a soft dependency-hygiene signal.

### Upgrade

After the GitHub release publishes the archives and checksums, operators can use `upgrade(action="apply")` or install the matching archive. Set the environment key in the MCP server configuration, then restart the daemon and reconnect the client before checking for the 22-tool surface.

## v5.24.1 — muxcore v0.27.1 handoff adaptation

This PATCH release adopts the released muxcore `v0.27.1` protocol-v2 handoff contract without changing Aimux's persistent daemon/shim lifecycle policy.

### Highlights

- Upgraded `github.com/thebtf/mcp-mux/muxcore` from `v0.26.13` to `v0.27.1` (`5f0ebb3c6c51cd6345b21bdf8ef0d0b21dc89338`).
- Carried stderr and Windows authority handles through the direct successor-handoff fixture.
- Closed every relayed stdin/stdout/stderr/authority handle once per unique value across the complete upstream set.
- Ignored both zero and `INVALID_HANDLE_VALUE` sentinels during cleanup.
- Updated the release toolchain to Go `1.25.12` and `google.golang.org/grpc` to `v1.79.3`; the hard vulnerability gate now scans the built release binary.

### Compatibility

The stateful Aimux daemon and per-host stdio shim remain `Persistent: true`. `OnInject`, tenant/session authorization, the active-engine pointer, and `RestartWithSuccessor` remain on their existing paths. No private dormant frame or second updater was added.

Provider issue [mcp-mux#140](https://github.com/thebtf/mcp-mux/issues/140) remains the stopline for a reusable stdio-retaining supervisor, capability negotiation, and exact park/wake behavior. This release therefore does not claim that abandoned host pipes converge to zero shim processes.

### Review and verification

- PR [#196](https://github.com/thebtf/aimux/pull/196) merged into `master` after CodeRabbit approval, a resolved invalid-handle review finding, and green cross-platform CI.
- Local build, vet, full-suite, critical-suite, stall-detection, deterministic product-gate, Loom-module, active-pointer successor, and customer-mode stdio checks passed on the release candidate.
- Clean-cache module verification passed.
- Artifact-mode `govulncheck` passes on the built Aimux binary. The soft source scan still reports `GO-2026-5932` because `go-selfupdate` compiles optional legacy OpenPGP support, but those symbols are not linked into the shipped Aimux binary.

### Upgrade

Authorized operators can use the public `upgrade(action="apply")` path after the GitHub release workflow publishes the archives and `checksums.txt`. Supported modes are `auto`, `hot_swap`, and `deferred`; the current muxcore `SessionHandler` topology reports truthful deferred fallback when live handoff is unavailable.

## v5.24.0 — AIMUX-9 review entry facade

This MINOR release ships AIMUX-9 CR-006 as the first dedicated methodology-bearing public entry after the Layer 5 purge. Aimux now exposes a literal `review` MCP tool for standard and gate-oriented code review while keeping the same task/Loom/runtime-events evidence plane underneath.

### Highlights

- Added a dedicated public `review` facade instead of forcing callers to express all review work through generic `task(...)` calls.
- Preserved the existing async task/result/runtime contract: `review` stays a thin facade over the current task/Loom backbone rather than creating a second execution surface.
- Kept `second-opinion` and workflow-backed review-class recipes off the public `review` facade; they remain on `task(recipe_id=...)`.
- Corrected handshake, prompt, and customer-mode playbook guidance so the 29-tool public surface and compiled caller-guide resources are described truthfully.

### Compatibility

The release is additive for callers that want direct review entry. Existing `task(task_class="review", ...)` and `task(recipe_id=...)` paths remain valid. The only intentional boundary tightening is that `second-opinion` is not part of the public `review` facade and continues to live on `task(recipe_id="second-opinion")`.

### Verification

- PR #193 merged the initial review-facade slice into `master` as `7914dc8f6d8b4ef2b0fb802d418db65d16288b56`.
- PR #194 merged the bounded truthfulness/playbook follow-up into `master` as `d5eee5905b6522d46929c913925857207db6a6da` after external PR-review evidence and resolved review threads.
- Local merged-`master` release gates passed: `go build ./...`, `go test ./... -count=1 -timeout 120s`, `go vet ./...`, `go test ./tests/critical -count=1 -timeout 300s`, `go test ./pkg/server -run "TestCritical_StallDetection_" -count=1 -timeout 120s`, `AIMUX21_E2E=1 go test ./test/e2e -run "TestE2E_(AIMUX21|ReviewEntry|TaskRouter|Resume)" -count=1 -timeout 600s`, and `go test ./... -count=1` from `loom/`.
- The `go mod verify` release gate passed in a disposable clean module cache with `GOMODCACHE=D:\tmp\aimux-go-mod-cache-cr006-release` and `GOCACHE=D:\tmp\aimux-go-build-cache-cr006-release` after `go mod download`.
- `govulncheck ./...` reported no vulnerabilities in aimux code paths.

### Notes

This release is cut as `v5.24.0` rather than a patch because CR-006 adds a new public MCP entry point (`review`) and raises the caller-visible tool inventory from 28 to 29. Manual consumer updates remain outside this release flow.

## v5.23.0 — AIMUX-23 structured runtime event slices

This MINOR release ships AIMUX-23 CR-011 as a caller-visible expansion of the
existing task inspectability surface. Aimux now exposes structured runtime-event
slices through the existing `aimux://tasks/{task_id}/events` resource, plus a
compact `aimux://tasks/{task_id}/progress` resource for polling progress
artifacts without reading the full event stream.

### Highlights

- Added bounded runtime-event paging for task inspection, including typed
  runtime slices via `kind`, `event_type`, and `channel` on
  `aimux://tasks/{task_id}/events`.
- Added a compact progress-only task resource on
  `aimux://tasks/{task_id}/progress` for lightweight polling consumers.
- Preserved the existing Loom-backed inspectability contract: runtime and
  progress evidence stays inside the current task resource/viewer surface rather
  than introducing a new MCP tool or ad hoc side channel.
- Closed the live PR blocker by enforcing resource-local `kind=` boundaries, so
  `/events` and `/progress` no longer leak cross-family artifacts.

### Compatibility

No migration is required for existing callers. The public MCP tool inventory is
unchanged: callers continue to use existing task submission and task-resource
surfaces. The new capability is additive through richer task inspection
resources rather than a new top-level tool.

### Verification

- PR #192 merged into `master` as `ce22bbcbd876f886e9bd8010cebcd53159f5cfc0`
  after the accepted seq37 PM blocker closeout plus green GitHub `CI`,
  `Security`, `loomlint`, and CodeRabbit status on head
  `eef69f49289895dedcee9366a180ceb0f41e6eb5`.
- Local merged-`master` release gates passed: `go build ./...`,
  `go test ./... -count=1 -timeout 120s`, `go vet ./...`,
  `go test ./tests/critical -count=1 -timeout 300s`,
  `go test ./pkg/server -run "TestCritical_StallDetection_" -count=1 -timeout 120s`,
  `AIMUX21_E2E=1 go test ./test/e2e -run "TestE2E_(AIMUX21|ReviewEntry|TaskRouter|Resume)" -count=1 -timeout 600s`,
  `go test ./... -count=1` from `loom/`, and `govulncheck ./...`.
- The `go mod verify` release gate was rechecked in a disposable clean module
  cache after the default user-level Go module cache proved locally modified
  outside the repo. With `GOMODCACHE=D:\tmp\aimux-go-mod-cache-cr011-release`
  and `GOCACHE=D:\tmp\aimux-go-build-cache-cr011-release`, both
  `go mod download` and `go mod verify` passed.

### Notes

This release is cut as `v5.23.0` rather than a patch because CR-011 adds new
caller-visible task inspection capabilities inside the existing task resource
surface. Manual consumer updates remain outside this release flow.

## v5.22.0 — AIMUX-23 workflow-backed curated recipes

This MINOR release ships AIMUX-23 CR-010 as a bounded expansion of the existing
`task(recipe_id=...)` surface. Aimux now exposes two compiled workflow-backed
recipe IDs, `security-audit` and `debug-investigation`, while keeping
`pkg/workflow` and `pkg/dialogue` internal code-behind rather than restoring
the removed public workflow/dialog/debate tool family.

### Highlights

- Added `security-audit` and `debug-investigation` as discoverable, read-only
  curated recipes backed by the internal workflow engine.
- Preserved the existing AIMUX-23 inspectability contract: workflow-backed
  recipes still produce Loom-backed task state, replay metadata, task/resource
  visibility, and caller-guide discovery instead of a parallel ad hoc surface.
- Kept `code-review` and `second-opinion` on their current paths while extending
  the registry, router, worker, and resource layers around the new internal
  workflow-backed lane.
- Closed the live review blocker set before merge by enforcing read-only support
  before fallback-selected CLI execution and by making the debug root-cause gate
  source-truthful and identifier-safe.

### Compatibility

No migration is required for existing callers. The public MCP tool inventory is
unchanged: callers continue to use `task(recipe_id=...)`, existing resources,
and existing viewer/replay surfaces. The new capability is additive through
new supported recipe IDs rather than a new top-level tool or DSL surface.

### Verification

- PR #191 merged into `master` as `bd65fd7045c47cd28df49e22834d91f3a4525dcf`
  after the accepted seq34 PM review closeout plus green GitHub `CI`,
  `Security`, and CodeRabbit status on head
  `e14a054c90e51aa33866e6a18d71875866f169d2`.
- Local merged-`master` release gates passed: `go build ./...`,
  `go test ./... -count=1 -timeout 120s`, `go vet ./...`,
  `go test ./tests/critical -count=1 -timeout 300s`,
  `go test ./pkg/server -run "TestCritical_StallDetection_" -count=1 -timeout 120s`,
  `AIMUX21_E2E=1 go test ./test/e2e -run "TestE2E_(AIMUX21|ReviewEntry|TaskRouter|Resume)" -count=1 -timeout 600s`,
  `go test ./... -count=1` from `loom/`, and `govulncheck ./...`.
- The `go mod verify` release gate was rechecked in a disposable clean module
  cache after the default user-level Go module cache proved locally modified
  outside the repo. With `GOMODCACHE=D:\tmp\aimux-go-mod-cache-cr010-release`
  and `GOCACHE=D:\tmp\aimux-go-build-cache-cr010-release`, both
  `go mod download` and `go mod verify` passed.

### Notes

This release is cut as `v5.22.0` rather than a patch because CR-010 adds new
caller-visible recipe capabilities within the existing `task` surface. Manual
consumer updates remain outside this release flow.

## v5.21.2 — AIMUX-4 fallback overhead closure

This patch release closes the remaining AIMUX-4 CR-003 residual lane from the shipped `v5.21.1` baseline. The shipped change is intentionally test-only: it adds quantitative benchmark proof that fallback retry decision overhead and orderer ranking overhead stay far below the `<10ms` NFR without real CLI execution, while preserving the current production fallback behavior and MCP surface.

### Highlights

- Added `BenchmarkFallbackPicker_DecisionOverhead`, covering an eligible primary failure followed by fake in-memory fallback success through `RunPrimary` without spawning any CLI process.
- Added `BenchmarkOrderer_RankDecisionOverhead`, covering ranking over healthy fake CLIs, in-memory score data, and attempted-CLI exclusion.
- Kept the shipped diff bounded to the reviewed AIMUX-4 CR-003 test surface: `pkg/executor/fallback/fallback_test.go` and `pkg/executor/fallback/orderer_test.go`.

### Compatibility

No migration is required. This release does not alter production fallback code, public MCP tools, or runtime configuration. It ships verification coverage for the already-release-contained fallback path rather than a new outward-facing capability.

### Verification

- PR #190 merged into `master` as `b774179d9f8e248324d7497b95a814ce31bd9314` after external PR review evidence, resolved discussion threads, and green GitHub `CI` / `Security` runs on the PR head.
- Local release gates on the merged commit passed: `go build ./...`, `go vet ./...`, `govulncheck ./...`, `go test ./tests/critical -count=1 -timeout 300s`, `go test ./pkg/server -run "TestCritical_StallDetection_" -count=1 -timeout 120s`, `AIMUX21_E2E=1 go test ./test/e2e -run "TestE2E_(AIMUX21|ReviewEntry|TaskRouter|Resume)" -count=1 -timeout 600s`, `go test ./... -count=1` from `loom/`, and the AIMUX-4 benchmark gate `go test ./pkg/executor/fallback -run '^$' -bench "Benchmark.*Fallback|Benchmark.*Orderer" -benchtime=100x`.
- Full-suite verification passed on the merged slice; earlier intermittent `pkg/server` P26 lock failures were reproduced as flaky/unrelated to the bounded AIMUX-4 diff and cleared on focused reruns plus a clean full-suite rerun.

### Notes

This is a PATCH release from `v5.21.1` because the shipped delta closes a residual verification gap without adding a new public capability. Manual consumer updates remain out of scope for this release flow.
