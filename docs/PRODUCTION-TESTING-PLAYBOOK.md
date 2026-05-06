# aimux Production Testing Playbook

**Last updated:** 2026-05-06
**Tested surface:** current post-purge master surface: 4 server tools,
`think(action=start|step|finalize)`, and 22 cognitive move tools.
**Mode:** customer (no internal code knowledge — operator perspective)

## How to use

This playbook is walked through in **customer mode**. The operator (or an agent
emulating one) opens ONLY this document and the public README/CHANGELOG. Source
files under `pkg/`, `loom/`, `cmd/` MUST stay closed during the walkthrough —
the goal is to expose UX and data-plane regressions that automated assertions
miss.

Procedure for each scenario:

1. Read the **Steps** block top-to-bottom and run the commands verbatim.
2. Compare actual output to the **Expected** block.
3. Apply the **Pass criteria** (objective: exit codes, file existence, exact
   strings — never "feels right").
4. Record one of the four verdicts from the [Verdict template](#verdict-template).
5. Move to the next scenario. Do NOT debug failures by reading source — log the
   verdict and continue. Debugging happens after the full pass.

A scenario marked **BROKEN** does not abort the run. The product is judged on the
aggregate: PRODUCT_WORKS / PARTIALLY_WORKS / BROKEN.

## Setup

### Prereqs

- Linux, macOS, or Windows host with a POSIX-like shell (bash, zsh, or PowerShell
  with `kill` shimmed).
- Go 1.25.9+ available **only for the build step**. After the binary is produced,
  the operator should drop the toolchain assumption — only `./aimux`, `cat`,
  `kill`, and `mcp-launcher` (optional) are used.
- Two unprivileged user accounts on the host with distinct numeric UIDs (used
  for tenant simulation in Phase C and beyond). Examples below use UID 1001
  ("alice") and UID 1002 ("bob").
- Writable working directory, default `./.aimux/`.
- No other aimux daemon running on the same engine name. If unsure:
  ```bash
  pgrep -af aimux || echo "no aimux processes"
  ```

### Build

From the project root:

```bash
export GOTOOLCHAIN=go1.25.9
go build -o aimux ./cmd/aimux/
./aimux --version
```

After this step, treat `./aimux` as a black-box binary the operator received
from a release artifact. Do not open the source tree again.

### Single-tenant smoke (legacy mode, no tenants.yaml)

This is the baseline — no `tenants.yaml` on disk, daemon should run as before
the multi-tenant work landed.

```bash
rm -f ./.aimux/tenants.yaml
./aimux --help
```

**Expected:**
- Help text printed to stdout.
- Exit code `0`.
- No mention of "tenant denied" or "bootstrap required" in stderr.

### Multi-tenant configuration

Place the following file at `./.aimux/tenants.yaml`:

```yaml
tenants:
  - name: alice
    uid: 1001
    role: operator
    rate_limit_per_sec: 100
    refill_rate_per_sec: 100
    max_loom_tasks_queued: 50
    max_concurrent_sessions: 10
  - name: bob
    uid: 1002
    role: plain
    rate_limit_per_sec: 50
    refill_rate_per_sec: 50
    max_loom_tasks_queued: 20
    max_concurrent_sessions: 5
```

Confirm the file is readable:

```bash
cat ./.aimux/tenants.yaml
```

**Expected:** YAML printed verbatim, no parse warnings on the next daemon start.

## Phase A — Daemon startup

### Scenario A1: Cold start (single-tenant, no tenants.yaml)

**Steps:**
1. `rm -f ./.aimux/tenants.yaml`
2. `./aimux serve --transport=stdio &`
3. `sleep 2 && pgrep -af aimux`
4. `kill -TERM $(pgrep -af 'aimux serve' | awk '{print $1}')`

**Expected:**
- `pgrep` finds at least one `aimux serve` process.
- stderr/log shows `daemon ready` (or equivalent — substring `ready` is the
  marker).
- Stop step exits cleanly within 5 seconds.

**Pass criteria:**
- `pgrep` exit code `0` after step 3.
- No string `panic` in stderr.
- Final `pgrep -af aimux` returns no rows after the kill.

### Scenario A2: Cold start with tenants.yaml present

**Steps:**
1. Place the multi-tenant `tenants.yaml` from Setup.
2. `./aimux serve --transport=stdio &`
3. `sleep 2 && cat ./.aimux/aimux.log | tail -n 50`
4. Stop the daemon.

**Expected:**
- Log mentions `tenants loaded: 2` (or equivalent — exact count `2`).
- No `bootstrap required` message.
- Daemon stays up for the full 2-second window.

**Pass criteria:**
- Log substring `tenants loaded: 2` present.
- Exit on stop is clean (no error in last 5 log lines).

### Scenario A3: Bootstrap flag `--bootstrap-operator-uid 1001`

**Steps:**
1. `rm -f ./.aimux/tenants.yaml`
2. `./aimux serve --transport=stdio --bootstrap-operator-uid 1001 &`
3. `sleep 2 && cat ./.aimux/tenants.yaml`
4. Stop the daemon.

**Expected:**
- File `./.aimux/tenants.yaml` is created automatically.
- It contains exactly one tenant with `uid: 1001` and `role: operator`.
- Log line confirms bootstrap (substring `bootstrap` or `tenants.yaml created`).

**Pass criteria:**
- File exists after step 3.
- `grep 'uid: 1001' ./.aimux/tenants.yaml` returns exit `0`.
- `grep 'role: operator' ./.aimux/tenants.yaml` returns exit `0`.

## Phase B — Happy path (single-tenant)

Single-tenant context: no `tenants.yaml` on disk. Run all five scenarios in one
daemon lifetime.

**Setup (run once):**
1. `rm -f ./.aimux/tenants.yaml`
2. `./aimux serve --transport=stdio &`
3. `sleep 2`

**Teardown (after all four):**
- Stop the daemon: `kill -TERM $(pgrep -af 'aimux serve' | awk '{print $1}')`

### Scenario B1: sessions health tool

**Steps:**
1. From an MCP client (mcp-launcher or any MCP-capable client), call:
   ```
   tool: sessions
   args: {"action": "health"}
   ```

**Expected:**
- JSON response with fields including `init_phase`, `running_jobs`, and
  `warmup_deferred_count`.
- `init_phase` is `2` (Phase B complete) within 30 seconds of cold start.

**Pass criteria:**
- Response is valid JSON.
- `init_phase == 2` within the SLA window.
- No `error` field at the top level.

### Scenario B2: sessions list tool

**Steps:**
1. Call:
   ```
   tool: sessions
   args: {"action": "list"}
   ```

**Expected:**
- Structured response containing legacy session rows and Loom task rows. Both
  sources may be empty on a cold daemon.
- Each row, if any, carries enough identity and status fields for an operator to
  correlate it with a running or completed task.

**Pass criteria:**
- Response parses as JSON.
- No 5xx-style error envelope.

### Scenario B3: think pattern call (sequential_thinking)

**Steps:**
1. Call:
   ```
   tool: sequential_thinking
   args: {"thought": "Test thought 1", "thoughtNumber": 1, "totalThoughts": 2}
   ```
2. Call again with `thoughtNumber: 2, totalThoughts: 2`.

**Expected:**
- Both calls return structured think output.
- Second call references the first via session continuity (or returns a fresh
  thread — both are acceptable, document which).

**Pass criteria:**
- Both responses include the input thought in some form.
- No `panic` or `internal error` substring in either response.

### Scenario B4: think(action=start|step|finalize) gated finalization

**Steps:**
1. Call:
   ```
   tool: think
   args: {"action":"start","task":"Decide whether the answer can ship","context_summary":"Caller must provide evidence before finalization"}
   ```
   Capture `session_id`.
2. Attempt premature finalization:
   ```
   tool: think
   args: {"action":"finalize","session_id":"<session_id>","proposed_answer":"Ship it now"}
   ```
3. Submit one supported move:
   ```
   tool: think
   args: {"action":"step","session_id":"<session_id>","chosen_move":"critical_thinking","work_product":"The answer has visible support and no critical objection.","confidence":0.78,"evidence":[{"kind":"file","ref":"spec.md","summary":"finalization requires visible support","verification_status":"verified"}]}
   ```
4. Finalize again:
   ```
   tool: think
   args: {"action":"finalize","session_id":"<session_id>","proposed_answer":"The supported answer can ship."}
   ```

**Expected:**
- Step 2 returns `can_finalize: false` and `missing_gates`.
- Step 3 returns `executed: true`, `gate_report`, `confidence_ceiling`, and
  bounded `trace_summary`.
- Step 4 returns `can_finalize: true`, `stop_decision.action: "finalize"`,
  `confidence_tier`, and `trace_summary.stop_reason`.
- No response exposes a full unbounded trace by default.

**Pass criteria:**
- Premature finalization is a gate response, not an MCP tool error.
- Supported finalization succeeds only after a visible work product and verified
  evidence are submitted.
- `trace_summary` is present and bounded in every action-mode response.

### Scenario B5: deepresearch query

**Steps:**
1. Call:
   ```
   tool: deepresearch
   args: {"topic": "What is mcp-aimux?"}
   ```

**Expected:**
- Either a synthesized research report, OR a clearly stated cache miss /
  rate-limit / API-key-missing message.
- A real network failure must surface as a message, not a hang.

**Pass criteria:**
- Tool returns within 120 seconds.
- If GEMINI_API_KEY is unset: clear error message naming the missing key, NOT a
  stack trace.
- If GEMINI_API_KEY is set but invalid or expired: clear provider error naming
  invalid credentials, NOT a stack trace. Classify the scenario as
  `PARTIAL`/credential-blocked for the environment, not as a handler crash.
- If GEMINI_API_KEY is set: response contains the topic phrase or a synthesized
  paragraph.

## Phase C — Multi-tenant tenant isolation

**Setup (run once for the phase):**
1. Place multi-tenant `tenants.yaml` from Setup.
2. `./aimux serve --transport=stdio &`
3. `sleep 2`

For each scenario the operator simulates two distinct UIDs by either (a) running
the MCP client under different OS user accounts, or (b) using the documented
`AIMUX_TENANT_UID` envelope override (operator-only test path).

### Scenario C1: Tenant A submits loom task, Tenant B status query

**Steps:**
1. As alice (UID 1001) submit a long-running loom task. Capture `task_id`.
2. As bob (UID 1002) call `status` with the captured `task_id`.

**Expected:**
- alice's call returns a `task_id`.
- bob's call returns an error envelope with `code` matching `ErrTaskNotFound`
  (NOT `403`, NOT a stack trace, NOT alice's task data).

**Pass criteria:**
- bob's response contains substring `not found` or the symbol `ErrTaskNotFound`.
- bob's response does NOT contain alice's `task_id` value.
- Audit log gains one entry (verified in Phase F).

### Scenario C2: Tenant A exhausts rate limit, Tenant B unaffected

**Steps:**
1. As alice, hammer any tool (e.g. `status`) at >100 calls/sec for 2 seconds.
2. While alice is being throttled, as bob make a single `status` call.

**Expected:**
- alice receives `rate_limited` errors after the bucket drains.
- bob's single call succeeds normally — no shared bucket.

**Pass criteria:**
- alice's last 5 calls in the burst contain substring `rate_limit` or
  `too_many_requests`.
- bob's call returns successfully (no error envelope).

### Scenario C3: Tenant A exhausts FR-17 quota, Tenant B can still Submit

**Steps:**
1. As alice, submit 50 loom tasks back-to-back (matches `max_loom_tasks_queued`).
2. As alice, submit task #51.
3. As bob, submit one loom task.

**Expected:**
- Task #51 by alice is rejected with a quota-exceeded error.
- bob's submission succeeds.

**Pass criteria:**
- alice's 51st response contains substring `quota` or `max_loom_tasks_queued`.
- bob's submission returns a `task_id` successfully.

### Scenario C4: Cross-tenant log file isolation

**Steps:**
1. Drive both tenants through any tool (B1 / B2 work) under multi-tenant mode.
2. Inspect:
   ```bash
   ls ./.aimux/logs/
   grep -c alice ./.aimux/logs/bob.log
   grep -c bob ./.aimux/logs/alice.log
   ```

**Expected:**
- Two separate files: `alice.log` and `bob.log` (or
  `<workdir>/.aimux/<tenant>.log` per the configured layout).
- Neither file mentions the other tenant's name in normal log lines.

**Pass criteria:**
- Both files exist.
- `grep -c alice ./.aimux/logs/bob.log` returns `0`.
- `grep -c bob ./.aimux/logs/alice.log` returns `0`.

## Phase D — Authorization gates

**Setup:** multi-tenant mode running.

### Scenario D1: Unenrolled UID connects → JSON-RPC -32000 deny

**Steps:**
1. Connect to the daemon as a UID NOT listed in `tenants.yaml` (e.g. UID 1003).
2. Issue any tool call.

**Expected:**
- JSON-RPC error response with `code: -32000` (or the documented authz code).
- Error message clearly names "tenant not enrolled" or equivalent.
- No tool work executes.

**Pass criteria:**
- Response is a JSON-RPC error envelope.
- `code` is `-32000` or the documented authz value.
- No side effect: no log line for the rejected UID under any tenant log.

### Scenario D2: Operator removes tenant via SIGHUP → existing tenant sessions drain → new sessions denied

**Steps:**
1. Both alice and bob have active sessions.
2. Edit `tenants.yaml`: remove bob's entry.
3. `kill -HUP $(pgrep -af 'aimux serve' | awk '{print $1}')`
4. Bob's existing session: complete the in-flight call.
5. Bob attempts a new session immediately after.

**Expected:**
- In-flight call from step 4 finishes successfully (drain semantics).
- New session attempt in step 5 is denied with the unenrolled-UID error.
- Alice's session is unaffected throughout.

**Pass criteria:**
- Step 4 returns a normal response (not an error).
- Step 5 returns the same authz error as D1.
- Alice's parallel call during the SIGHUP window succeeds.

## Phase E — Hot-reload

**Setup:** multi-tenant mode running.

### Scenario E1: Edit tenants.yaml + SIGHUP → snapshot swap, no daemon restart

**Steps:**
1. Capture daemon PID: `PID=$(pgrep -af 'aimux serve' | awk '{print $1}')`
2. Edit `tenants.yaml` — change alice's `rate_limit_per_sec` from 100 to 200.
3. `kill -HUP $PID`
4. Verify PID unchanged: `pgrep -af 'aimux serve'`
5. Drive alice at >150 calls/sec briefly.

**Expected:**
- PID from step 4 matches the captured PID — no restart.
- Log line confirms reload (substring `tenants reloaded` or `snapshot swapped`).
- Alice no longer gets throttled at 150/sec (new ceiling is 200).

**Pass criteria:**
- PID unchanged.
- Reload log line present.
- Alice's bursts of 150/sec succeed without `rate_limit`.

### Scenario E2: Malformed tenants.yaml + SIGHUP → previous snapshot retained

**Steps:**
1. Backup current `tenants.yaml`.
2. Replace it with malformed content:
   ```bash
   echo "tenants: [this is not valid yaml" > ./.aimux/tenants.yaml
   ```
3. `kill -HUP $PID`
4. Drive alice with the previously-working rate.
5. Restore the backup.

**Expected:**
- Daemon does NOT crash.
- Log line indicates parse failure with file path and a hint.
- Alice's calls continue to succeed under the OLD snapshot's limits.

**Pass criteria:**
- Daemon PID unchanged after SIGHUP.
- Log substring `parse` or `invalid` present, naming `tenants.yaml`.
- Alice's traffic flows under old limits during the bad-config window.

## Phase F — Audit integrity

**Setup:** multi-tenant mode running. Audit log expected at
`./.aimux/audit.log` (or as documented in the README).

### Scenario F1: Cross-tenant attempt → cross_tenant_blocked event

**Steps:**
1. Re-run Scenario C1 (bob queries alice's task_id).
2. `cat ./.aimux/audit.log | tail -n 5`

**Expected:**
- Audit log contains an entry with event type `cross_tenant_blocked`.
- Entry includes both UIDs (1001 and 1002), the tool name, and a timestamp.

**Pass criteria:**
- `grep cross_tenant_blocked ./.aimux/audit.log | wc -l` returns at least `1`
  more than before the scenario.
- Entry contains both `1001` and `1002`.

### Scenario F2: Rate-limited request → rate_limited event

**Steps:**
1. Re-run Scenario C2 (alice burst).
2. `grep rate_limited ./.aimux/audit.log | tail -n 3`

**Expected:**
- One or more `rate_limited` audit entries for alice during the burst window.

**Pass criteria:**
- At least one new `rate_limited` entry naming `1001` after the burst.

### Scenario F3: Audit log buffer full under burst → events dropped, counter incremented

**Steps:**
1. Generate a burst large enough to exceed the audit buffer (operator may need
   to consult the configured buffer size; default ~1024 entries).
2. After the burst, call:
   ```
   tool: sessions
   args: {"action": "health"}
   ```
3. Inspect the response for an `audit_dropped_count` field.

**Expected:**
- The daemon does NOT block on full buffer — it drops events and increments a
  counter.
- The dropped-count is exposed via the health endpoint.

**Pass criteria:**
- Daemon stays responsive throughout the burst (no >5s stalls on unrelated calls).
- `audit_dropped_count` value is greater than `0` after the burst.

## Phase G — Cleanup

### Scenario G1: Graceful shutdown SIGTERM

**Steps:**
1. With several active client connections:
   ```bash
   kill -TERM $(pgrep -af 'aimux serve' | awk '{print $1}')
   ```
2. Wait up to 30 seconds.
3. `pgrep -af aimux`

**Expected:**
- Active calls receive a final response or a clean shutdown error.
- All daemon processes gone within 30 seconds.
- No `panic` in the last 50 log lines.

**Pass criteria:**
- Final `pgrep` returns no rows.
- Last log line indicates clean shutdown (substring `shutdown complete` or
  `bye`).

### Scenario G2: Stale lockfiles after crash recovery

**Steps:**
1. Hard-kill the daemon: `kill -9 $(pgrep -af 'aimux serve' | awk '{print $1}')`
2. List leftover state:
   ```bash
   ls -la ./.aimux/*.lock ./.aimux/*.pid 2>/dev/null
   ```
3. Cold-start a fresh daemon: `./aimux serve --transport=stdio &`
4. `sleep 5 && cat ./.aimux/aimux.log | tail -n 20`

**Expected:**
- New daemon detects stale lock/PID files and reclaims them.
- Log explicitly mentions reclaim (substring `stale` or `reclaim` or `crash recovery`).
- Daemon reaches `Phase B complete` (init_phase 2) normally.

**Pass criteria:**
- Daemon comes up healthy on the second attempt.
- Log line confirms recovery, naming the reclaimed file.
- `sessions` tool returns valid health within 10 seconds of restart.

## Phase H — Installed binary update

### Scenario H1: Local-source install through MCP upgrade tool

This scenario verifies the installed daemon path, not only a source-tree test
binary. Use `mcp-launcher` or an equivalent MCP client that can call
`upgrade(action="apply", source=..., force=true)` and then reconnect.

**Steps:**
1. Build the candidate binary:
   ```powershell
   .\scripts\build.ps1 -Output aimux-dev-next.exe
   $version = (.\aimux-dev-next.exe --version).Trim()
   ```
2. Install through the running daemon:
   ```powershell
   D:\Dev\mcp-launcher\mcp-launcher.exe `
     -binary D:\Dev\aimux\aimux-dev.exe `
     -cwd D:\Dev\aimux `
     -env-mode clean `
     -mode install `
     -source D:\Dev\aimux\aimux-dev-next.exe `
     -force `
     -expect-tools 27 `
     -expect-version $version `
     -timeout 30
   ```
3. Confirm the rollback backup slot is present and not a stale blocker:
   ```powershell
   Test-Path D:\Dev\aimux\aimux-dev.exe.old
   ```

**Expected:**
- The upgrade payload reports `status: "updated_deferred"` in current muxcore
  `SessionHandler` mode.
- The payload includes `handoff_error` explaining that live hot-swap is
  unsupported because `SessionHandler` mode has no transferable upstream
  process.
- Reconnect verification reaches `sessions(action="health")`, reads
  `aimux://health`, sees `tools: 27`, and reports the expected version.
- The final installer line is `[install] PASS`.
- `aimux-dev.exe.old` may remain as the rollback backup created by the atomic
  replace path. Its presence is not a failure; a locked stale old-slot that
  prevents the next install is a failure.

**Pass criteria:**
- `mcp-launcher` exits `0`.
- `aimux://health.version` matches the candidate binary version from step 1.
- `sessions(action="health").init_phase == 2`.
- If `aimux-dev.exe.old` exists after the install, classify it as expected
  rollback state unless the next install reports an old-slot lock.

## Customer-mode questions to answer

After the full walkthrough, the operator must answer these nine questions in
prose. Each answer should cite the scenario(s) that informed it.

1. Does the daemon start cleanly in single-tenant mode without any config?
2. Does it automatically create the bootstrap `tenants.yaml` on
   `--bootstrap-operator-uid`?
3. Does cross-tenant query for someone else's loom task return
   `ErrTaskNotFound` (not 403, not a stack trace)?
4. Does the rate-limited tenant get a clear error message when over quota?
5. Does the audit log contain enough info to debug who got blocked from what?
6. Is the per-tenant log file structure operator-friendly (e.g.,
   `<workdir>/.aimux/<tenant>.log` separable, no cross-tenant leakage)?
7. Does SIGHUP reload work from a stable operator script (no daemon restart
   needed, malformed config does not bring the daemon down)?
8. Does shutdown drain in-flight requests vs hard-cut?
9. Does local-source binary install work through the MCP upgrade tool from a
   fresh operator session, including reconnect and health verification?

## Verdict template

For each scenario, record one verdict:

- **PASS** — works as documented; all pass criteria met.
- **PARTIAL** — works but degraded (e.g., wrong error code, missing audit
  field, slow but not broken). Document the degradation.
- **BROKEN** — does not work at all; pass criteria failed.
- **NO_SURFACE** — feature not present in the current build (e.g., audit
  buffer counter not yet exposed). Document the gap.

### Aggregation rule

- All scenarios PASS → **PRODUCT_WORKS**.
- Any BROKEN in Phase A, C, or D → **BROKEN** (these are load-bearing).
- Any BROKEN in Phase B, E, F, G, or H OR any PARTIAL in any phase →
  **PARTIALLY_WORKS**.
- A NO_SURFACE result does NOT downgrade the verdict on its own — note it as a
  documentation gap and proceed.

### Final report shape

```
## Verdict: <PRODUCT_WORKS | PARTIALLY_WORKS | BROKEN>

### Phase A
- A1: PASS
- A2: PASS
- A3: PARTIAL — bootstrap created file but role field was empty

### Phase B
- B1: PASS
...

### Customer-mode answers
1. <prose answer with scenario citations>
...

### Documentation gaps (NO_SURFACE)
- F3: audit_dropped_count field not present in health response
```

---

**Maintenance:** this playbook stays in lockstep with `docs/CHANGELOG.md`.
Whenever a feature ships that adds a new tenant role, audit event class, or
shutdown signal, the corresponding scenario MUST be added in the same PR. The
`Skill("nvmd-platform:emulation-playbook", "--audit")` gate is what catches
drift; treat its findings as release-blocking.
