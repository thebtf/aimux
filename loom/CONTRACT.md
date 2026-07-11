# loom Contract

Formal specification for the current `loom` library API. Sections marked **stable**
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
  Submit caller's context. It is cancelled when `Cancel(taskID)` targets the
  task, when `CancelAllForProject(projectID)` selects its live execution, or
  when the task's `Timeout` expires.
- The worker MUST NOT retain `task` beyond the call — the engine may reuse or
  deallocate the struct after `Execute` returns.
- `WorkerResult.Content` MUST be non-empty (after trimming whitespace) for the
  quality gate to accept. Empty or whitespace-only strings trigger an automatic retry.
- `WorkerResult.Metadata` is optional. It is persisted and returned by `Get`.
- Returning a non-nil error normally marks the task `failed` immediately (no
  retry). If durable cancellation already won, the cancellation contract below
  decides the terminal outcome instead.

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

| From | Legal next states |
|---|---|
| `pending` | `dispatched`, `failed`, `cancelled` |
| `dispatched` | `running`, `failed`, `failed_crash`, `cancelling` |
| `running` | `completed`, `failed`, `failed_crash`, `retrying`, `input_required`, `cancelling` |
| `input_required` | `running`, `failed`, `failed_crash`, `cancelling` |
| `retrying` | `dispatched`, `failed`, `failed_crash`, `cancelling` |
| `cancelling` | `cancelled`, `failed_crash` |

`completed`, `failed`, `failed_crash`, and `cancelled` are terminal. No store
transition out of a terminal state is valid.

### Status Values

The ordinary success path is `pending → dispatched → running → completed`.
The quality gate can loop through `running → retrying → dispatched → running`
up to `maxRetries`; action-response workflows can use `input_required`.

Cancellation is durable state, not an in-memory signal. A pending task can move
directly to `cancelled` because no execution exists. An active
`dispatched|running|input_required|retrying` task first moves to `cancelling`;
terminal `cancelled` then requires valid stop evidence. If the current legacy
`Worker` path returns or crashes without that proof, Loom records
`failed_crash` with the `unverified_stop` error class instead of claiming the
process stopped.

---

## Engine Lifecycle

### Submit

`Submit(ctx, req)` is non-blocking. It:

1. Generates a task ID and atomically commits the `pending` row plus its
   `task.created` authority fact.
2. Emits `EventTaskCreated` after that commit.
3. Atomically commits `pending → dispatched` plus `task.dispatched` before
   returning.
4. Emits `EventTaskDispatched` after that commit.
5. Launches `go dispatch(task)` and returns the task ID.

The caller's context (`ctx`) is used ONLY for:
- Extracting `RequestIDKey` for distributed tracing.
- Emitting OTel metrics (counter/histogram record calls).

It is NOT used as the task's execution context. The task continues running even
if the caller's context is cancelled.

### Dispatch Goroutine

The dispatch goroutine runs with a task-scoped `context.Background()`-derived
context. It:

1. Atomically commits `dispatched → running` plus `task.running`, then emits
   `EventTaskRunning`.
2. Creates a cancel function stored in `l.cancels[taskID]`.
3. Calls `worker.Execute(taskCtx, task)`.
4. On success: runs the quality gate; on accept, atomically commits
   `running → completed` plus the terminal fact, then emits completion.
5. On gate reject with retry: atomically commits each
   `running → retrying → dispatched → running` step and its matching fact before
   emitting the corresponding event.
6. On gate reject without retry, or worker error: atomically commits `failed`
   and its terminal fact unless a cancellation intent already won.

Panics in `Execute` or the gate are recovered. The task is marked `failed_crash`.

### Context Independence (C4 Rule)

**Session disconnect does NOT cancel running tasks.** The task-scoped context
is derived from `context.Background()`, not from any HTTP request, MCP session,
or caller-provided context. Tasks survive connection drops.

Only these operations interrupt task execution:

- `engine.Cancel(taskID)` — durably requests cancellation for one eligible task,
  then signals its live execution when a stop is required.
- `engine.CancelAllForProject(projectID)` — does the same per snapshotted running
  task in the project.
- Task `Timeout` field expiry (if set) cancels the task context, but it is not an
  explicit `RequestCancel` and does not fabricate a `cancelled` outcome.

---

## Event Delivery Contract (FR-14)

### Subscribe

```go
unsubscribe := engine.Events().Subscribe(func(e TaskEvent) {
    // Handle event. MUST return quickly.
})
defer unsubscribe()
```

- Delivery is **synchronous** on the emitting goroutine.
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

### Event Types (11 values)

| EventType | When emitted |
|---|---|
| `task.created` | After task written to store (still pending) |
| `task.dispatched` | After pending→dispatched transition |
| `task.running` | After dispatched→running transition |
| `task.completed` | After running→completed transition |
| `task.failed` | After any →failed transition |
| `task.failed_crash` | After crash recovery, dispatch panic, or active cancellation without valid stop evidence |
| `task.retrying` | After running→retrying transition |
| `task.cancel_requested` | After an active cancellation intent commits `cancelling`, before signalling execution |
| `task.cancelled` | EventBus emission currently occurs only when `Cancel` commits a pending/no-stop cancellation; active `CommitCancelled` writes the durable authority artifact with this event type but does not project `EventTaskCancelled` |
| `task.progress` | After `AppendProgress` records a progress line for a running task |
| `task.artifacts_appended` | Payload-free wake-up after a runtime-event batch commits |

---

## Cancellation Semantics

### Cancel(taskID)

`Cancel` calls the canonical `RequestCancel` authority command before any
in-memory signal:

- `pending` commits directly to terminal `cancelled`, closes open actions,
  appends the durable `task.cancelled` authority artifact, and emits
  `EventTaskCancelled`; no process-stop proof is required because execution
  never started.
- `dispatched`, `running`, `input_required`, and `retrying` commit to
  `cancelling`, close open actions, and append `task.cancel_requested`. Loom
  emits the matching event after commit and only then invokes the captured
  cancel function when one exists.
- `cancelling` and terminal states reject the request as an authority conflict.

For an active task, durable `cancelled` truth and its `task.cancelled` authority
artifact require valid `StopEvidence` accepted by `CommitCancelled`. That store
commit currently has no production path that emits `EventTaskCancelled` on the
Loom EventBus. The current legacy `Worker` integration does not invent stop
proof: subscribers observe `task.cancel_requested`, then a worker return or
panic after the cancel intent wins commits and emits
`failed_crash`/`unverified_stop` instead.

### CancelAllForProject(projectID)

Takes a deterministic snapshot of running tasks and their live cancel functions.
For each candidate, it commits that task's `RequestCancel` intent before emitting
`task.cancel_requested` and signalling execution. A task whose authority commit
fails is never signalled. The return count is the number actually signalled;
per-task infrastructure errors are joined while later candidates continue.
Tasks that complete between snapshot and authority commit are skipped as
conflicts. An unknown project returns `(0, nil)`.

**Best-effort, snapshot race.** Between listing tasks and committing each intent,
some tasks may complete. This is safe because terminal truth wins and no signal
is sent without that task's durable cancel intent.

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

`RecoverCrashed()` captures one UTC recovery timestamp for the invocation, then
processes engine-owned tasks in deterministic creation/ID order from
`dispatched`, `running`, `input_required`, `retrying`, and `cancelling`.
Each task is committed independently through `CommitFailedCrash`, which writes
the terminal row, action fence, and one `task.failed_crash` authority fact before
the event is emitted. Conflicts are skipped; infrastructure errors are joined
while later tasks continue. The return count is the number of successful
authority commits, and a second invocation is idempotent.

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
| `loom.tasks.cancelled` | Int64Counter | Incremented when Loom directly terminalizes a pending task as cancelled |
| `loom.gate.pass` | Int64Counter | Incremented on quality gate accept |
| `loom.gate.fail` | Int64Counter | Incremented on quality gate reject |
| `loom.submit.duration_ms` | Int64Histogram | Time from Submit entry to dispatch return |
| `loom.task.duration_ms` | Int64Histogram | Time from dispatched_at to completed |

---

## Dependency Closure (NFR-1)

Only: stdlib + `github.com/google/uuid` + `go.opentelemetry.io/otel/metric`
(API-only, no SDK) + `modernc.org/sqlite` (pure-Go, no CGO).
No MCP SDK, no aimux server packages, no external HTTP clients.
