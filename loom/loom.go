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
// internally and returns the engine. This is the v0.1.0-aligned constructor
// from spec FR-6 — New(store, opts) remains for backwards compatibility with
// aimux call sites and will be removed during Phase 3 atomic migration.
func NewEngine(db *sql.DB, opts ...Option) (*LoomEngine, error) {
	if db == nil {
		return nil, fmt.Errorf("loom: db must not be nil")
	}
	store, err := NewTaskStore(db)
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

// Submit creates a persistent task and dispatches to the appropriate worker.
// Returns immediately with taskID. Execution happens in a background goroutine.
// RequestID is extracted from ctx via RequestIDFrom for distributed tracing.
// After Close has been called Submit returns ErrEngineClosed without side effects.
func (l *LoomEngine) Submit(ctx context.Context, req TaskRequest) (string, error) {
	if l.closed.Load() {
		return "", ErrEngineClosed
	}
	submitStart := l.clock.Now()

	reqID := RequestIDFrom(ctx)
	// Allow explicit override in request (e.g. from non-loom callers).
	if reqID == "" {
		reqID = req.RequestID
	}

	taskID := l.idGen.NewID()
	now := l.clock.Now().UTC()
	task := &Task{
		ID:         taskID,
		Status:     TaskStatusPending,
		WorkerType: req.WorkerType,
		ProjectID:  req.ProjectID,
		RequestID:  reqID,
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
	// WaitGroup is incremented BEFORE spawning so Close can safely wait for drain.
	l.wg.Add(1)
	go l.dispatch(task)

	return task.ID, nil
}

// Close signals engine shutdown and waits for all in-flight dispatch goroutines
// to complete (or ctx to expire). Callers MUST invoke Close before closing the
// underlying *sql.DB to prevent write-after-close races. Close is idempotent:
// subsequent invocations return nil immediately.
//
// After Close returns, Submit will reject new tasks with ErrEngineClosed.
// In-flight tasks that are already running continue to completion until either
// they finish naturally or ctx expires — whichever comes first.
func (l *LoomEngine) Close(ctx context.Context) error {
	if !l.closed.CompareAndSwap(false, true) {
		return nil // already closed
	}
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

// Events returns the event bus for subscribing to task lifecycle events.
func (l *LoomEngine) Events() *EventBus {
	return l.events
}

// failTask is a best-effort helper that marks a task as failed in the store
// and emits EventTaskFailed. Errors from store operations are logged but not
// returned — the caller is typically already handling a failure path.
// task is passed directly so the helper avoids an additional DB round-trip and
// emits a fully-populated TaskEvent regardless of store availability.
func (l *LoomEngine) failTask(task *Task, fromStatus TaskStatus, errMsg string) {
	ctx := context.Background()
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
	// Paired with the l.wg.Add(1) performed by Submit before `go l.dispatch(task)`.
	defer l.wg.Done()
	// Panic recovery: ensure any panic in worker or gate is caught, task is
	// marked failed_crash, and the process is not terminated.
	defer func() {
		if r := recover(); r != nil {
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
