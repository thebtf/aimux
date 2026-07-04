## v5.19.0 — API executor hardening, workflow gates, persistent sessions

This release ships the next aimux roadmap batch after `v5.18.1`: API executor resilience, persistent CLI session binding, workflow gate hardening, and the MCP task-call release-gate fixes that unblocked the release train.

### Highlights

- API executor groundwork now covers the OpenAI, Anthropic, and Google SDK refresh, typed API executor configuration, provider health enrichment, and rate-limit/cooldown tracking.
- Persistent CLI session binding is wired through Swarm and executor adapters so stateful/persistent handles can carry session startup arguments without weakening handle lifecycle checks.
- Workflow coverage now exercises audit observability, advisory/blocking gate modes, domain workflows, and YAML workflow loading.
- Deferred-verification coverage now protects FR-8, FR-9, FR-11, and timezone behavior.
- The Windows CI blocker in `TestSwarm_ParallelKeysFactoryNonBlocking` is fixed by replacing a brittle wall-clock threshold with a deterministic distinct-key concurrency barrier proof.
- MCP `task` now advertises the minimal mcp-go task capability surface required for `TaskSupportRequired` tool-call tasks while keeping the production `async_mandatory` contract strict.
- Direct JSON-RPC e2e now proves the outer SDK task to inner Loom job path, including a bounded detached submit window for caller disconnect tolerance.
- The repository now has a project-local release protocol documenting aimux tag-triggered GoReleaser releases and required gates.

### Compatibility

No breaking API or CLI migration is required for this release. The release is a minor version because it includes new executor/workflow capabilities after `v5.18.1`.

### Verification

- PR #183 merged after CI run `28690293149` and Security run `28690293163` completed successfully.
- PR #184 merged as `ff17d2d194cd445d7d8a2aa019c63532c7c947d4`; master push CI run `28694599167` and Security run `28694599172` completed successfully.
- Post-merge focused regression passed: `go test ./pkg/swarm -run TestSwarm_ParallelKeysFactoryNonBlocking -count=20 -timeout 60s`.
- Release-readiness on `origin/master@ff17d2d` passed with clean `GOMODCACHE=D:\tmp\aimux-clean-gomodcache-mcpgo`: `go mod verify`, focused server/stall tests, AIMUX-21 deterministic e2e, full e2e, critical suite, `go build ./...`, full `go test ./...`, `go vet ./...`, Loom module tests, and `govulncheck ./...`.
- Final tag/publish still requires the gates documented in `docs/RELEASE-PROTOCOL.md`, including clean/synchronized `master`, annotated remote tag verification, and tag-triggered release workflow evidence.

### Notes

AIMUX-25 CR-004 remains open for a later implementation slice; this release covers CR-000 through CR-003 plus B2/C1/C2/D1 work and the merged CI fix.
