# loom Recovery Guide

Operator playbook for terminal states, crash recovery, and cleanup.

References: [CONTRACT.md](CONTRACT.md) | [PLAYBOOK.md](PLAYBOOK.md) | [CHANGELOG.md](CHANGELOG.md)

---

## Terminal States Explained

A task in a terminal state will never transition again. The store enforces this.

### `completed`

The task ran to completion and its result was accepted by the quality gate.
`task.Result` contains the worker's output. `task.CompletedAt` is set.

**Operator action:** None required.

### `failed`

The worker returned a non-nil error, or the quality gate rejected the result
after all retries were exhausted (including thrashing detection). `task.Error`
contains the error message or rejection reason.

**Operator action:** Inspect `task.Error` to understand why the task failed.
Common causes: invalid input, worker configuration error, external service
permanently unavailable. Decide whether to resubmit with corrected input.

### `failed_crash`

Loom uses `failed_crash` when execution ended without trustworthy completion or
stop proof. This includes:

- a daemon restart that finds `dispatched`, `running`, `input_required`,
  `retrying`, or `cancelling` work;
- a recovered worker or quality-gate panic;
- an active cancellation whose legacy `Worker` returns or crashes without valid
  `StopEvidence`. This case carries the `unverified_stop` error class.

**The task may have partially executed.** The worker may have spawned a
subprocess, made an HTTP call, or mutated external state before the crash.
`failed_crash` is terminal — loom does NOT auto-retry these tasks.

**WARNING: Do NOT blindly auto-resubmit `failed_crash` tasks.** The task may
have already produced side effects. Resubmitting an idempotent task is safe;
resubmitting a non-idempotent task (one that sends emails, charges cards, writes
to external APIs without deduplication) may cause duplicate effects.

### `cancelled`

`cancelled` is a stored terminal status, not an early signal:

- A `pending` task can commit directly to `cancelled` because no execution
  exists; `Cancel` also emits `EventTaskCancelled` for this no-stop path.
- An active task first commits `cancelling` and `task.cancel_requested` before
  its cancel function is signalled. It reaches `cancelled` only when
  `CommitCancelled` accepts valid `StopEvidence` observed no earlier than the
  durable cancel request. That command appends the durable `task.cancelled`
  authority artifact but currently does not emit `EventTaskCancelled` through
  the Loom EventBus.

The current legacy `Worker` integration does not fabricate stop proof from a
context error or worker return. Active cancellation without valid proof becomes
`failed_crash`/`unverified_stop` instead.

**Operator action:** A terminal `cancelled` task needs no recovery. A
`failed_crash` task with `unverified_stop` needs the same side-effect review as
other uncertain executions.

### `cancelling` (non-terminal)

`cancelling` means durable cancellation intent exists, but stop truth is not yet
known. Subscribers observe `task.cancel_requested` only after this state and its
authority fact commit. Do not treat the signal or the worker's context error as
proof that the underlying execution stopped.

---

## Crash Recovery Semantics

When the daemon restarts, call `RecoverCrashed()` once before registering
workers or accepting new submissions:

```go
engine, err := loom.NewEngine(db, "daemon-name", opts...)
if err != nil {
    log.Fatalf("engine init: %v", err)
}

n, err := engine.RecoverCrashed()
if err != nil {
    log.Fatalf("crash recovery failed: %v", err)
}
if n > 0 {
    log.Printf("[loom] crash recovery: marked %d tasks as failed_crash", n)
}

engine.RegisterWorker(loom.WorkerTypeCLI, myWorker)
// now start accepting requests
```

`RecoverCrashed()` captures one UTC timestamp for the invocation and lists
engine-owned candidates in deterministic `created_at`/ID order from:

```text
dispatched | running | input_required | retrying | cancelling
```

Each candidate goes through `CommitFailedCrash` independently. The task row,
open-action fence, and one `task.failed_crash` authority fact use the same
recovery timestamp and commit before the subscriber event is emitted. A
per-task authority conflict is skipped; an infrastructure error is joined while
later candidates continue. The return value counts successful commits. A count
of 0 on a clean or already-recovered startup is normal.

Do not substitute `TaskStore.MarkCrashed` for this startup path. That exported
legacy helper preserves the old raw-SQL behavior for compatibility; it does not
write authority facts, fence actions, cover all active states, or emit events.

---

## Operator Playbook: After a Daemon Restart

### Step 1: Check how many tasks were affected

```go
tasks, err := engine.ListEngine(loom.TaskStatusFailedCrash)
if err != nil {
    log.Fatalf("list recovered tasks: %v", err)
}
log.Printf("%d tasks marked failed_crash", len(tasks))
```

### Step 2: Investigate each failed_crash task

For each `failed_crash` task:

1. **Read `task.Error`** — it contains the panic message (if the crash was a
   Go panic in the worker) or the crash recovery message.
2. **Check `task.DispatchedAt`** — how long was the task running before the crash?
3. **Check external systems** — did the worker's subprocess or HTTP call complete?
   Look at external logs for the time window between `DispatchedAt` and the crash.

### Step 3: Decide on each task

| Task type | Action |
|---|---|
| Idempotent (read-only, deduplication guaranteed) | Safe to resubmit |
| Non-idempotent, no evidence of completion | Investigate, then resubmit if safe |
| Non-idempotent, evidence of partial completion | Manual intervention required |
| Already producing output visible to end users | Do NOT resubmit without deduplication |

### Step 4: Resubmit selectively

```go
for _, t := range crashedTasks {
    if isIdempotentWorkerType(t.WorkerType) {
        _, err := engine.Submit(ctx, loom.TaskRequest{
            WorkerType: t.WorkerType,
            ProjectID:  t.ProjectID,
            RequestID:  t.RequestID, // preserve original tracing ID
            Prompt:     t.Prompt,
            // copy other fields as needed
        })
        if err != nil {
            log.Printf("resubmit %s failed: %v", t.ID, err)
        }
    } else {
        log.Printf("manual review required: task %s (type=%s)", t.ID, t.WorkerType)
    }
}
```

---

## Cleanup Strategies

Terminal tasks (`completed`, `failed`, `failed_crash`, `cancelled`) accumulate
in the store indefinitely. Implement a periodic cleanup job to keep the database
small. Treat `failed_crash`/`unverified_stop` as uncertain execution, not as a
routine cancellation record.

### Recommended retention

| Status | Recommended retention |
|---|---|
| `completed` | 7–30 days (depends on audit requirements) |
| `failed` | 30 days (for debugging) |
| `failed_crash` | 90 days (for post-mortems) |
| `cancelled` | 7 days when cancellation is routine |

### Cleanup query

The `TaskStore` does not expose a Delete method in the current API. Access the underlying
`*sql.DB` directly for cleanup:

```go
// Purge completed tasks older than 30 days.
cutoff := time.Now().Add(-30 * 24 * time.Hour)
result, err := db.Exec(
    `DELETE FROM tasks WHERE status = 'completed' AND completed_at < ?`,
    cutoff,
)
if err != nil {
    log.Printf("cleanup error: %v", err)
    return
}
n, _ := result.RowsAffected()
log.Printf("purged %d completed tasks", n)
```

Run this as a background goroutine on a timer, or as a cron job against the
SQLite file.

---

## Frequently Asked Questions

**Q: Should I auto-resubmit `failed_crash` tasks?**

No — not unconditionally. The correct answer depends on whether your workers
are idempotent. A worker that reads a file and returns its contents is safe to
resubmit. A worker that sends an email is not. Design your workers for
idempotency if you want safe auto-retry.

**Q: Why is `failed_crash` terminal and not retried automatically?**

Because loom cannot know whether the worker partially executed before the crash.
Auto-retrying a non-idempotent worker after a crash can cause data corruption or
duplicate side effects. The conservative choice is to require explicit operator
decision.

**Q: What is the difference between `failed` and `failed_crash`?**

`failed` = worker returned an error or the gate rejected after all retries.
`failed_crash` = Loom lacks trustworthy completion/stop truth after restart,
panic, or active cancellation without valid stop evidence; the task may have
partially executed.

**Q: Can a `failed_crash` task be re-queued in-place (same ID)?**

No. Task IDs are immutable. Resubmitting creates a new task with a new ID.
