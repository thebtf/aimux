## v5.20.0 — Tenant-aware API Swarm factory

This release ships AIMUX-25 CR-004: API executors can now be constructed through Swarm with tenant/session-aware key resolution while preserving the existing CLI factory path.

### Highlights

- Swarm now has `NewWithContextFactory`, letting executor construction receive a detached value-carrying request context without inheriting cancellation.
- API executor Swarm names use `api:<provider>:<model>` and route through `api.SwarmFactory` / `api.ContextCompositeFactory`.
- API key resolution now receives `context.Context`, provider, and tenant ID before executor construction, closing the CR-004 SEC-001 review blocker.
- Restart/recreate paths preserve tenant/session-specific key resolution after executor replacement, including legacy-default fallback context canonicalization.
- Critical smoke coverage proves OpenAI, Anthropic, and Google API executor handles spawn through Swarm without real provider calls, including tenant A/B distinct key resolution for the same API executor name.

### Compatibility

No CLI migration is required. Existing `swarm.New(func(name string) ...)` callers keep the legacy name-only factory behavior. Live server ProjectContext wiring is intentionally not claimed by this release; this release ships the factory/helper seam and critical Swarm smoke scoped by AIMUX-25 CR-004.

### Verification

- PR #186 and follow-up PR #187 merged after CI, Security, CodeRabbit, and PM review gates completed successfully.
- PM re-review accepted `origin/master..8fea4f1`, then accepted PR #187 head `389717d` after the PR-review restart fallback thread was patched and resolved.
- PM reran on the final patch: `git diff --check 160689963b660011e028ce7542a9b997ebb9ca5f..389717d`, `go test ./pkg/swarm/... -count=1`, `go test ./pkg/executor/api/... -count=1`, `go test ./tests/critical/... -count=1 -run SwarmAPI`, `go build ./...`, and `go test ./... -count=1 -timeout 120s`.
- Release-readiness gates passed on `master@130e95f`: release git readiness preflight, `go build ./...`, `go test ./... -count=1 -timeout 120s`, `go vet ./...`, clean-cache `go mod verify`, critical suite, stall-detection playbook, AIMUX-21 deterministic product gate, Loom module tests, and `govulncheck ./...`.

### Notes

AIMUX-25 CR-004 code landed at `master@130e95f`; the release commit adds only release notes/changelog updates, and the annotated `v5.20.0` tag points at that release commit. AIMUX-9 CR-002 was also reconciled in PM governance artifacts as implemented/release-contained; AIMUX-9 CR-001 remains a post-purge rebaseline lane.
