package loom

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

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
	events     *EventBus
	workers    map[WorkerType]Worker
	cancels    map[string]context.CancelFunc
	mu         sync.RWMutex
	maxRetries int
}

// New creates a LoomEngine with the given store and options.
func New(store *TaskStore, opts ...Option) *LoomEngine {
	l := &LoomEngine{
		store:      store,
		events:     NewEventBus(),
		workers:    make(map[WorkerType]Worker),
		cancels:    make(map[string]context.CancelFunc),
		maxRetries: 2,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// RegisterWorker registers a worker for a given worker type.
func (l *LoomEngine) RegisterWorker(wt WorkerType, w Worker) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.workers[wt] = w
}

// Submit creates a persistent task and dispatches to the appropriate worker.
// Returns immediately with taskID. Execution happens in a background goroutine.
func (l *LoomEngine) Submit(ctx context.Context, req TaskRequest) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}

	now := time.Now().UTC()
	task := &Task{
		ID:         id.String(),
		Status:     TaskStatusPending,
		WorkerType: req.WorkerType,
		ProjectID:  req.ProjectID,
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

	l.events.Emit(Event{Type: EventTaskCreated, TaskID: task.ID})

	// Dispatch in background — task lifecycle independent of caller context.
	go l.dispatch(task)

	return task.ID, nil
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

// RecoverCrashed marks all dispatched/running tasks as failed_crash.
// Called once on daemon startup.
func (l *LoomEngine) RecoverCrashed() (int, error) {
	return l.store.MarkCrashed()
}

// Events returns the event bus for subscribing to task lifecycle events.
func (l *LoomEngine) Events() *EventBus {
	return l.events
}

// dispatch runs the worker for a task in a background goroutine.
func (l *LoomEngine) dispatch(task *Task) {
	// Transition: pending → dispatched
	if err := l.store.UpdateStatus(task.ID, TaskStatusPending, TaskStatusDispatched); err != nil {
		l.events.Emit(Event{Type: EventTaskFailed, TaskID: task.ID, Data: map[string]any{"error": err.Error()}})
		return
	}
	l.events.Emit(Event{Type: EventTaskDispatched, TaskID: task.ID})

	l.mu.RLock()
	worker, ok := l.workers[task.WorkerType]
	l.mu.RUnlock()

	if !ok {
		_ = l.store.SetResult(task.ID, "", fmt.Sprintf("no worker registered for type %q", task.WorkerType))
		_ = l.store.UpdateStatus(task.ID, TaskStatusDispatched, TaskStatusRunning)
		_ = l.store.UpdateStatus(task.ID, TaskStatusRunning, TaskStatusFailed)
		l.events.Emit(Event{Type: EventTaskFailed, TaskID: task.ID})
		return
	}

	// Transition: dispatched → running
	if err := l.store.UpdateStatus(task.ID, TaskStatusDispatched, TaskStatusRunning); err != nil {
		return
	}

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
		return
	}

	result, execErr := worker.Execute(taskCtx, latest)
	if execErr != nil {
		_ = l.store.SetResult(task.ID, "", execErr.Error())
		_ = l.store.UpdateStatus(task.ID, TaskStatusRunning, TaskStatusFailed)
		l.events.Emit(Event{Type: EventTaskFailed, TaskID: task.ID, Data: map[string]any{"error": execErr.Error()}})
		return
	}

	// Store result.
	_ = l.store.SetResult(task.ID, result.Content, "")
	_ = l.store.UpdateStatus(task.ID, TaskStatusRunning, TaskStatusCompleted)
	l.events.Emit(Event{Type: EventTaskCompleted, TaskID: task.ID})
}
