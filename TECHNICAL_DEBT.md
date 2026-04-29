# Technical Debt — aimux

**Status:** EMPTY (2026-04-29).

All previously-tracked debt items have been migrated to active spec roadmaps.
Per autopilot promise ("Tech debt is empty. No deferred tasks instead of already documented"),
this file is intentionally vacant.

## Migration map (where former TD items now live)

| Former TD item | Current home |
|---|---|
| TestShim_Latency 2.017s deterministic outlier | `.agent/specs/aimux-v5-roadmap/architecture.md` §DEF-7 (muxcore upstream blocked) |
| FR-8 hot-swap log handoff continuity UNVERIFIED | `.agent/specs/aimux-v5-roadmap/architecture.md` §Phase D1 (AIMUX-11 deferred verifications) |
| FR-11 SIGTERM 500ms drain UNVERIFIED | `.agent/specs/aimux-v5-roadmap/architecture.md` §Phase D1 |
| FR-9 non-default config knob UNVERIFIED | `.agent/specs/aimux-v5-roadmap/architecture.md` §Phase D1 |
| TZ inconsistency `[shim-...]` vs `[daemon-...]` | `.agent/specs/aimux-v5-roadmap/architecture.md` §Phase D1 |
| AIMUX_TEST_EMIT_LINES env hook → build-tag separation | `.agent/specs/aimux-v5-roadmap/architecture.md` §DEF-6 (trust model change) |
| think-patterns-excellence T021 live-test | RESOLVED 2026-04-28 — engram#180 fix in commit 3cd8c4c |
| PeerCredsUnavailable counter restore | `.agent/specs/aimux-v5-roadmap/architecture.md` §DEF-4 (muxcore upstream blocked) |

## Why this file exists

Format reference for future deferral entries (5 mandatory fields per entry):

```
### YYYY-MM-DD: [Title]
**What:** description
**Why:** value/risk reasoning that justifies deferral
**Impact:** consequence if unaddressed
**Owner:** named person/agent
**Future ticket:** path to spec/CR/issue that will resolve it
```

## Discipline rule

Items here are NOT bugs (file as engram issues instead) — they are conscious tradeoffs documented for future refactor planning.

If you find yourself adding an entry, ask FIRST:
1. Can this be fixed inline in current PR? → Fix it now.
2. Is it covered by an active spec roadmap entry already? → Add cross-reference in commit message, do NOT duplicate here.
3. Is it truly blocked by external dependency (upstream library, infrastructure decision)? → Add to `.agent/specs/aimux-v5-roadmap/architecture.md` §Deferred Items as DEF-N.

This file should remain empty under normal operation. A non-empty file means migration to a tracked roadmap entry is overdue.
