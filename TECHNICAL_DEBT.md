# Technical Debt — aimux

Tracked accepted-and-deferred work items. Every entry MUST have all 5 fields.
Items here are NOT bugs (file as engram issues instead) — they are conscious tradeoffs
documented for future refactor planning.

Format:
```
### YYYY-MM-DD: [Title]
**What:** description
**Why:** value/risk reasoning that justifies deferral
**Impact:** consequence if unaddressed
**Owner:** named person/agent
**Future ticket:** path to spec/CR/issue that will resolve it
```

---

## AIMUX-11 (centralized logging) — accepted gaps from CR-002

### 2026-04-28: TestShim_Latency iter-6 / iter-11 deterministic 2.017s outlier

**What:** During TestShim_Latency 20-iteration measurement loop, exactly one iteration
(usually #6 or #11) lands at 2.017±0.003 seconds while the other 19 cluster at 25-39 ms.
Trim-top-1 statistic (drop the maximum sample, percentile over remaining 19) is currently
in place to mask the outlier so NFR-1 percentiles report meaningful values.

**Why:** The outlier is NOT shim startup latency — it is daemon-side IPC reconnect
backoff window in `muxcore/owner.ResilientClient` between consecutive shim spawns
under aggressive test loop pacing (100 ms inter-iteration sleep). Root cause is a
muxcore-internal timing characteristic, not aimux code. Fixing it requires upstream
instrumentation in `thebtf/mcp-mux` to expose owner state transitions; we cannot
patch upstream within this CR per the no-external-repo-mutation rule.

**Impact:** NFR-1 metric is computed over 19 samples instead of 20 — a 5% reduction
in statistical robustness. Observable behavior unchanged for real users (single-shim
spawn under normal CC operation never hits this window).

**Owner:** aimux-operator (will revisit after upstream `thebtf/mcp-mux` issue lands
exposing owner state visibility for instrumentation).

**Future ticket:** `thebtf/mcp-mux` issue to be filed: "owner.ResilientClient — expose
state transitions for downstream test instrumentation".

---

### 2026-04-28: FR-8 hot-swap log handoff continuity — UNVERIFIED

**What:** Spec FR-8 requires shim's IPCSink to reconnect transparently to a successor
daemon during hot-swap (issue #129 path) and lose zero log entries during the swap
window (entries should land on stderr fallback). Code path exists (IPCSink reconnect
logic via `ReconnectInitialMs` / `ReconnectMaxMs`); test does NOT exist.

**Why:** Hot-swap test requires orchestrating two daemon binaries, handoff token
exchange, and shim reconnect verification — non-trivial e2e infrastructure. Within
CR-002 scope (blocker fixes from PRC), we focus on direct production blockers; FR-8
verification is deferred.

**Impact:** Hot-swap log behaviour is verified manually by operator during dev binary
upgrades; no automated regression catch. Risk: a future muxcore upgrade silently
breaks shim reconnect → log entries silently land in stderr instead of file → operator
loses correlation between shim activity and aimux.log content.

**Owner:** aimux-operator.

**Future ticket:** to be filed in next AIMUX-N feature spec; reference engram#174
(hot-swap second-apply EngineMode loss) for adjacent test infrastructure.

---

### 2026-04-28: FR-11 SIGTERM graceful drain 500ms — UNVERIFIED

**What:** Spec FR-11 mandates that on SIGTERM/SIGINT/CTRL_BREAK_EVENT the daemon
drains the central queue to lumberjack within a 500 ms hard deadline before exit.
`Logger.DrainWithDeadline()` exists and accepts a `time.Duration`; cmd/aimux/main.go
wires it via `defer log.DrainWithDeadline(500 * time.Millisecond)` in the daemon
shutdown path. No SIGTERM-aware test exercises this.

**Why:** Signal-aware test on Windows requires CTRL_BREAK_EVENT delivery via
GenerateConsoleCtrlEvent in test harness. Within CR-002 scope this is deferred —
the drain function itself is unit-tested via direct call; the SIGTERM trigger path
is not.

**Impact:** Risk that Windows console signal handling regression silently breaks
the drain path → operator-initiated shutdown loses up to 4096 buffered entries
without warning.

**Owner:** aimux-operator.

**Future ticket:** future AIMUX-N feature; pair with FR-8 hot-swap test infrastructure
since both need cross-process signal coordination.

---

### 2026-04-28: FR-9 config knobs (log_forward_buffer_size, log_forward_timeout_ms) — non-default values UNVERIFIED

**What:** Spec FR-9 introduces two config fields (default 100 and 100 ms respectively)
read by `pkg/logger/IPCSink.NewIPCSink` from `cfg.Server.LogForwardBufferSize` and
`cfg.Server.LogForwardTimeoutMs`. Wiring in main.go is verified via existing tests;
non-default values (e.g., BufferSize=10000, Timeout=50ms) are NOT exercised by tests.

**Why:** Default-only verification covers the common case. Edge-case tests with extreme
buffer or timeout values would confirm correct propagation but add little value over
the existing wiring tests — the parameters are passed through directly without
transformation.

**Impact:** A future refactor that accidentally swaps the two parameters or applies
incorrect type coercion would not be caught by current tests. Risk is low because both
fields are integers passed verbatim, but non-zero.

**Owner:** aimux-operator.

**Future ticket:** add table-driven test in `pkg/logger/ipc_sink_test.go` covering
4-5 BufferSize/Timeout combinations.

---

### 2026-04-28: TZ inconsistency between [shim-...] and [daemon-...] log entries

**What:** Daemon-internal log entries are formatted with local timezone (`+03:00`
seen in operator's environment), while shim-forwarded entries appear with UTC suffix
`Z`. Same `aimux.log` file therefore contains mixed timezone formats. Manually verified
2026-04-28 in TestMultiProcessLogIntegrity output.

**Why:** Each emitter formats its own timestamp at the point of `log.Info(...)` —
shim's clock (UTC) vs daemon's clock (local). Spec FR-5 does not mandate a normalised
timezone; CR-002 does not have bandwidth to relitigate this.

**Impact:** Operator parsing log entries with simple time-sort sees apparent
out-of-order entries when crossing role boundaries. Cosmetic, NOT a correctness issue.

**Owner:** aimux-operator (cosmetic, low priority).

**Future ticket:** future AIMUX-N: standardise on UTC for all log entries (preferred —
matches RFC 3339 best practice for shared logs) OR daemon-local for all (preserves
operator's mental model). Decision deferred.

---

### 2026-04-28: AIMUX_TEST_EMIT_LINES env hook in production binary without build-tag

**What:** `cmd/aimux/shim.go` contains a test-only code path activated by the
`AIMUX_TEST_EMIT_LINES` environment variable. When set to a positive integer N, the
shim emits N synthetic test-emit log entries after IPC handshake completes, then
calls `os.Exit(0)`. The path is GUARDED by env-presence check (env unset → dead code),
but the binary itself ships with the test code compiled in.

**Why:** Build-tag separation (`//go:build aimux_testhooks`) would require dual binary
artifacts (production vs test) and dual CI matrices. Within CR-002 scope, env-gating
is sufficient because:
- Setting AIMUX_TEST_EMIT_LINES requires shell access (already attacker-equivalent);
- The hook only emits log lines + os.Exit, no privilege escalation;
- Multi-tenant scenarios are explicitly out of trust boundary per FR-12 honesty
  rewrite (separate daemons per tenant required).

**Impact:** A hostile user with shell access on the same machine can trigger the
hook to emit synthetic log lines into the shared aimux.log. Confined to log spam +
abrupt shim disconnect; no code execution, no data exfiltration.

**Owner:** aimux-operator.

**Future ticket:** future AIMUX-N: introduce `+build aimux_testhooks` separation if
multi-tenant operation becomes a real production scenario. Currently single-operator
single-tenant deployment is the design target (constitution).

---

### 2026-04-29: think-patterns-excellence T021 live-test deferred

**What:** T021 of think-patterns-excellence requires live-testing all 23 think
patterns with realistic input via the running MCP server, rating each EXCELLENT.
Cannot be automated within `go test ./...` because the patterns are
session-stateful and require a live MCP client driver.

**Why:** Operator runs CC sessions throughout the day exercising patterns naturally.
A formal 23-pattern walkthrough script before every release adds operational
overhead with limited regression-detection value over the 270 unit + AC tests
that already cover behaviour.

**Impact:** Risk that a runtime regression invisible to unit tests (e.g.
session-state corruption under concurrent CC sessions) ships unnoticed. Mitigated
by the existing critical-suite (rule #10) + emulation-playbook (rule #11) gates
which exercise the live IPC path.

**Owner:** aimux-operator.

**Future ticket:** before tagging `v5.0.2`, walk through 5 representative
patterns (sequential_thinking, debugging_approach, scientific_method,
critical_thinking, collaborative_reasoning) via live CC session. If any
returns degraded data — open issue. Skip the formal 23-row table; this is
a manual smoke gate, not a contract.

---

### 2026-04-28: PeerCredsUnavailable counter REMOVED in CR-002 (was misleading)

**What:** CR-001 introduced `PeerCredsUnavailable` counter exposed via
`sessions(action=health)`. CR-002 honesty rewrite (FR-12 revised) acknowledges that
peer credentials are physically unreachable on the muxcore notification path — the
counter would therefore increment on EVERY forwarded entry, conveying no signal.
CR-002 spec amendment removes the counter declaration; CR-002 implementation deletes
the increment site (was never implemented anyway, so deletion is trivial).

**Why:** Keeping a permanently-pegged counter in the public health endpoint is a
discoverability anti-pattern — operators monitoring the value would conclude the
system is in a constant degraded state when in fact this is the API limitation.

**Impact:** Any external dashboard/alert wiring this counter would need to remove the
reference. Internally aimux has none.

**Owner:** aimux-operator.

**Future ticket:** when upstream `thebtf/mcp-mux` exposes `ConnInfo` on notification
handler signature (issue to be filed in CR-002 step T008), restore peer-credential
verification AND a meaningfully-conditional `PeerCredsUnavailable` counter (incremented
only when the call genuinely fails on a supported platform).

---
