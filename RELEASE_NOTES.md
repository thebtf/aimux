## v5.16.0 — AIMUX-23 curated recipes, replay, and caller guide

### Curated Recipes

- aimux now exposes a compiled recipe catalog through MCP resources:
  - `aimux://recipes`
  - `aimux://recipes/{recipe_id}`
- The initial read-only recipe IDs are `code-review` and `second-opinion`.
- Recipes run through the existing `task` tool with `recipe_id`; no new
  workflow tool, JavaScript/YAML runner, or plugin DSL is introduced.

### Provider Safety

- Recipe policy is checked before Loom submission. If the selected provider
  cannot enforce the recipe policy, `task` fails closed with
  `CapabilityMismatch` and no worker is spawned.
- Capability mismatch payloads include the recipe ID, selected CLI, requested
  policy, missing capabilities, and supported capabilities.

### Replay and Inspection

- Read-only curated recipe calls can replay a matching completed source task.
  Cache hits are visible through `recipe_replay_cache_hit=true` and
  `recipe_replay_source_task_id`.
- Mutating code tasks expose worktree preservation metadata in task resources:
  `worktree_path`, `worktree_branch`, `worktree_base_sha`, and
  `worktree_preserve_reason`.
- New task viewer resources make inspection friendlier:
  - `aimux://tasks`
  - `aimux://tasks/{task_id}/viewer`
- The viewer is read-only: no forms, buttons, scripts, task submission controls,
  mutation endpoints, or workflow controls.
- Candidate resource smoke documentation now clears stale same-name muxcore
  daemons before validation so a fresh shim does not reconnect to stale code.

### Caller Guide

- New compiled guide resources document the supported caller path for this
  binary:
  - `aimux://guides`
  - `aimux://guides/caller`
- The guide covers `task`, `think(action=start|step|finalize)`, task resources,
  recipe resources, replay metadata, worktree metadata, viewer usage, and safety
  rules.

### Verification

- `go build ./...`
- `go test ./... -count=1 -timeout 300s`
- `go test ./pkg/recipes ./pkg/server -count=1`
- `go test ./pkg/server -run "GuideResource|CallerGuide|RecipeResource|Task.*Viewer|TaskList" -count=1`
- `go test ./tests/critical -count=1 -timeout 300s`
- `AIMUX23_E2E=1 go test ./test/e2e -run TaskInspectability -count=1 -timeout 300s`
- `go test . -count=1` from `D:\Dev\aimux\loom`
- `go vet ./...`
- `go mod verify`
- `govulncheck ./...`
