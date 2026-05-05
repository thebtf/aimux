package loom

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/thebtf/aimux/loom/deps"
)

// ErrEngineClosed is returned by Submit when the engine has been shut down
// via Close. It is a sentinel error — callers can compare against it with
// errors.Is to distinguish graceful shutdown from other failures.
var ErrEngineClosed = errors.New("loom: engine closed")

// ErrTaskNotFound is returned by tenant-scoped Get/Cancel when the task does
// not exist OR belongs to a different tenant. Both cases return 404 semantics
// (CHK079 fix: never reveal task existence to a foreign tenant via 403 distinction).
var ErrTaskNotFound = errors.New("loom: task not found")

// ErrLoomQuotaExceeded is returned by TenantScopedLoomEngine.Submit when the
// tenant's in-flight task count (pending+dispatched+running) reaches the
// configured MaxLoomTasksQueued limit (T060 / FR-17).
var ErrLoomQuotaExceeded = errors.New("loom: quota exceeded: too many in-flight tasks for tenant")

// LegacyTenantID is the tenant_id value used for tasks created before AIMUX-12
// multi-tenant isolation was deployed, or when no tenants.yaml is present
// (single-tenant legacy mode). This constant matches the SQL column default
// '__legacy__' in migrateV4Columns (ADR-011).
const LegacyTenantID = "__legacy__"

// Option configures LoomEngine.
type Option func(*LoomEngine)

// WithMaxRetries sets the maximum retry count (default 2).
func WithMaxRetries(n int) Option {
	return func(l *LoomEngine) { l.maxRetries = n }
}

// LoomEngine is the central task mediator.
// All tool handler work flows through LoomEngine which owns task creation,
// dispatch, execution, persistence, and delivery.
type LoomEngine struct {
	store      *TaskStore
	gate       *QualityGate
	events     *EventBus
	workers    map[WorkerType]Worker
	cancels    map[string]context.CancelFunc
	mu         sync.RWMutex
	maxRetries int
	logger     deps.Logger
	clock      deps.Clock
	idGen      deps.IDGenerator
	meter      deps.Meter
	// Lifecycle: wg tracks in-flight dispatch goroutines; closed signals that
	// the engine has been shut down via Close and no further Submit calls are
	// accepted. Both fields are zero-valued by default and require no explicit
	// initialisation.
	wg     sync.WaitGroup
	closed atomic.Bool
	// W2 (AIMUX-12 v5.1.0): per-tenant submit serialization. Closes the TOCTOU
	// race where N concurrent Submits all read depth=cap-1, all pass quota
	// check, all insert → cap exceeded by goroutine count. The lock serializes
	// quota-check + insert per tenant. Different tenants remain parallel.
	tenantSubmitLocks sync.Map // tenantID string → *sync.Mutex
	// T030 instruments — initialised in New() after options are applied.
	taskSubmittedCounter otelmetric.Int64Counter
	taskCompletedCounter otelmetric.Int64Counter
	taskFailedCounter    otelmetric.Int64Counter
	taskCancelledCounter otelmetric.Int64Counter
	gatePassCounter      otelmetric.Int64Counter
	gateFailCounter      otelmetric.Int64Counter
	submitDurationHist   otelmetric.Int64Histogram
	taskDurationHist     otelmetric.Int64Histogram
}

// New creates a LoomEngine with the given store and options.
// Dep fields (logger, clock, idGen, meter) are initialised to their noop/system
// defaults before Options are applied so callers that omit an option get a safe default.
// EventBus is created AFTER options so it receives the final (possibly injected) logger.
func New(store *TaskStore, opts ...Option) *LoomEngine {
	l := &LoomEngine{
		store:      store,
		gate:       NewQualityGate(),
		workers:    make(map[WorkerType]Worker),
		cancels:    make(map[string]context.CancelFunc),
		maxRetries: 2,
		logger:     deps.NoopLogger(),
		clock:      deps.SystemClock(),
		idGen:      deps.UUIDGenerator(),
		meter:      deps.NoopMeter(),
	}
	// Apply options FIRST so logger can be overridden before EventBus is created.
	for _, opt := range opts {
		opt(l)
	}
	// Create EventBus AFTER options so it gets the final logger.
	l.events = NewEventBus(l.logger)
	// Initialise T030 metric instruments. Errors are discarded because the noop
	// meter never errors and a real meter only errors on configuration mistakes
	// (bad name, duplicate registration) which cannot be recovered from at runtime.
	l.taskSubmittedCounter, _ = l.meter.Int64Counter("loom.tasks.submitted")
	l.taskCompletedCounter, _ = l.meter.Int64Counter("loom.tasks.completed")
	l.taskFailedCounter, _ = l.meter.Int64Counter("loom.tasks.failed")
	l.taskCancelledCounter, _ = l.meter.Int64Counter("loom.tasks.cancelled")
	l.gatePassCounter, _ = l.meter.Int64Counter("loom.gate.pass")
	l.gateFailCounter, _ = l.meter.Int64Counter("loom.gate.fail")
	l.submitDurationHist, _ = l.meter.Int64Histogram("loom.submit.duration_ms")
	l.taskDurationHist, _ = l.meter.Int64Histogram("loom.task.duration_ms")
	return l
}

// NewEngine constructs a LoomEngine from a raw *sql.DB. It creates a TaskStore
// internally and returns the engine. Prefer this constructor for normal
// consumers; use New(store, opts...) only when tests or advanced integrations
// need to inject a pre-built TaskStore.
//
// engineName identifies the owning daemon for per-daemon task scoping (AIMUX-10).
// It must not be empty; NewEngine returns an error if it is.
func NewEngine(db *sql.DB, engineName string, opts ...Option) (*LoomEngine, error) {
	if db == nil {
		return nil, fmt.Errorf("loom: db must not be nil")
	}
	store, err := NewTaskStore(db, engineName)
	if err != nil {
		return nil, fmt.Errorf("loom: new task store: %w", err)
	}
	return New(store, opts...), nil
}

// RegisterWorker registers a worker for a given worker type.
func (l *LoomEngine) RegisterWorker(wt WorkerType, w Worker) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.workers[wt] = w
}

// TenantSubmitLock returns the per-tenant submit Mutex used to serialize the
// quota-check + insert sequence for a tenant. Different tenants get
// independent Mutex instances (concurrency preserved across tenants).
//
// W2 (AIMUX-12 v5.1.0): closes the TOCTOU race in TenantScopedLoomEngine.Submit.
func (l *LoomEngine) TenantSubmitLock(tenantID string) *sync.Mutex {
	v, _ := l.tenantSubmitLocks.LoadOrStore(tenantID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Submit creates a persistent task and dispatches to the appropriate worker.
// Returns immediately with taskID. Execution happens in a background goroutine.
// RequestID is extracted from ctx via RequestIDFrom for distributed tracing.
// After Close has been called Submit returns ErrEngineClosed without side effects.
func (l *LoomEngine) Submit(ctx context.Context, req TaskRequest) (string, error) {
	// Protect the closed-check + wg.Add(1) as an atomic section to prevent a
	// race with Close. Without this lock, Close could call wg.Wait() between
	// the Load and the Add, which violates sync.WaitGroup's rule that Add must
	// happen-before Wait when the counter is zero.
	l.mu.Lock()
	if l.closed.Load() {
		l.mu.Unlock()
		return "", ErrEngineClosed
	}
	l.wg.Add(1)
	l.mu.Unlock()
	// goroutineLaunched tracks whether we successfully reach go l.dispatch(task).
	// If Submit returns early (error), we must call wg.Done() ourselves because
	// the dispatch goroutine (which owns the corresponding defer wg.Done()) was
	// never started.
	goroutineLaunched := false
	defer func() {
		if !goroutineLaunched {
			l.wg.Done()
		}
	}()
	submitStart := l.clock.Now()

	reqID := RequestIDFrom(ctx)
	// Allow explicit override in request (e.g. from non-loom callers).
	if reqID == "" {
		reqID = req.RequestID
	}

	taskID := l.idGen.NewID()
	now := l.clock.Now().UTC()
	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = LegacyTenantID
	}
	task := &Task{
		ID:         taskID,
		Status:     TaskStatusPending,
		WorkerType: req.WorkerType,
		ProjectID:  req.ProjectID,
		RequestID:  reqID,
		TenantID:   tenantID,
		Prompt:     req.Prompt,
		CWD:        req.CWD,
		Env:        req.Env,
		CLI:        req.CLI,
		Role:       req.Role,
		Model:      req.Model,
		Effort:     req.Effort,
		Timeout:    req.Timeout,
		Metadata:   req.Metadata,
		CreatedAt:  now,
	}

	if err := l.store.Create(task); err != nil {
		return "", fmt.Errorf("loom: persist task: %w", err)
	}

	l.events.Emit(TaskEvent{
		Type:      EventTaskCreated,
		TaskID:    task.ID,
		ProjectID: task.ProjectID,
		RequestID: task.RequestID,
		Status:    task.Status,
		Timestamp: l.clock.Now().UTC(),
	})

	// Transition pending → dispatched synchronously before launching goroutine.
	// This ensures crash recovery (MarkCrashed) can pick up the task even if the
	// process dies before the goroutine runs.
	if err := l.store.UpdateStatus(task.ID, TaskStatusPending, TaskStatusDispatched); err != nil {
		return "", fmt.Errorf("loom: dispatch task: %w", err)
	}
	task.Status = TaskStatusDispatched
	l.events.Emit(TaskEvent{
		Type:      EventTaskDispatched,
		TaskID:    task.ID,
		ProjectID: task.ProjectID,
		RequestID: task.RequestID,
		Status:    task.Status,
		Timestamp: l.clock.Now().UTC(),
	})

	// T030: emit submit metrics after successful dispatch transition.
	attrs := otelmetric.WithAttributes(
		attribute.String("worker_type", string(task.WorkerType)),
		attribute.String("project_id", task.ProjectID),
	)
	submitDurationMS := l.clock.Now().Sub(submitStart).Milliseconds()
	l.submitDurationHist.Record(ctx, submitDurationMS, attrs)
	l.taskSubmittedCounter.Add(ctx, 1, attrs)

	l.logger.InfoContext(ctx, "task submitted",
		"module", "loom",
		"task_id", task.ID,
		"project_id", task.ProjectID,
		"worker_type", string(task.WorkerType),
		"task_status", string(task.Status),
		"request_id", task.RequestID,
	)

	// Dispatch in background — task lifecycle independent of caller context.
	// wg.Add(1) was already called at the top of Submit (under mu) to eliminate
	// the race window between closed.Load() and the Add. Mark goroutineLaunched
	// before the go statement so the deferred fallback wg.Done() is suppressed —
	// dispatch's own defer wg.Done() takes ownership from here.
	goroutineLaunched = true
	go l.dispatch(task)

	return task.ID, nil
}

// Close signals engine shutdown and waits for all in-flight dispatch goroutines
// to complete (or ctx to expire). Callers MUST invoke Close before closing the
// underlying *sql.DB to prevent write-after-close races. Close is idempotent:
// subsequent invocations return nil immediately.
//
// After Close returns, Submit will reject new tasks with ErrEngineClosed.
// In-flight dispatch goroutines already running continue until they finish
// naturally. ctx is used only as a deadline on how long Close will wait for
// them — it does NOT cancel the tasks themselves. Use Cancel or
// CancelAllForProject before Close if you need to abort in-flight work.
func (l *LoomEngine) Close(ctx context.Context) error {
	// Hold mu while flipping closed so no Submit goroutine can slip a wg.Add(1)
	// in between our CAS and the subsequent wg.Wait(). Without this, a Submit
	// that passed the closed.Load() check could call wg.Add(1) after wg.Wait()
	// starts, violating the WaitGroup contract.
	l.mu.Lock()
	if !l.closed.CompareAndSwap(false, true) {
		l.mu.Unlock()
		return nil // already closed
	}
	l.mu.Unlock()
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Get returns current task state.
func (l *LoomEngine) Get(taskID string) (*Task, error) {
	return l.store.Get(taskID)
}

// List returns tasks for a project, optionally filtered by status.
func (l *LoomEngine) List(projectID string, statuses ...TaskStatus) ([]*Task, error) {
	return l.store.List(projectID, statuses...)
}

// ListAll returns tasks across all engines and projects, optionally filtered by status.
// Used for cross-daemon diagnostic views (AIMUX-10 FR-5).
func (l *LoomEngine) ListAll(statuses ...TaskStatus) ([]*Task, error) {
	return l.store.ListAll(statuses...)
}

// Cancel requests cancellation of a running task.
func (l *LoomEngine) Cancel(taskID string) error {
	l.mu.RLock()
	cancel, ok := l.cancels[taskID]
	l.mu.RUnlock()

	if !ok {
		return fmt.Errorf("loom: task %s not cancellable (not running or not found)", taskID)
	}
	cancel()
	return nil
}

// CancelAllForProject cancels all running tasks for the given project.
// Returns the number of tasks signaled for cancellation. Tasks that are not
// currently running (pending, completed, failed, already cancelled) are not affected.
// Per US9: used by engram to cancel all work for a disconnecting project.
func (l *LoomEngine) CancelAllForProject(projectID string) (int, error) {
	// Snapshot running tasks first (avoid holding lock during store queries).
	running, err := l.store.List(projectID, TaskStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("loom: list running tasks: %w", err)
	}

	// Collect which tasks we actually cancelled (have a live cancel func).
	cancelled := make([]*Task, 0, len(running))

	l.mu.Lock()
	for _, task := range running {
		if cancel, ok := l.cancels[task.ID]; ok {
			cancel()
			cancelled = append(cancelled, task)
		}
	}
	l.mu.Unlock()

	// Emit TaskCancelled events and metrics for each cancelled task (outside the lock).
	cancelCtx := context.Background()
	for _, task := range cancelled {
		l.events.Emit(TaskEvent{
			Type:      EventTaskCancelled,
			TaskID:    task.ID,
			ProjectID: projectID,
			RequestID: task.RequestID,
			Status:    TaskStatusRunning, // will transition as context propagates
			Timestamp: l.clock.Now().UTC(),
		})
		// T030: cancelled task counter.
		l.taskCancelledCounter.Add(cancelCtx, 1, otelmetric.WithAttributes(
			attribute.String("worker_type", string(task.WorkerType)),
			attribute.String("project_id", task.ProjectID),
		))
		l.logger.InfoContext(cancelCtx, "task cancelled",
			"module", "loom",
			"task_id", task.ID,
			"project_id", task.ProjectID,
			"worker_type", string(task.WorkerType),
			"task_status", string(TaskStatusRunning),
			"request_id", task.RequestID,
		)
	}

	return len(cancelled), nil
}

// RecoverCrashed marks all dispatched/running tasks as failed_crash.
// Called once on daemon startup.
func (l *LoomEngine) RecoverCrashed() (int, error) {
	return l.store.MarkCrashed()
}

// Import upserts a historical task into Loom persistence without dispatching it.
// Used by aimux startup migration for legacy job rows and WAL entries.
func (l *LoomEngine) Import(task *Task) error {
	return l.store.Import(task)
}

// Events returns the event bus for subscribing to task lifecycle events.
func (l *LoomEngine) Events() *EventBus {
	return l.events
}

// AppendProgress records a single progress line for taskID and emits an
// EventTaskProgress on the event bus. Workers (or worker wrappers like
// workers.StreamingBase) call this for every line of live output so that
// status() readers see progress_tail / progress_lines / progress_updated_at
// at parity with the legacy job-progress contract (DEF-13 / AIMUX-16 CR-005).
//
// The line is UTF-8-safe truncated to ≤100 bytes by the store, with secrets
// scrubbed before storage. Errors from the store are propagated; the event
// is emitted only after a successful store update so subscribers never
// observe a delivered event whose state is missing from disk.
//
// taskID lookups for unknown / cancelled tasks are no-ops at the store
// layer (info.OK == false) and produce no event — this preserves the
// contract that EventTaskProgress fires only for state that survived the
// write, so multi-tenant subscribers filtering on ProjectID never receive
// orphan events for tasks that no longer exist.
//
// The emitted TaskEvent carries ProjectID and RequestID returned by the
// store (read atomically alongside the row update via UPDATE ... RETURNING)
// so subscribers can correlate progress with multi-tenant fanout filters
// and distributed tracing the same way they correlate lifecycle events.
func (l *LoomEngine) AppendProgress(taskID, line string) error {
	info, err := l.store.AppendProgress(taskID, line)
	if err != nil {
		return err
	}
	if !info.OK {
		// Unknown / cancelled task: no row was updated, so there is no state
		// for subscribers to observe. Suppress the event per CR-005 design.
		return nil
	}
	l.events.Emit(TaskEvent{
		Type:      EventTaskProgress,
		TaskID:    taskID,
		ProjectID: info.ProjectID,
		RequestID: info.RequestID,
		Status:    TaskStatusRunning,
		Timestamp: l.clock.Now().UTC(),
	})
	return nil
}

func (l *LoomEngine) isTerminalTask(taskID string) bool {
	current, err := l.store.Get(taskID)
	return err == nil && current.Status.IsTerminal()
}

// failTask is a best-effort helper that marks a task as failed in the store
// and emits EventTaskFailed. Errors from store operations are logged but not
// returned — the caller is typically already handling a failure path.
// task is passed directly so the helper avoids an additional DB round-trip and
// emits a fully-populated TaskEvent regardless of store availability.
func (l *LoomEngine) failTask(task *Task, fromStatus TaskStatus, errMsg string) {
	ctx := context.Background()
	if l.isTerminalTask(task.ID) {
		return
	}
	if err := l.store.SetResult(task.ID, "", errMsg); err != nil {
		l.logger.ErrorContext(ctx, "failTask: store.SetResult failed",
			"module", "loom",
			"task_id", task.ID,
			"project_id", task.ProjectID,
			"worker_type", string(task.WorkerType),
			"task_status", string(fromStatus),
			"request_id", task.RequestID,
			"error_code", "store_set_result",
			"error", err,
		)
	}
	if err := l.store.UpdateStatus(task.ID, fromStatus, TaskStatusFailed); err != nil {
		l.logger.ErrorContext(ctx, "failTask: store.UpdateStatus failed",
			"module", "loom",
			"task_id", task.ID,
			"project_id", task.ProjectID,
			"worker_type", string(task.WorkerType),
			"task_status", string(fromStatus),
			"request_id", task.RequestID,
			"error_code", "store_update_status",
			"error", err,
		)
	}
	l.events.Emit(TaskEvent{
		Type:      EventTaskFailed,
		TaskID:    task.ID,
		ProjectID: task.ProjectID,
		RequestID: task.RequestID,
		Status:    TaskStatusFailed,
		Timestamp: l.clock.Now().UTC(),
	})
	// T030: failed task counter.
	l.taskFailedCounter.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("worker_type", string(task.WorkerType)),
		attribute.String("project_id", task.ProjectID),
	))
	l.logger.ErrorContext(ctx, "task failed",
		"module", "loom",
		"task_id", task.ID,
		"project_id", task.ProjectID,
		"worker_type", string(task.WorkerType),
		"task_status", string(TaskStatusFailed),
		"request_id", task.RequestID,
		"error_code", "task_failed",
		"error", errMsg,
	)
}

// dispatch runs the worker for a task in a background goroutine.
// Task arrives already in TaskStatusDispatched (transitioned synchronously by Submit).
func (l *LoomEngine) dispatch(task *Task) {
	// Decrement the WaitGroup exactly once when this goroutine exits.
	// Paired with the l.wg.Add(1) performed at the start of Submit (under mu).
	defer l.wg.Done()
	// Panic recovery: ensure any panic in worker or gate is caught, task is
	// marked failed_crash, and the process is not terminated.
	defer func() {
		if r := recover(); r != nil {
			if l.isTerminalTask(task.ID) {
				return
			}
			panicCtx := context.Background()
			stack := debug.Stack()
			panicMsg := fmt.Sprintf("panic: %v", r)
			l.logger.ErrorContext(panicCtx, "dispatch panic",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", "unknown",
				"request_id", task.RequestID,
				"error_code", "dispatch_panic",
				"error", panicMsg,
				"stack", string(stack),
			)
			if err := l.store.SetResult(task.ID, "", panicMsg); err != nil {
				l.logger.ErrorContext(panicCtx, "dispatch panic: store.SetResult failed",
					"module", "loom",
					"task_id", task.ID,
					"project_id", task.ProjectID,
					"worker_type", string(task.WorkerType),
					"task_status", string(TaskStatusFailedCrash),
					"request_id", task.RequestID,
					"error_code", "store_set_result",
					"error", err,
				)
			}
			// Best-effort: try both running→failed_crash and dispatched→failed_crash
			// since we don't know exact current status at panic time.
			if err := l.store.UpdateStatus(task.ID, TaskStatusRunning, TaskStatusFailedCrash); err != nil {
				if err2 := l.store.UpdateStatus(task.ID, TaskStatusDispatched, TaskStatusFailedCrash); err2 != nil {
					l.logger.ErrorContext(panicCtx, "dispatch panic: could not mark failed_crash",
						"module", "loom",
						"task_id", task.ID,
						"project_id", task.ProjectID,
						"worker_type", string(task.WorkerType),
						"task_status", string(TaskStatusFailedCrash),
						"request_id", task.RequestID,
						"error_code", "failed_crash_transition",
						"error", fmt.Sprintf("running→failed_crash: %v; dispatched→failed_crash: %v", err, err2),
					)
				}
			}
			l.events.Emit(TaskEvent{
				Type:      EventTaskFailedCrash,
				TaskID:    task.ID,
				ProjectID: task.ProjectID,
				RequestID: task.RequestID,
				Status:    TaskStatusFailedCrash,
				Timestamp: l.clock.Now().UTC(),
			})
			// T030: panic counts as a failed task (failed_crash subtype).
			l.taskFailedCounter.Add(panicCtx, 1, otelmetric.WithAttributes(
				attribute.String("worker_type", string(task.WorkerType)),
				attribute.String("project_id", task.ProjectID),
			))
			l.logger.ErrorContext(panicCtx, "task failed crash",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", string(TaskStatusFailedCrash),
				"request_id", task.RequestID,
				"error_code", "dispatch_panic",
				"error", panicMsg,
			)
		}
	}()

	// Clear gate history for this task when dispatch finishes (memory cleanup).
	defer l.gate.Clear(task.ID)

	l.mu.RLock()
	worker, ok := l.workers[task.WorkerType]
	l.mu.RUnlock()

	if !ok {
		l.failTask(task, TaskStatusDispatched, fmt.Sprintf("no worker registered for type %q", task.WorkerType))
		return
	}

	// Transition: dispatched → running
	if err := l.store.UpdateStatus(task.ID, TaskStatusDispatched, TaskStatusRunning); err != nil {
		l.logger.ErrorContext(context.Background(), "dispatch: UpdateStatus(dispatched→running) failed",
			"module", "loom",
			"task_id", task.ID,
			"project_id", task.ProjectID,
			"worker_type", string(task.WorkerType),
			"task_status", string(TaskStatusDispatched),
			"request_id", task.RequestID,
			"error_code", "store_update_status",
			"error", err,
		)
		l.failTask(task, TaskStatusDispatched, fmt.Sprintf("dispatch: transition to running failed: %v", err))
		return
	}

	// Emit running event after successful dispatched→running transition.
	l.events.Emit(TaskEvent{
		Type:      EventTaskRunning,
		TaskID:    task.ID,
		ProjectID: task.ProjectID,
		RequestID: task.RequestID,
		Status:    TaskStatusRunning,
		Timestamp: l.clock.Now().UTC(),
	})

	// Create task-scoped context — NOT derived from caller's context.
	// FR-4: session disconnect does not cancel running tasks.
	var taskCtx context.Context
	var cancel context.CancelFunc

	if task.Timeout > 0 {
		taskCtx, cancel = context.WithTimeout(context.Background(), time.Duration(task.Timeout)*time.Second)
	} else {
		taskCtx, cancel = context.WithCancel(context.Background())
	}

	l.mu.Lock()
	l.cancels[task.ID] = cancel
	l.mu.Unlock()

	defer func() {
		cancel()
		l.mu.Lock()
		delete(l.cancels, task.ID)
		l.mu.Unlock()
	}()

	// Reload task from store to get latest state (in case of retry).
	latest, err := l.store.Get(task.ID)
	if err != nil {
		l.logger.ErrorContext(taskCtx, "dispatch: store.Get failed",
			"module", "loom",
			"task_id", task.ID,
			"project_id", task.ProjectID,
			"worker_type", string(task.WorkerType),
			"task_status", string(TaskStatusRunning),
			"request_id", task.RequestID,
			"error_code", "store_get",
			"error", err,
		)
		l.failTask(task, TaskStatusRunning, fmt.Sprintf("dispatch: reload task failed: %v", err))
		return
	}

	result, execErr := worker.Execute(taskCtx, latest)
	if l.isTerminalTask(task.ID) {
		return
	}
	if execErr != nil {
		l.failTask(task, TaskStatusRunning, execErr.Error())
		return
	}

	// Quality gate: validate result before accepting.
	// Retry loop: continues until gate accepts, retries exhausted, or non-retryable rejection.
	for {
		decision := l.gate.Check(latest, result)
		gateCtx := context.Background()
		attrs := otelmetric.WithAttributes(
			attribute.String("worker_type", string(task.WorkerType)),
			attribute.String("project_id", task.ProjectID),
		)
		if decision.Accept {
			// T030: gate pass counter.
			l.gatePassCounter.Add(gateCtx, 1, attrs)
			l.logger.InfoContext(gateCtx, "quality gate pass",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", string(TaskStatusRunning),
				"request_id", task.RequestID,
			)
			if err := l.store.SetResult(task.ID, result.Content, ""); err != nil {
				l.logger.ErrorContext(gateCtx, "dispatch complete: store.SetResult failed",
					"module", "loom",
					"task_id", task.ID,
					"project_id", task.ProjectID,
					"worker_type", string(task.WorkerType),
					"task_status", string(TaskStatusRunning),
					"request_id", task.RequestID,
					"error_code", "store_set_result",
					"error", err,
				)
				// Abort: do not emit EventTaskCompleted — persisted state is not completed.
				return
			}
			if err := l.store.UpdateStatus(task.ID, TaskStatusRunning, TaskStatusCompleted); err != nil {
				l.logger.ErrorContext(gateCtx, "dispatch complete: store.UpdateStatus failed",
					"module", "loom",
					"task_id", task.ID,
					"project_id", task.ProjectID,
					"worker_type", string(task.WorkerType),
					"task_status", string(TaskStatusRunning),
					"request_id", task.RequestID,
					"error_code", "store_update_status",
					"error", err,
				)
				// Abort: do not emit EventTaskCompleted — status transition failed.
				return
			}
			l.events.Emit(TaskEvent{
				Type:      EventTaskCompleted,
				TaskID:    task.ID,
				ProjectID: task.ProjectID,
				RequestID: task.RequestID,
				Status:    TaskStatusCompleted,
				Timestamp: l.clock.Now().UTC(),
			})
			// T030: completed task counter + end-to-end duration.
			l.taskCompletedCounter.Add(gateCtx, 1, attrs)
			var taskDurationMS int64
			if latest.DispatchedAt != nil {
				taskDurationMS = l.clock.Now().Sub(*latest.DispatchedAt).Milliseconds()
				l.taskDurationHist.Record(gateCtx, taskDurationMS, attrs)
			}
			// CR-MED-1 fix: include duration_ms in the canonical 8-field log.
			l.logger.InfoContext(gateCtx, "task completed",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", string(TaskStatusCompleted),
				"duration_ms", taskDurationMS,
				"request_id", task.RequestID,
			)
			return
		}

		// Gate rejected.
		// T030: gate fail counter.
		l.gateFailCounter.Add(gateCtx, 1, attrs)
		l.logger.InfoContext(gateCtx, "quality gate fail",
			"module", "loom",
			"task_id", task.ID,
			"project_id", task.ProjectID,
			"worker_type", string(task.WorkerType),
			"task_status", string(TaskStatusRunning),
			"request_id", task.RequestID,
			"reason", decision.Reason,
		)

		if !decision.Retry || latest.Retries >= l.maxRetries {
			// No retry or retries exhausted.
			l.failTask(task, TaskStatusRunning, fmt.Sprintf("gate rejected: %s", decision.Reason))
			return
		}

		// Retry: running → retrying → dispatched → running.
		// BUG-002 fix: each transition failure is now a hard stop. Previously the
		// errors were swallowed with log.Printf and execution continued on stale
		// state, leaving tasks permanently in `retrying`.
		if err := l.store.UpdateStatus(task.ID, TaskStatusRunning, TaskStatusRetrying); err != nil {
			l.logger.ErrorContext(gateCtx, "retry: UpdateStatus(running→retrying) failed",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", string(TaskStatusRunning),
				"request_id", task.RequestID,
				"error_code", "store_update_status",
				"error", err,
			)
			l.failTask(task, TaskStatusRunning, fmt.Sprintf("retry: UpdateStatus(running→retrying) failed: %v", err))
			return
		}
		if err := l.store.IncrementRetries(task.ID); err != nil {
			l.logger.ErrorContext(gateCtx, "retry: IncrementRetries failed",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", string(TaskStatusRetrying),
				"request_id", task.RequestID,
				"error_code", "store_increment_retries",
				"error", err,
			)
			l.failTask(task, TaskStatusRetrying, fmt.Sprintf("retry: IncrementRetries failed: %v", err))
			return
		}

		// Emit retrying event after successful transition.
		l.events.Emit(TaskEvent{
			Type:      EventTaskRetrying,
			TaskID:    task.ID,
			ProjectID: task.ProjectID,
			RequestID: task.RequestID,
			Status:    TaskStatusRetrying,
			Timestamp: l.clock.Now().UTC(),
		})

		if err := l.store.UpdateStatus(task.ID, TaskStatusRetrying, TaskStatusDispatched); err != nil {
			l.logger.ErrorContext(gateCtx, "retry: UpdateStatus(retrying→dispatched) failed",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", string(TaskStatusRetrying),
				"request_id", task.RequestID,
				"error_code", "store_update_status",
				"error", err,
			)
			l.failTask(task, TaskStatusRetrying, fmt.Sprintf("retry: UpdateStatus(retrying→dispatched) failed: %v", err))
			return
		}
		if err := l.store.UpdateStatus(task.ID, TaskStatusDispatched, TaskStatusRunning); err != nil {
			l.logger.ErrorContext(gateCtx, "retry: UpdateStatus(dispatched→running) failed",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", string(TaskStatusDispatched),
				"request_id", task.RequestID,
				"error_code", "store_update_status",
				"error", err,
			)
			l.failTask(task, TaskStatusDispatched, fmt.Sprintf("retry: UpdateStatus(dispatched→running) failed: %v", err))
			return
		}

		latest, err = l.store.Get(task.ID)
		if err != nil {
			l.logger.ErrorContext(taskCtx, "dispatch retry: store.Get failed",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", string(TaskStatusRunning),
				"request_id", task.RequestID,
				"error_code", "store_get",
				"error", err,
			)
			l.failTask(task, TaskStatusRunning, fmt.Sprintf("dispatch retry: reload task failed: %v", err))
			return
		}

		result, execErr = worker.Execute(taskCtx, latest)
		if execErr != nil {
			l.failTask(task, TaskStatusRunning, execErr.Error())
			return
		}
	}
}
