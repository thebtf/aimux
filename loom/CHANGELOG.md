# Changelog

All notable changes to this module will be documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [v0.1.0] — TBD (unreleased)

Initial release. The full public API is listed below.

### Public API

#### Engine

- `loom.New(store *TaskStore, opts ...Option) *LoomEngine`
  Create engine from a pre-built TaskStore.

- `loom.NewEngine(db *sql.DB, opts ...Option) (*LoomEngine, error)`
  Create engine from a raw `*sql.DB`. Preferred constructor.

- `(*LoomEngine).Submit(ctx context.Context, req TaskRequest) (string, error)`
  Submit a task; returns task ID. Non-blocking — dispatch happens in background.

- `(*LoomEngine).Get(taskID string) (*Task, error)`
  Retrieve current task state from the store.

- `(*LoomEngine).List(projectID string, statuses ...TaskStatus) ([]*Task, error)`
  List tasks for a project, optionally filtered by one or more statuses.

- `(*LoomEngine).Cancel(taskID string) error`
  Signal cancellation for a single running task.

- `(*LoomEngine).CancelAllForProject(projectID string) (int, error)`
  Signal cancellation for all running tasks belonging to a project. Returns count
  of tasks signalled.

- `(*LoomEngine).RecoverCrashed() (int, error)`
  Mark dispatched and running tasks as failed_crash. Call once on daemon startup.

- `(*LoomEngine).RegisterWorker(wt WorkerType, w Worker)`
  Register a worker for a given WorkerType.

- `(*LoomEngine).Events() *EventBus`
  Return the event bus for subscribing to task lifecycle events.

#### EventBus

- `(*EventBus).Subscribe(handler func(TaskEvent)) func()`
  Register a lifecycle event handler. Returns an unsubscribe function.

- `(*EventBus).Emit(e TaskEvent)`
  Deliver an event to all registered subscribers (called internally by engine).

#### Types

- `type Task struct` — unit of work with ID, Status, WorkerType, ProjectID,
  RequestID, Prompt, CWD, Env, CLI, Role, Model, Effort, Timeout, Metadata,
  Result, Error, Retries, CreatedAt, DispatchedAt, CompletedAt.

- `type TaskRequest struct` — input for Submit. Fields: WorkerType, ProjectID,
  RequestID, Prompt, CWD, Env, CLI, Role, Model, Effort, Timeout, Metadata.

- `type TaskEvent struct` — lifecycle event. Fields: Type, TaskID, ProjectID,
  RequestID, Status, Timestamp. All six fields always populated.

- `type WorkerResult struct` — output from Execute. Fields: Content, Metadata,
  DurationMS.

- `type TaskStatus string` — state machine value.

- `type WorkerType string` — identifies which registered worker handles a task.

- `type EventType string` — lifecycle event type.

- `type GateDecision struct` — quality gate verdict. Fields: Accept, Reason, Retry.

- `type Option func(*LoomEngine)` — functional option for engine construction.

- `type QualityGateOption func(*QualityGate)` — functional option for gate.

#### TaskStatus Constants

- `TaskStatusPending` — created, not yet dispatched
- `TaskStatusDispatched` — dispatch goroutine launched
- `TaskStatusRunning` — Execute in progress
- `TaskStatusCompleted` — accepted by quality gate (terminal)
- `TaskStatusFailed` — worker error or gate reject without retry (terminal)
- `TaskStatusFailedCrash` — process died while task in-flight (terminal)
- `TaskStatusRetrying` — gate rejected, re-dispatching

#### WorkerType Constants

- `WorkerTypeCLI` — subprocess-based CLI worker
- `WorkerTypeThinker` — structured reasoning worker
- `WorkerTypeInvestigator` — investigation session worker
- `WorkerTypeOrchestrator` — multi-model orchestration worker

#### EventType Constants

- `EventTaskCreated` — `"task.created"`
- `EventTaskDispatched` — `"task.dispatched"`
- `EventTaskRunning` — `"task.running"`
- `EventTaskCompleted` — `"task.completed"`
- `EventTaskFailed` — `"task.failed"`
- `EventTaskFailedCrash` — `"task.failed_crash"`
- `EventTaskRetrying` — `"task.retrying"`
- `EventTaskCancelled` — `"task.cancelled"`

#### Options

- `WithLogger(l deps.Logger) Option` — inject a custom logger
- `WithClock(c deps.Clock) Option` — inject a custom clock
- `WithIDGenerator(g deps.IDGenerator) Option` — inject a custom ID generator
- `WithMeter(m deps.Meter) Option` — inject a custom OTel meter
- `WithMaxRetries(n int) Option` — set max quality-gate retry count (default 2)

#### QualityGate

- `NewQualityGate() *QualityGate` — default gate (threshold=0.8, window=3)
- `NewQualityGateWithOpts(opts ...QualityGateOption) *QualityGate`
- `(*QualityGate).Check(task *Task, result *WorkerResult) GateDecision`
- `(*QualityGate).Clear(taskID string)` — release history for a task
- `WithThreshold(t float64) QualityGateOption`
- `WithWindowSize(n int) QualityGateOption`

#### TaskStore

- `NewTaskStore(db *sql.DB) (*TaskStore, error)`
- `(*TaskStore).Create(task *Task) error`
- `(*TaskStore).Get(id string) (*Task, error)`
- `(*TaskStore).List(projectID string, statuses ...TaskStatus) ([]*Task, error)`
- `(*TaskStore).UpdateStatus(id string, from, to TaskStatus) error`
- `(*TaskStore).SetResult(id string, result string, errMsg string) error`
- `(*TaskStore).IncrementRetries(id string) error`
- `(*TaskStore).MarkCrashed() (int, error)`

#### Context Helpers

- `WithRequestID(ctx context.Context, requestID string) context.Context`
  Attach a request tracing ID to a context before Submit.

- `RequestIDFrom(ctx context.Context) string`
  Extract the request tracing ID from a context.

- `var RequestIDKey` — exported context key constant.

- `TaskStatus.CanTransitionTo(target TaskStatus) bool`
- `TaskStatus.IsTerminal() bool`

#### deps Package (`loom/deps`)

Interfaces:

- `type Logger interface` — DebugContext, InfoContext, WarnContext, ErrorContext
- `type Clock interface` — Now, Sleep
- `type IDGenerator interface` — NewID
- `type Meter interface` — Float64Histogram, Int64Counter, Int64Histogram,
  Int64UpDownCounter

Production defaults:

- `NoopLogger() Logger`
- `SystemClock() Clock`
- `UUIDGenerator() IDGenerator`
- `NoopMeter() Meter`

Test helpers:

- `type FakeClock struct` — frozen time with Advance(d)
- `NewFakeClock(t time.Time) *FakeClock`
- `type SequentialIDGenerator struct` — counter-based IDs
- `NewSequentialIDGenerator() *SequentialIDGenerator`

#### workers Package (`loom/workers`)

Subprocess:

- `type SubprocessBase struct` — composable subprocess worker base
- `type SubprocessSpawn struct` — spawn descriptor (Command, Args, CWD, Env, Stdin, Meta)
- `type SpawnResolver interface` — Resolve(ctx, task) (SubprocessSpawn, error)
- `type SubprocessRunner interface` — Run(ctx, spawn) (stdout, exitCode, error)
- `DefaultRunner() SubprocessRunner` — os/exec backend

HTTP:

- `type HTTPBase struct` — composable HTTP worker base
- `type HTTPRequest struct` — request descriptor (Method, URL, Headers, Body)
- `type HTTPResolver interface` — Resolve(ctx, task) (HTTPRequest, error)
- `NewHTTPBase(r HTTPResolver) *HTTPBase` — default 2 retries, 500ms backoff

Streaming:

- `type StreamingBase struct` — wraps any Worker with line-by-line progress
- `type ProgressHandler func(line string)` — per-line callback

### Metrics (v0.1.0)

Eight OTel instruments, all labeled `worker_type` + `project_id`:

`loom.tasks.submitted`, `loom.tasks.completed`, `loom.tasks.failed`,
`loom.tasks.cancelled`, `loom.gate.pass`, `loom.gate.fail`,
`loom.submit.duration_ms`, `loom.task.duration_ms`

### Canonical Log Fields (v0.1.0)

Eight structured fields on significant operations:

`module`, `task_id`, `project_id`, `worker_type`, `task_status`,
`duration_ms`, `error_code`, `request_id`
