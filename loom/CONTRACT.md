# loom Contract

Formal specification for the `loom` library (v0.1.0). Sections marked **stable**
will not change in minor versions. Additions are allowed; renames require a
deprecation cycle.

See also: [README.md](README.md) | [PLAYBOOK.md](PLAYBOOK.md) | [CHANGELOG.md](CHANGELOG.md)

---

## Worker Interface

```go
type Worker interface {
    Execute(ctx context.Context, task *Task) (*WorkerResult, error)
    Type() WorkerType
}
```

### Semantics

- `Execute` is called from a background goroutine, never from the caller's goroutine.
- `ctx` is a task-scoped context derived from `context.Background()`, NOT from the
  Submit caller's context. It is cancelled only when `Cancel(taskID)` is called
  or the task's `Timeout` expires.
- The worker MUST NOT retain `task` beyond the call — the engine may reuse or
  deallocate the struct after `Execute` returns.
- `WorkerResult.Content` MUST be non-empty (after trimming whitespace) for the
  quality gate to accept. Empty or whitespace-only strings trigger an automatic retry.
- `WorkerResult.Metadata` is optional. It is persisted and returned by `Get`.
- Returning a non-nil error marks the task `failed` immediately (no retry).

### Error Contract

| Condition | Correct action |
|---|---|
| Transient failure (network, process crash) | Return `(nil, err)` — triggers `failed` |
| Empty output | Return `(result, nil)` — gate handles retry |
| Rate-limit output | Return `(result, nil)` — gate detects and retries |
| Panic | Recovered by engine — task marked `failed_crash` |

Workers MUST NOT call `panic` intentionally. Panics are recovered as a safety
net but indicate a programming error.

### Type Method

`Type()` returns the `WorkerType` that identifies this worker. The engine uses
this to match the registered worker to incoming tasks. `Type()` MUST be
consistent — it must return the same value on every call.

---

## State Machine

All task lifecycle transitions are enforced by the store layer. Illegal
transitions are rejected with an error.

```
                    ┌──────────────────────────────────────────┐
                    │              cancel (any active state)    │
                    ▼                                           │
  ┌─────────┐   ┌────────────┐   ┌─────────┐   ┌───────────┐  │
  │ pending │──▶│ dispatched │──▶│ running │──▶│ completed │  │
  └─────────┘   └────────────┘   └─────────┘   └───────────┘  │
                      │               │         (terminal)      │
                      │               │                         │
                      ▼               ▼                         │
                  ┌────────┐      ┌────────┐                    │
                  │ failed │      │ failed │                    │
                  └────────┘      └────────┘                    │
                  (terminal)      (terminal)                    │
                                      │                         │
                                      │ gate reject             │
                                      ▼                         │
                                 ┌──────────┐   ┌────────────┐ │
                                 │ retrying │──▶│ dispatched │─┘
                                 └──────────┘   └────────────┘
                                 (transient)    (re-dispatches)

  [crash recovery on daemon restart]
  dispatched ──▶ failed_crash  (terminal)
  running    ──▶ failed_crash  (terminal)

  [cancellation via Cancel/CancelAllForProject]
  running    ──▶ failed  (context cancelled, worker returns error)
```

### Status Values

`pending` → `dispatched` → `running` → `completed` (terminal).
Failed outcomes: `failed` (terminal), `failed_crash` (terminal).
Gate-driven loop: `running` → `retrying` → `dispatched` (up to maxRetries).
Cancellation: signals context cancellation; task ends as `failed` in v0.1.0.

Terminal states: `completed`, `failed`, `failed_crash`. No store transitions
out of terminal states are valid. The store layer enforces this invariant.

---

## Engine Lifecycle

### Submit

`Submit(ctx, req)` is non-blocking. It:

1. Generates a task ID and writes the task to the store as `pending`.
2. Emits `EventTaskCreated`.
3. Transitions the task synchronously to `dispatched` (before returning).
4. Emits `EventTaskDispatched`.
5. Launches `go dispatch(task)` and returns the task ID.

The caller's context (`ctx`) is used ONLY for:
- Extracting `RequestIDKey` for distributed tracing.
- Emitting OTel metrics (counter/histogram record calls).

It is NOT used as the task's execution context. The task continues running even
if the caller's context is cancelled.

### Dispatch Goroutine

The dispatch goroutine runs with a task-scoped `context.Background()`-derived
context. It:

1. Transitions `dispatched → running`, emits `EventTaskRunning`.
2. Creates a cancel function stored in `l.cancels[taskID]`.
3. Calls `worker.Execute(taskCtx, task)`.
4. On success: runs quality gate; on accept: transitions `running → completed`.
5. On gate reject with retry: transitions `running → retrying → dispatched → running`.
6. On gate reject without retry, or worker error: transitions to `failed`.

Panics in `Execute` or the gate are recovered. The task is marked `failed_crash`.

### Context Independence (C4 Rule)

**Session disconnect does NOT cancel running tasks.** The task-scoped context
is derived from `context.Background()`, not from any HTTP request, MCP session,
or caller-provided context. Tasks survive connection drops.

Only these operations cancel a task:
- `engine.Cancel(taskID)` — signals one task.
- `engine.CancelAllForProject(projectID)` — signals all running tasks for a project.
- Task `Timeout` field expiry (if set, creates a `context.WithTimeout`).

---

## Event Delivery Contract (FR-14)

### Subscribe

```go
unsubscribe := engine.Events().Subscribe(func(e TaskEvent) {
    // Handle event. MUST return quickly.
})
defer unsubscribe()
```

- Delivery is **synchronous** on the dispatch goroutine.
- Subscribers are called in registration order.
- Panics in a subscriber are recovered and logged. They do NOT affect other
  subscribers or the engine.
- Subscribers **MUST return quickly**. Blocking a subscriber blocks task dispatch.
  Offload heavy work to a goroutine.

### No Past-Event Replay

Subscribing mid-flight does NOT deliver events that already occurred. Only future
events from the moment of subscription are delivered.

### Unsubscribe Safety

Calling the returned unsubscribe function is idempotent. Calling it after engine
shutdown is safe. Calling it multiple times is safe.

### Event Fields

```go
type TaskEvent struct {
    Type      EventType
    TaskID    string
    ProjectID string
    RequestID string    // empty if no RequestID injected
    Status    TaskStatus
    Timestamp time.Time
}
```

All six fields are always populated. `RequestID` is empty if none was injected
via `WithRequestID(ctx, id)` before `Submit`.

### Event Types (8 values)

| EventType | When emitted |
|---|---|
| `task.created` | After task written to store (still pending) |
| `task.dispatched` | After pending→dispatched transition |
| `task.running` | After dispatched→running transition |
| `task.completed` | After running→completed transition |
| `task.failed` | After any →failed transition |
| `task.failed_crash` | After crash recovery or dispatch panic |
| `task.retrying` | After running→retrying transition |
| `task.cancelled` | After Cancel or CancelAllForProject signals a task (before worker returns) |

---

## Cancellation Semantics

### Cancel(taskID)

Signals the cancel function for a single running task. If the task is not
currently running (completed, failed, or not yet dispatched), returns an error.

### CancelAllForProject(projectID)

Takes a snapshot of running tasks for the project, signals each cancel function.
Returns the count of tasks actually signalled. Tasks that completed between the
snapshot and the signal are skipped. Unknown project returns `(0, nil)` — not
an error.

**Best-effort, snapshot race.** Between listing tasks and signalling, some tasks
may complete. This is intentional and safe — cancellation is advisory.

---

## QualityGate Rules

The quality gate runs after every `Execute` call, including retries.

| Condition | Decision | Retry? |
|---|---|---|
| Content is empty (after TrimSpace) | reject | yes |
| Content matches rate-limit pattern | reject | yes |
| Last N results are Jaccard-similar | reject (thrashing) | no |
| None of the above | accept | — |

Rate-limit patterns detected: `rate limit`, `rate_limit`, `too many requests`,
`429`, `quota exceeded`, `throttled` (case-insensitive).

Thrashing uses a sliding window of 3 results with Jaccard word-similarity
threshold 0.8. When the last 3 results are all ≥ 0.8 similar, the task fails
without retry to prevent infinite loops.

---

## Crash Recovery

`RecoverCrashed()` scans the store for tasks in `dispatched` or `running` state
and marks them `failed_crash`. It returns the count of affected tasks.

Call once on daemon startup before accepting requests. See [RECOVERY.md](RECOVERY.md).

---

## Logging — Canonical Fields (stable)

Every significant `deps.Logger` call emits a subset of these 8 canonical fields.
Field names are stable across minor versions. New fields may be added; existing
fields will not be renamed without a deprecation cycle.

| Field | Type | Description |
|---|---|---|
| `module` | string | Always `"loom"` |
| `task_id` | string | Task UUID |
| `project_id` | string | Project/tenant identifier |
| `worker_type` | string | WorkerType string value |
| `task_status` | string | TaskStatus at time of log |
| `duration_ms` | int64 | Duration in milliseconds |
| `error_code` | string | Machine-readable error category |
| `request_id` | string | Distributed tracing ID from context |

Error log entries MUST include `error_code` and the `error` message field.
Info log entries include all applicable fields except `error_code`.

---

## Metrics — OTel Instruments (stable)

All instruments are registered via the injected `deps.Meter`. When `WithMeter`
is not called, a noop meter is used and all emissions are zero-cost.

All instruments carry `worker_type` and `project_id` attributes.

| Instrument | Kind | Description |
|---|---|---|
| `loom.tasks.submitted` | Int64Counter | Incremented on every successful Submit |
| `loom.tasks.completed` | Int64Counter | Incremented when task reaches completed |
| `loom.tasks.failed` | Int64Counter | Incremented on failed or failed_crash |
| `loom.tasks.cancelled` | Int64Counter | Incremented per Cancel signal |
| `loom.gate.pass` | Int64Counter | Incremented on quality gate accept |
| `loom.gate.fail` | Int64Counter | Incremented on quality gate reject |
| `loom.submit.duration_ms` | Int64Histogram | Time from Submit entry to dispatch return |
| `loom.task.duration_ms` | Int64Histogram | Time from dispatched_at to completed |

---

## Dependency Closure (NFR-1)

Only: stdlib + `github.com/google/uuid` + `go.opentelemetry.io/otel/metric`
(API-only, no SDK) + `modernc.org/sqlite` (pure-Go, no CGO).
No MCP SDK, no aimux server packages, no external HTTP clients.
