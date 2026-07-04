## v5.19.0 — API executor hardening, workflow gates, persistent sessions

This release ships the next aimux roadmap batch after `v5.18.1`: API executor resilience, persistent CLI session binding, workflow gate hardening, and the CI-stability fix that unblocked the release train.

### Highlights

- API executor groundwork now covers the OpenAI, Anthropic, and Google SDK refresh, typed API executor configuration, provider health enrichment, and rate-limit/cooldown tracking.
- Persistent CLI session binding is wired through Swarm and executor adapters so stateful/persistent handles can carry session startup arguments without weakening handle lifecycle checks.
- Workflow coverage now exercises audit observability, advisory/blocking gate modes, domain workflows, and YAML workflow loading.
- Deferred-verification coverage now protects FR-8, FR-9, FR-11, and timezone behavior.
- The Windows CI blocker in `TestSwarm_ParallelKeysFactoryNonBlocking` is fixed by replacing a brittle wall-clock threshold with a deterministic distinct-key concurrency barrier proof.
- The repository now has a project-local release protocol documenting aimux tag-triggered GoReleaser releases and required gates.

### Compatibility

No breaking API or CLI migration is required for this release. The release is a minor version because it includes new executor/workflow capabilities after `v5.18.1`.

### Verification

- PR #183 merged after CI run `28690293149` and Security run `28690293163` completed successfully.
- Post-merge focused regression passed: `go test ./pkg/swarm -run TestSwarm_ParallelKeysFactoryNonBlocking -count=20 -timeout 60s`.
- Release preflight requires the gates documented in `docs/RELEASE-PROTOCOL.md` before tagging and publishing `v5.19.0`.

### Notes

AIMUX-25 CR-004 remains open for a later implementation slice; this release covers CR-000 through CR-003 plus B2/C1/C2/D1 work and the merged CI fix.
