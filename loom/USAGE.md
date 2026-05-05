# Loom Engine Consumer Guide

`loom` is a standalone Go library for durable background task execution. It is
the task-state engine used by aimux, but the module is intentionally split at
`github.com/thebtf/aimux/loom` so other Go programs can use it without importing
the aimux MCP server.

This guide is for future library consumers who need to understand what Loom is,
when to use it, and which API surface is stable enough to build against.

## What It Is

Loom is a persistent task mediator:

1. Callers submit `TaskRequest` values.
2. Loom stores each task in SQLite.
3. A registered `Worker` executes the task in a background goroutine.
4. Loom persists status, result, error, retries, progress, and timestamps.
5. Subscribers receive lifecycle events.
6. Startup recovery marks interrupted in-flight tasks as `failed_crash`.

The caller's context is used for submission and tracing metadata. It does not
own the worker lifetime. A disconnected HTTP client, CLI session, or MCP
transport does not cancel a task that Loom already dispatched.

## What It Is For

Use Loom when you need a small in-process task engine with durable state:

- Long-running CLI, subprocess, or API calls that must outlive the caller.
- Local daemon job tracking with crash recovery.
- Multi-tenant task isolation inside one service process.
- Worker progress tails for status endpoints or operator dashboards.
- A reusable worker abstraction that can wrap subprocesses, HTTP APIs, or pure
  Go functions.
- Tests that need deterministic task IDs, clocks, and worker outcomes.

Loom is not a distributed queue or a broker. It does not provide cross-host
leader election, exactly-once side-effect semantics, or external deduplication.
If a worker sends emails, charges cards, or writes to remote APIs, design the
worker's own idempotency keys before resubmitting crashed tasks.

## Installation

```powershell
go get github.com/thebtf/aimux/loom@v0.2.0
```

The repository tag is `loom/v0.2.0`; Go consumers request it as `@v0.2.0`.
APIs documented here as current source but not yet present in the latest Loom
tag should be consumed by commit pin until the next `loom/v*` tag is cut.
Use Go 1.25.9 or newer for production builds.

## Minimal Lifecycle

```go
db, err := sql.Open("sqlite", "tasks.db?_pragma=journal_mode(WAL)")
if err != nil {
    return err
}
defer db.Close()

engine, err := loom.NewEngine(db, "my-daemon",
    loom.WithLogger(logger),
    loom.WithMeter(meter),
)
if err != nil {
    return err
}
defer engine.Close(context.Background())

if recovered, err := engine.RecoverCrashed(); err != nil {
    return err
} else if recovered > 0 {
    logger.WarnContext(ctx, "loom crash recovery marked tasks failed_crash",
        "count", recovered)
}

engine.RegisterWorker(loom.WorkerTypeCLI, myWorker)

taskID, err := engine.Submit(ctx, loom.TaskRequest{
    WorkerType: loom.WorkerTypeCLI,
    ProjectID:  "project-a",
    Prompt:     "run the job",
})
if err != nil {
    return err
}

task, err := engine.Get(taskID)
```

`engineName` is required. It scopes `List`, `Count`, crash recovery, and admin
operations when several daemons share the same SQLite database.

## Common Use Cases

### Durable Subprocess Jobs

Wrap `workers.SubprocessBase` when a task maps to an executable. The resolver
turns a `Task` into a command, args, environment, working directory, and stdin.
Loom records the final result and stores worker errors.

### HTTP API Jobs

Use `workers.HTTPBase` when a task maps to an outbound HTTP request. It handles
retry/backoff for network and 5xx failures. Consumer code owns request shaping,
authentication, and response interpretation.

### Progress Tails

Wrap a worker with `workers.StreamingBase` and set `Sink: engine` to persist
progress lines. Loom updates:

- `Task.LastOutputLine`
- `Task.ProgressLines`
- `Task.ProgressUpdatedAt`
- `EventTaskProgress`

Progress is best-effort. Sink errors are logged but do not fail the worker.

### Multi-Tenant In-Process Isolation

Use `NewTenantScopedEngine(engine, tenantID, quota)` when callers from different
tenants share one underlying engine. The wrapper injects `TenantID` on submit,
filters `Get` and `List`, verifies ownership before `Cancel`, and returns
`ErrTaskNotFound` for foreign tasks so callers cannot infer their existence.

`TenantQuotaConfig` can cap active tasks per tenant. When quota rejects a
submission, Loom returns `ErrLoomQuotaExceeded` and emits an `AuditEvent` through
the configured `AuditEmitter`.

### Crash Recovery

Call `RecoverCrashed()` once during daemon startup before registering workers or
accepting new submissions. It marks tasks left in `dispatched` or `running` as
`failed_crash`.

Do not blindly resubmit `failed_crash` tasks. They may have partially executed.
Resubmit only when the worker is idempotent or external evidence proves that the
side effect did not happen.

## API Map

### Constructors

- `NewTaskStore(db *sql.DB, engineName string) (*TaskStore, error)` creates the
  SQLite-backed store and applies migrations.
- `New(store *TaskStore, opts ...Option) *LoomEngine` creates an engine from a
  pre-built store.
- `NewEngine(db *sql.DB, engineName string, opts ...Option) (*LoomEngine, error)`
  is the preferred constructor for consumers.
- `NewTenantScopedEngine(engine *LoomEngine, tenantID string, quota *TenantQuotaConfig) *TenantScopedLoomEngine`
  wraps an existing engine for tenant isolation.

### Engine Operations

- `Submit(ctx, TaskRequest) (string, error)` persists and dispatches a task.
- `Get(taskID string) (*Task, error)` reads current task state.
- `List(projectID string, statuses ...TaskStatus) ([]*Task, error)` lists tasks
  for one project, scoped to the engine.
- `ListEngine(statuses ...TaskStatus) ([]*Task, error)` lists all tasks owned
  by this engine.
- `ListAll(statuses ...TaskStatus) ([]*Task, error)` lists tasks across all
  engines sharing the database.
- `Count(TaskFilter) (int, error)` returns an engine-scoped SQL count.
- `CountAll() (int, error)` returns a cross-engine SQL count.
- `Cancel(taskID string) error` signals one running task.
- `CancelAllForProject(projectID string) (int, error)` signals running tasks
  for a project.
- `RecoverCrashed() (int, error)` marks interrupted dispatched/running tasks
  as `failed_crash`.
- `Close(ctx context.Context) error` stops accepting submissions and waits for
  dispatch goroutines before the caller closes the database.
- `AppendProgress(taskID, line string) error` records one progress line for a
  running task.
- `FailActive`, `FailActiveByProject`, `FailActiveAll`, and `FailStaleRunning`
  support administrative kill, shutdown, and stale-task cleanup paths.

### Worker API

```go
type Worker interface {
    Execute(ctx context.Context, task *Task) (*WorkerResult, error)
    Type() WorkerType
}
```

`Execute` runs in a background goroutine with a task-scoped context. Return a
non-nil error for permanent worker failure. Return a `WorkerResult` with
non-empty `Content` for success. Empty content and rate-limit-like content are
handled by the quality gate and can trigger retries.

### Task Types

- `TaskRequest` is the submission input.
- `Task` is the persisted state record.
- `TaskStatus` is the state-machine enum.
- `TaskEvent` is the event bus payload.
- `WorkerResult` is the worker output.
- `TaskFilter` filters `Count`.
- `TenantQuotaConfig`, `AuditEvent`, and `AuditEmitter` configure tenant quota
  rejection handling.

### Options

- `WithLogger(deps.Logger)`
- `WithClock(deps.Clock)`
- `WithIDGenerator(deps.IDGenerator)`
- `WithMeter(deps.Meter)`
- `WithMaxRetries(int)`

Omit options to use safe defaults: noop logger, system clock, UUID generator,
noop meter, and two max retries.

### Events

Subscribe through `engine.Events().Subscribe(handler)`. Delivery is synchronous
on the emitting goroutine, so handlers must return quickly.

Event types:

- `task.created`
- `task.dispatched`
- `task.running`
- `task.completed`
- `task.failed`
- `task.failed_crash`
- `task.retrying`
- `task.cancelled`
- `task.progress`

### Observability

Loom exposes OTel metric instruments through the injected `deps.Meter` and
structured logs through `deps.Logger`. Metrics use `worker_type` and
`project_id` attributes. Logs use canonical fields such as `module`, `task_id`,
`project_id`, `worker_type`, `task_status`, `request_id`, `duration_ms`,
`error_code`, and `error`.

## Operational Rules

- Call `RecoverCrashed()` exactly once during startup.
- Call `Close(ctx)` before closing the underlying database.
- Keep event subscribers fast.
- Use different `WorkerType` values for different worker implementations.
- Treat `failed_crash` as an operator decision point, not an auto-retry signal.
- Use tenant-scoped wrappers for multi-tenant callers.
- Keep external side effects idempotent if tasks may be manually resubmitted.

## Related Documents

- [README.md](README.md) - quick start and dependency overview.
- [CONTRACT.md](CONTRACT.md) - state machine and stability contract.
- [PLAYBOOK.md](PLAYBOOK.md) - recipes for common workers and patterns.
- [RECOVERY.md](RECOVERY.md) - operator playbook for terminal states.
- [TESTING.md](TESTING.md) - deterministic tests and integration patterns.
- [CHANGELOG.md](CHANGELOG.md) - versioned API changes.
