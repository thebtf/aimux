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
