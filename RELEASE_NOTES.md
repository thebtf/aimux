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
