{{define "poll-wrapper-subagent"}}
### Polling Wrapper Subagent (MANDATORY for long delegations)

**This is the ONLY valid way to run long-running delegated work (coding, research, audit,
debug, analyze, plan, any task >30 s).** Direct polling loops in the main agent's context
are prohibited — they burn tokens and congest the main turn with noise.

When you dispatch a job via `exec(async=true)`, `agent(async=true)`, or any other aimux
action that returns a `job_id`, you MUST hand polling off to a cheap Sonnet subagent via
your Task/Agent tool. The subagent's ONLY job is to poll until completion and return the
final content. The polling logic stays in the subagent's context (no token cost to you);
you receive a clean single-shot result.

**The rule applies to:**

- Every coding task (implementation, refactor, test generation, any diff-producing work)
- Every research task (deep research, literature review, comparison, analysis)
- Every audit or security scan
- Every debug or investigation that delegates to a CLI
- Every `agent` invocation with `async=true`
- Anything with an unknown or variable runtime

**The rule does NOT apply to:**

- Deterministic sub-second calls: `status`, `sessions(action=list)`, `agents(action=list)`
- Single-shot `think` patterns that complete in-process (no CLI involved)
- Synchronous `exec` calls known to be <30 s AND you actually need the result in the
  same turn (e.g., a quick lookup). When in doubt, use the wrapper.

**Why this pattern is correct:**

- Your own context stays free of polling loops — no sleeps, no status checks, no retry
  ladders. One Task/Agent call in, one final content out.
- The subagent is allowed to run synchronously. It is cheap, stateless, and has a single
  responsibility. Blocking it on polling is fine because its context is isolated from
  yours.
- Cancel semantics are clean: cancelling the subagent kills the wrapper; the underlying
  aimux job continues unless you also call `sessions(action="cancel", job_id=...)`.
- Works uniformly for `exec`, `agent`, and any other aimux async action.

**Template — sync wrapper (blocks your turn, simplest):**

```
Agent(
  subagent_type="general-purpose",
  model="sonnet",
  description="Poll aimux job <JOB_ID>",
  prompt="""
You are a polling wrapper. Do ONLY these steps — nothing else, no exploration, no analysis.

1. Call mcp__aimux__status(job_id="<JOB_ID>")
2. If response.status == "completed": output the response.content field verbatim and stop.
3. If response.status == "failed": output the response.error field verbatim and stop.
4. If response.status == "running":
   - Optionally echo the last 3 lines of response.progress (for visibility in logs).
   - Wait 10 seconds via Bash("sleep 10").
   - Go to step 1.
5. Hard ceiling: 20 minutes of polling total. If exceeded:
   - Call mcp__aimux__sessions(action="cancel", job_id="<JOB_ID>")
   - Output "TIMEOUT: job cancelled after 20 minutes of polling"

Do not interpret the output. Do not attempt to improve or fix anything.
Your job is ONLY to poll and return the raw final content.
"""
)
```

**Template — async wrapper (you keep working in parallel):**

Use this variant only when you have real parallel work to do while the job runs.
Otherwise sync is simpler.

```
Agent(
  subagent_type="general-purpose",
  model="sonnet",
  run_in_background=true,
  description="Monitor aimux job <JOB_ID>",
  prompt="""
Poll mcp__aimux__status(job_id='<JOB_ID>') every 10 seconds.
Echo a short status line on each poll for visibility.
When status='completed' or status='failed', return the content or error and stop.
Hard ceiling: 20 minutes — cancel via mcp__aimux__sessions if exceeded.
"""
)
```

With `run_in_background=true`, the Agent call returns immediately with a task ID; you
receive a `<task-notification>` when the wrapper finishes. Use this only if the polling
loop would otherwise block meaningful parallel work.

**When NOT to use a polling wrapper:**

- Task is known to take less than ~30 seconds — a direct sync tool call is simpler and
  already shows progress via `notifications/progress` (Claude Code renders a progress bar).
- You need intermediate state mid-run (stall warnings, partial findings, cancel on a
  specific progress marker) — in that case, poll yourself with the state you need.
- The job produces streaming data that must be acted upon as it arrives (rare).

**Which subagent_type to use:** `general-purpose` is the safest default — it has full
tool access including Bash (needed for sleep) and MCP tools (needed for status polling).
Avoid specialized agent types that may restrict those tools.
{{end}}
