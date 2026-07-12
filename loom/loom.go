package loom

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/thebtf/aimux/loom/clierror"
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

const (
	DefaultMaxSubtaskDepth   = 8
	DefaultMaxSubtaskBreadth = 16
	lifecycleOrderingStripes = 256
)

// Config contains LoomEngine runtime limits.
type Config struct {
	MaxSubtaskDepth   int
	MaxSubtaskBreadth int
}

// DefaultConfig returns LoomEngine's production defaults.
func DefaultConfig() Config {
	return Config{
		MaxSubtaskDepth:   DefaultMaxSubtaskDepth,
		MaxSubtaskBreadth: DefaultMaxSubtaskBreadth,
	}
}

// Option configures LoomEngine.
type Option func(*LoomEngine)

// WithConfig applies LoomEngine runtime limits.
func WithConfig(cfg Config) Option {
	return func(l *LoomEngine) { l.config = normalizeConfig(cfg) }
}

// WithMaxSubtaskDepth sets the accepted maximum sub-task tree depth.
func WithMaxSubtaskDepth(n int) Option {
	return func(l *LoomEngine) {
		cfg := l.config
		cfg.MaxSubtaskDepth = n
		l.config = normalizeConfig(cfg)
	}
}

// WithMaxSubtaskBreadth sets the accepted active sub-task budget per root task.
func WithMaxSubtaskBreadth(n int) Option {
	return func(l *LoomEngine) {
		cfg := l.config
		cfg.MaxSubtaskBreadth = n
		l.config = normalizeConfig(cfg)
	}
}

// WithMaxRetries sets the maximum retry count (default 2).
func WithMaxRetries(n int) Option {
	return func(l *LoomEngine) { l.maxRetries = n }
}

type subtaskContext struct {
	projectID  string
	rootTaskID string
}

type failedCrashIntent struct {
	task                 *Task
	expectedStatus       TaskStatus
	errMsg               string
	errorCode            string
	completedAt          time.Time
	logFailure           bool
	retryCanonicalWinner bool
}

type lifecycleProjectionState struct {
	deferred *failedCrashIntent
}

type lifecycleOrderingStripe struct {
	mu      sync.Mutex
	pending map[string]*lifecycleProjectionState
}

// markProjectionPending records the short interval in which cancellation has
// been committed but its externally visible event and worker signal have not
// both completed. The caller must hold s.mu.
func (s *lifecycleOrderingStripe) markProjectionPending(taskID string) {
	if s.pending == nil {
		s.pending = make(map[string]*lifecycleProjectionState)
	}
	s.pending[taskID] = &lifecycleProjectionState{}
}

// deferFailedCrash records at most one terminal intent while cancellation is
// being projected. Additional contenders are accepted but cannot replace the
// deterministic first winner. The caller must hold s.mu.
func (s *lifecycleOrderingStripe) deferFailedCrash(taskID string, intent failedCrashIntent) bool {
	state, pending := s.pending[taskID]
	if !pending {
		return false
	}
	if state.deferred == nil {
		state.deferred = cloneFailedCrashIntent(intent)
	}
	return true
}

func cloneFailedCrashIntent(intent failedCrashIntent) *failedCrashIntent {
	cloned := intent
	if intent.task != nil {
		task := *intent.task
		cloned.task = &task
	}
	return &cloned
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
	workerEnv  map[string]map[string]string
	mu         sync.RWMutex
	config     Config
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
	// AIMUX-21 T022: serializes sub-task breadth check + insert. The critical
	// section is short and avoids an unbounded per-root lock registry in the
	// long-running daemon.
	subtaskSubmitMu sync.Mutex

	// lifecycleOrdering serializes the cancellation intent projection with any
	// local failed_crash authority commit for the same task. A fixed stripe set
	// keeps the ordering primitive bounded for the lifetime of the daemon.
	lifecycleOrdering [lifecycleOrderingStripes]lifecycleOrderingStripe

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
		workerEnv:  make(map[string]map[string]string),
		config:     DefaultConfig(),
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

	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = LegacyTenantID
	}
	subtaskCtx, err := l.resolveSubtaskContext(req, tenantID)
	if err != nil {
		return "", err
	}
	if subtaskCtx.rootTaskID != "" {
		l.subtaskSubmitMu.Lock()
		defer l.subtaskSubmitMu.Unlock()
		if err := l.validateSubtaskBreadth(subtaskCtx.rootTaskID); err != nil {
			return "", err
		}
	}

	taskID := l.idGen.NewID()
	now := l.clock.Now().UTC()
	task := &Task{
		ID:           taskID,
		Status:       TaskStatusPending,
		WorkerType:   req.WorkerType,
		ProjectID:    subtaskCtx.projectID,
		RequestID:    reqID,
		ParentTaskID: req.ParentTaskID,
		TenantID:     tenantID,
		Prompt:       req.Prompt,
		CWD:          req.CWD,
		// Session environment is execution-only: never serialize its values into
		// the durable task row.
		Env:       map[string]string{},
		CLI:       req.CLI,
		Role:      req.Role,
		Model:     req.Model,
		Effort:    req.Effort,
		Timeout:   req.Timeout,
		Metadata:  req.Metadata,
		CreatedAt: now,
	}

	created, err := l.store.CommitCreated(context.Background(), CreateTask{
		TaskID:       task.ID,
		WorkerType:   task.WorkerType,
		ProjectID:    task.ProjectID,
		RequestID:    task.RequestID,
		ParentTaskID: task.ParentTaskID,
		TenantID:     task.TenantID,
		Prompt:       task.Prompt,
		CWD:          task.CWD,
		Env:          task.Env,
		CLI:          task.CLI,
		Role:         task.Role,
		Model:        task.Model,
		Effort:       task.Effort,
		Timeout:      task.Timeout,
		Metadata:     task.Metadata,
		CreatedAt:    now,
	})
	if err != nil {
		return "", fmt.Errorf("loom: persist task: %w", err)
	}
	if !created.Applied {
		return "", fmt.Errorf("loom: persist task: authority command was not applied")
	}
	l.emitTaskEvent(task, EventTaskCreated, TaskStatusPending, now)

	// Transition pending → dispatched synchronously before launching goroutine.
	// This ensures RecoverCrashed can pick up the task even if the process dies
	// before the goroutine runs.
	dispatchedAt := l.clock.Now().UTC()
	dispatched, err := l.store.CommitDispatched(context.Background(), DispatchTask{
		TaskID:         task.ID,
		ExpectedStatus: TaskStatusPending,
		DispatchedAt:   dispatchedAt,
	})
	if err != nil {
		return "", fmt.Errorf("loom: dispatch task: %w", err)
	}
	if !dispatched.Applied {
		return "", fmt.Errorf("loom: dispatch task: authority command was not applied")
	}
	// The execution-only environment is handed to the dispatch goroutine only
	// after its durable dispatch fact commits. Earlier failures must retain no
	// raw environment values in this long-lived engine.
	if len(req.Env) > 0 {
		l.mu.Lock()
		l.workerEnv[task.ID] = maps.Clone(req.Env)
		l.mu.Unlock()
	}
	task.Status = TaskStatusDispatched
	task.DispatchedAt = timePointer(dispatchedAt)
	l.emitTaskEvent(task, EventTaskDispatched, TaskStatusDispatched, dispatchedAt)

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

func (l *LoomEngine) resolveSubtaskContext(req TaskRequest, tenantID string) (subtaskContext, error) {
	if req.ParentTaskID == "" {
		return subtaskContext{projectID: req.ProjectID}, nil
	}

	parent, err := l.store.GetForTenantInEngine(req.ParentTaskID, tenantID)
	if err != nil {
		return subtaskContext{}, fmt.Errorf("loom: get parent task %s: %w", req.ParentTaskID, err)
	}
	rootTaskID, err := l.validateSubtaskDepth(parent, tenantID)
	if err != nil {
		return subtaskContext{}, err
	}
	if parent.ProjectID == "" {
		return subtaskContext{projectID: req.ProjectID, rootTaskID: rootTaskID}, nil
	}
	if req.ProjectID != "" && req.ProjectID != parent.ProjectID {
		return subtaskContext{}, clierror.NewCapabilityMismatch("subtask ProjectID must match parent ProjectID", nil)
	}
	return subtaskContext{projectID: parent.ProjectID, rootTaskID: rootTaskID}, nil
}

func (l *LoomEngine) validateSubtaskDepth(parent *Task, tenantID string) (string, error) {
	maxDepth := normalizeConfig(l.config).MaxSubtaskDepth
	depth := 0
	current := parent
	for {
		if depth >= maxDepth {
			return "", clierror.NewCapabilityMismatch(fmt.Sprintf("subtask depth exceeded; max=%d", maxDepth), nil)
		}
		if current.ParentTaskID == "" {
			return current.ID, nil
		}
		next, err := l.store.GetForTenantInEngine(current.ParentTaskID, tenantID)
		if err != nil {
			return "", fmt.Errorf("loom: get parent ancestor task %s: %w", current.ParentTaskID, err)
		}
		current = next
		depth++
	}
}

func (l *LoomEngine) validateSubtaskBreadth(rootTaskID string) error {
	maxBreadth := normalizeConfig(l.config).MaxSubtaskBreadth
	inflight, err := l.countInflightSubtasks(rootTaskID)
	if err != nil {
		return err
	}
	if inflight >= maxBreadth {
		return clierror.NewCapabilityMismatch("root subtask budget exhausted", nil)
	}
	return nil
}

func (l *LoomEngine) countInflightSubtasks(rootTaskID string) (int, error) {
	visited := map[string]struct{}{rootTaskID: {}}
	return l.countInflightSubtasksFrom(rootTaskID, visited)
}

func (l *LoomEngine) countInflightSubtasksFrom(parentTaskID string, visited map[string]struct{}) (int, error) {
	children, err := l.store.ListChildren(parentTaskID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, child := range children {
		if _, ok := visited[child.ID]; ok {
			return 0, fmt.Errorf("loom: subtask cycle at %s", child.ID)
		}
		visited[child.ID] = struct{}{}
		if child.Status.IsActive() {
			total++
		}
		childTotal, err := l.countInflightSubtasksFrom(child.ID, visited)
		if err != nil {
			return 0, err
		}
		total += childTotal
	}
	return total, nil
}

func normalizeConfig(cfg Config) Config {
	if cfg.MaxSubtaskDepth <= 0 {
		cfg.MaxSubtaskDepth = DefaultMaxSubtaskDepth
	}
	if cfg.MaxSubtaskBreadth <= 0 {
		cfg.MaxSubtaskBreadth = DefaultMaxSubtaskBreadth
	}
	return cfg
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
	return l.GetContext(context.Background(), taskID)
}

// GetContext returns current task state with caller-controlled cancellation.
func (l *LoomEngine) GetContext(ctx context.Context, taskID string) (*Task, error) {
	return l.store.GetContext(ctx, taskID)
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

// Cancel durably records cancellation intent before signalling a live worker.
func (l *LoomEngine) Cancel(taskID string) error {
	task, err := l.store.Get(taskID)
	if err != nil {
		return err
	}
	if task.EngineName != l.store.engineName {
		return ErrTaskNotFound
	}
	ordering := l.lifecycleOrderingFor(taskID)
	ordering.mu.Lock()
	requestedAt := l.clock.Now().UTC()
	intent, err := l.store.RequestCancel(context.Background(), taskID, requestedAt)
	if err != nil {
		ordering.mu.Unlock()
		return err
	}
	if !intent.Applied {
		ordering.mu.Unlock()
		return fmt.Errorf("loom: cancel task %s: authority command was not applied", taskID)
	}

	if !intent.RequiresStop {
		ordering.mu.Unlock()
		l.emitTaskEvent(task, EventTaskCancelled, TaskStatusCancelled, requestedAt)
		l.taskCancelledCounter.Add(context.Background(), 1, otelmetric.WithAttributes(
			attribute.String("worker_type", string(task.WorkerType)),
			attribute.String("project_id", task.ProjectID),
		))
		l.logger.InfoContext(context.Background(), "task cancelled",
			"module", "loom",
			"task_id", task.ID,
			"project_id", task.ProjectID,
			"worker_type", string(task.WorkerType),
			"task_status", string(TaskStatusCancelled),
			"request_id", task.RequestID,
		)
		return nil
	}
	ordering.markProjectionPending(taskID)
	l.mu.RLock()
	cancel := l.cancels[taskID]
	l.mu.RUnlock()
	ordering.mu.Unlock()
	l.emitTaskEvent(task, EventTaskCancelRequested, TaskStatusCancelling, requestedAt)
	if cancel != nil {
		cancel()
	}
	return l.flushDeferredFailedCrash(taskID)
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

	sort.Slice(running, func(i, j int) bool {
		if running[i].CreatedAt.Equal(running[j].CreatedAt) {
			return running[i].ID < running[j].ID
		}
		return running[i].CreatedAt.Before(running[j].CreatedAt)
	})
	type cancelCandidate struct {
		task   *Task
		cancel context.CancelFunc
	}
	candidates := make([]cancelCandidate, 0, len(running))

	l.mu.RLock()
	for _, task := range running {
		if cancel, ok := l.cancels[task.ID]; ok {
			candidates = append(candidates, cancelCandidate{task: task, cancel: cancel})
		}
	}
	l.mu.RUnlock()

	requestedAt := l.clock.Now().UTC()
	var joined []error
	signalled := 0
	for _, candidate := range candidates {
		ordering := l.lifecycleOrderingFor(candidate.task.ID)
		ordering.mu.Lock()
		intent, requestErr := l.store.RequestCancel(context.Background(), candidate.task.ID, requestedAt)
		if requestErr != nil {
			ordering.mu.Unlock()
			if errors.Is(requestErr, ErrAuthorityConflict) {
				continue
			}
			joined = append(joined, fmt.Errorf("loom: cancel task %s: %w", candidate.task.ID, requestErr))
			continue
		}
		if !intent.Applied || !intent.RequiresStop {
			ordering.mu.Unlock()
			continue
		}
		ordering.markProjectionPending(candidate.task.ID)
		ordering.mu.Unlock()
		l.emitTaskEvent(candidate.task, EventTaskCancelRequested, TaskStatusCancelling, requestedAt)
		candidate.cancel()
		signalled++
		if flushErr := l.flushDeferredFailedCrash(candidate.task.ID); flushErr != nil {
			joined = append(joined, fmt.Errorf("loom: cancel task %s: %w", candidate.task.ID, flushErr))
		}
	}

	return signalled, errors.Join(joined...)
}

// RecoverCrashed atomically records one failed_crash fact for every task whose
// active execution was interrupted by daemon restart.
// Called once on daemon startup.
func (l *LoomEngine) RecoverCrashed() (int, error) {
	tasks, err := l.store.listRecoveryCandidates()
	if err != nil {
		return 0, fmt.Errorf("loom: list recovery candidates: %w", err)
	}
	recoveryAt := l.clock.Now().UTC()
	const recoveryError = "task interrupted by daemon restart"
	var joined []error
	recovered := 0
	for _, task := range tasks {
		intent := failedCrashIntent{task: task, expectedStatus: task.Status, errMsg: recoveryError, completedAt: recoveryAt}
		ordering := l.lifecycleOrderingFor(task.ID)
		ordering.mu.Lock()
		if ordering.deferFailedCrash(task.ID, intent) {
			ordering.mu.Unlock()
			continue
		}
		projection, commitErr := l.commitFailedCrashLocked(intent)
		ordering.mu.Unlock()
		if commitErr != nil {
			if errors.Is(commitErr, ErrAuthorityConflict) {
				continue
			}
			joined = append(joined, fmt.Errorf("loom: recover task %s: %w", task.ID, commitErr))
			continue
		}
		if projection == nil {
			continue
		}
		l.projectFailedCrash(*projection)
		recovered++
	}
	return recovered, errors.Join(joined...)
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
	l.appendProgressArtifact(context.Background(), taskID, info)
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

func (l *LoomEngine) appendProgressArtifact(ctx context.Context, taskID string, info ProgressInfo) {
	if taskID == "" {
		return
	}
	payload := map[string]any{
		"last_output_line": info.LastOutputLine,
		"progress_lines":   info.ProgressLines,
		"project_id":       info.ProjectID,
		"request_id":       info.RequestID,
	}
	if info.ProgressUpdatedAt != nil {
		payload["progress_updated_at"] = info.ProgressUpdatedAt.UTC().Format(time.RFC3339)
	}
	_, err := l.store.AppendArtifact(taskID, TaskArtifactAppend{
		Kind:          TaskArtifactKindProgress,
		EventType:     string(EventTaskProgress),
		Summary:       info.LastOutputLine,
		Payload:       payload,
		ContentLength: int64(len(info.LastOutputLine)),
		Redacted:      strings.Contains(info.LastOutputLine, "[REDACTED]"),
		Truncated:     len(info.LastOutputLine) >= progressLineMaxBytes,
	})
	if err != nil {
		l.logArtifactProjectionError(ctx, &Task{ID: taskID, ProjectID: info.ProjectID, RequestID: info.RequestID, WorkerType: info.WorkerType}, "progress", err)
	}
}

func (l *LoomEngine) logArtifactProjectionError(ctx context.Context, task *Task, kind string, err error) {
	if task == nil {
		return
	}
	l.logger.ErrorContext(ctx, "task artifact projection failed",
		"module", "loom",
		"task_id", task.ID,
		"project_id", task.ProjectID,
		"worker_type", string(task.WorkerType),
		"request_id", task.RequestID,
		"artifact_kind", kind,
		"error_code", "artifact_projection",
		"error", err,
	)
}

const unverifiedStopError = "task cancellation completed without verified stop evidence"

func (l *LoomEngine) emitTaskEvent(task *Task, eventType EventType, status TaskStatus, at time.Time) {
	if task == nil {
		return
	}
	l.events.Emit(TaskEvent{
		Type:      eventType,
		TaskID:    task.ID,
		ProjectID: task.ProjectID,
		RequestID: task.RequestID,
		Status:    status,
		Timestamp: at.UTC(),
	})
}

func (l *LoomEngine) recordTaskFailed(task *Task) {
	if task == nil {
		return
	}
	l.taskFailedCounter.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("worker_type", string(task.WorkerType)),
		attribute.String("project_id", task.ProjectID),
	))
}

func (l *LoomEngine) logAuthorityCommitError(task *Task, operation string, status TaskStatus, err error) {
	if task == nil || err == nil || errors.Is(err, ErrAuthorityConflict) {
		return
	}
	l.logger.ErrorContext(context.Background(), operation,
		"module", "loom",
		"task_id", task.ID,
		"project_id", task.ProjectID,
		"worker_type", string(task.WorkerType),
		"task_status", string(status),
		"request_id", task.RequestID,
		"error_code", "authority_commit",
		"error", err,
	)
}

// commitFailedCrashAt returns true when this intent was either applied now or
// accepted into an in-flight cancellation projection. In the deferred case it
// means handled, not that this contender became the deterministic winner.
func (l *LoomEngine) commitFailedCrashAt(task *Task, expectedStatus TaskStatus, errMsg, errorCode string, completedAt time.Time) bool {
	return l.commitFailedCrashIntent(failedCrashIntent{
		task:           task,
		expectedStatus: expectedStatus,
		errMsg:         errMsg,
		errorCode:      errorCode,
		completedAt:    completedAt,
		logFailure:     true,
	})
}

func (l *LoomEngine) commitPanicFailedCrashAt(task *Task, expectedStatus TaskStatus, errMsg string, completedAt time.Time) bool {
	return l.commitFailedCrashIntent(failedCrashIntent{
		task:                 task,
		expectedStatus:       expectedStatus,
		errMsg:               errMsg,
		errorCode:            "dispatch_panic",
		completedAt:          completedAt,
		logFailure:           true,
		retryCanonicalWinner: true,
	})
}

func (l *LoomEngine) commitFailedCrashIntent(intent failedCrashIntent) bool {
	if intent.task == nil {
		return false
	}
	ordering := l.lifecycleOrderingFor(intent.task.ID)
	ordering.mu.Lock()
	if ordering.deferFailedCrash(intent.task.ID, intent) {
		ordering.mu.Unlock()
		return true
	}
	projection, err := l.commitFailedCrashLocked(intent)
	ordering.mu.Unlock()
	if err != nil {
		l.logAuthorityCommitError(intent.task, "failed_crash authority commit failed", intent.expectedStatus, err)
		return false
	}
	if projection == nil {
		return false
	}
	l.projectFailedCrash(*projection)
	return true
}

// commitFailedCrashLocked performs only canonical persistence. Event delivery,
// metrics, and logging are deliberately projected after the ordering stripe is
// released so arbitrary subscribers never execute under the stripe lock.
func (l *LoomEngine) commitFailedCrashLocked(intent failedCrashIntent) (*failedCrashIntent, error) {
	result, err := l.store.CommitFailedCrash(context.Background(), FailCrashedTask{
		TaskID:         intent.task.ID,
		ExpectedStatus: intent.expectedStatus,
		Error:          intent.errMsg,
		CompletedAt:    intent.completedAt,
	})
	if errors.Is(err, ErrAuthorityConflict) {
		winner := result.Winner.Task.Status
		if winner.IsTerminal() {
			return nil, nil
		}
		retryWinner := winner == TaskStatusCancelling || intent.retryCanonicalWinner
		if retryWinner && winner != intent.expectedStatus && !winner.IsTerminal() {
			intent.expectedStatus = winner
			if winner == TaskStatusCancelling {
				intent.completedAt = l.clock.Now().UTC()
				intent.errMsg = unverifiedStopError
				intent.errorCode = "unverified_stop"
			}
			result, err = l.store.CommitFailedCrash(context.Background(), FailCrashedTask{
				TaskID:         intent.task.ID,
				ExpectedStatus: intent.expectedStatus,
				Error:          intent.errMsg,
				CompletedAt:    intent.completedAt,
			})
			if errors.Is(err, ErrAuthorityConflict) && result.Winner.Task.Status.IsTerminal() {
				return nil, nil
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if !result.Applied {
		return nil, nil
	}
	return cloneFailedCrashIntent(intent), nil
}

func (l *LoomEngine) projectFailedCrash(intent failedCrashIntent) {
	if intent.task == nil {
		return
	}
	l.emitTaskEvent(intent.task, EventTaskFailedCrash, TaskStatusFailedCrash, intent.completedAt)
	l.recordTaskFailed(intent.task)
	if !intent.logFailure {
		return
	}
	l.logger.ErrorContext(context.Background(), "task failed crash",
		"module", "loom",
		"task_id", intent.task.ID,
		"project_id", intent.task.ProjectID,
		"worker_type", string(intent.task.WorkerType),
		"task_status", string(TaskStatusFailedCrash),
		"request_id", intent.task.RequestID,
		"error_code", intent.errorCode,
		"error", intent.errMsg,
	)
}

func (l *LoomEngine) flushDeferredFailedCrash(taskID string) error {
	ordering := l.lifecycleOrderingFor(taskID)
	ordering.mu.Lock()
	state, pending := ordering.pending[taskID]
	if !pending {
		ordering.mu.Unlock()
		return nil
	}
	delete(ordering.pending, taskID)
	if len(ordering.pending) == 0 {
		ordering.pending = nil
	}
	var projection *failedCrashIntent
	var err error
	if state.deferred != nil {
		projection, err = l.commitFailedCrashLocked(*state.deferred)
	}
	ordering.mu.Unlock()
	if err != nil {
		if state.deferred != nil {
			l.logAuthorityCommitError(state.deferred.task, "deferred failed_crash authority commit failed", state.deferred.expectedStatus, err)
		}
		return fmt.Errorf("flush deferred failed_crash: %w", err)
	}
	if projection != nil {
		l.projectFailedCrash(*projection)
	}
	return nil
}

func (l *LoomEngine) lifecycleOrderingFor(taskID string) *lifecycleOrderingStripe {
	// FNV-1a is stable, allocation-free, and sufficient for spreading task IDs
	// across a bounded set of private ordering stripes.
	const offset32 = uint32(2166136261)
	const prime32 = uint32(16777619)
	hash := offset32
	for i := 0; i < len(taskID); i++ {
		hash ^= uint32(taskID[i])
		hash *= prime32
	}
	return &l.lifecycleOrdering[hash%uint32(len(l.lifecycleOrdering))]
}

func (l *LoomEngine) lifecycleProjectionPending(taskID string) bool {
	ordering := l.lifecycleOrderingFor(taskID)
	ordering.mu.Lock()
	_, pending := ordering.pending[taskID]
	ordering.mu.Unlock()
	return pending
}

func (l *LoomEngine) reconcileCancellingWinner(task *Task, result CommitResult, err error, at time.Time) bool {
	if !errors.Is(err, ErrAuthorityConflict) {
		return false
	}
	if result.Winner.Task.Status == TaskStatusCancelling {
		l.commitFailedCrashAt(task, TaskStatusCancelling, unverifiedStopError, "unverified_stop", at)
	}
	return true
}

func (l *LoomEngine) loadRunningTask(task *Task, phase string) (*Task, bool) {
	current, err := l.store.Get(task.ID)
	if err != nil {
		l.logger.ErrorContext(context.Background(), phase+": store.Get failed",
			"module", "loom",
			"task_id", task.ID,
			"project_id", task.ProjectID,
			"worker_type", string(task.WorkerType),
			"task_status", string(TaskStatusRunning),
			"request_id", task.RequestID,
			"error_code", "store_get",
			"error", err,
		)
		l.failTask(task, TaskStatusRunning, fmt.Sprintf("%s: reload task failed: %v", phase, err))
		return nil, false
	}
	if current.Status == TaskStatusCancelling {
		l.commitFailedCrashAt(task, TaskStatusCancelling, unverifiedStopError, "unverified_stop", l.clock.Now().UTC())
		return nil, false
	}
	if current.Status != TaskStatusRunning {
		return nil, false
	}
	l.mu.RLock()
	current.Env = maps.Clone(l.workerEnv[task.ID])
	l.mu.RUnlock()
	return current, true
}

func (l *LoomEngine) stopAfterWorkerReturn(task *Task) bool {
	current, err := l.store.Get(task.ID)
	if err != nil {
		return false
	}
	if current.Status == TaskStatusCancelling {
		l.commitFailedCrashAt(task, TaskStatusCancelling, unverifiedStopError, "unverified_stop", l.clock.Now().UTC())
		return true
	}
	return current.Status != TaskStatusRunning
}

// failTask is a best-effort helper that marks a task as failed in the store
// and emits EventTaskFailed. Errors from store operations are logged but not
// returned — the caller is typically already handling a failure path.
// task is passed directly so the helper avoids an additional DB round-trip and
// emits a fully-populated TaskEvent regardless of store availability.
func (l *LoomEngine) failTask(task *Task, fromStatus TaskStatus, errMsg string) {
	ctx := context.Background()
	completedAt := l.clock.Now().UTC()
	result, err := l.store.CommitFailed(ctx, FailTask{
		TaskID:         task.ID,
		ExpectedStatus: fromStatus,
		Error:          errMsg,
		CompletedAt:    completedAt,
	})
	if err != nil {
		if l.reconcileCancellingWinner(task, result, err, l.clock.Now().UTC()) {
			return
		}
		l.logAuthorityCommitError(task, "failTask authority commit failed", fromStatus, err)
		return
	}
	if !result.Applied {
		return
	}
	l.emitTaskEvent(task, EventTaskFailed, TaskStatusFailed, completedAt)
	l.recordTaskFailed(task)
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
	defer func() {
		l.mu.Lock()
		delete(l.workerEnv, task.ID)
		l.mu.Unlock()
	}()
	// Decrement the WaitGroup exactly once when this goroutine exits.
	// Paired with the l.wg.Add(1) performed at the start of Submit (under mu).
	defer l.wg.Done()
	// Panic recovery: ensure any panic in worker or gate is caught, task is
	// marked failed_crash, and the process is not terminated.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		panicMsg := fmt.Sprintf("panic: %v", r)
		expectedStatus := task.Status
		current, err := l.store.Get(task.ID)
		if err != nil {
			l.logAuthorityCommitError(task, "dispatch panic: load canonical task failed", task.Status, err)
		} else {
			if current.Status.IsTerminal() {
				return
			}
			expectedStatus = current.Status
		}
		if l.commitPanicFailedCrashAt(task, expectedStatus, panicMsg, l.clock.Now().UTC()) {
			l.logger.ErrorContext(context.Background(), "dispatch panic stack",
				"module", "loom",
				"task_id", task.ID,
				"project_id", task.ProjectID,
				"worker_type", string(task.WorkerType),
				"task_status", string(TaskStatusFailedCrash),
				"request_id", task.RequestID,
				"error_code", "dispatch_panic",
				"stack", string(debug.Stack()),
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
		if !l.lifecycleProjectionPending(task.ID) {
			cancel()
		}
		// lifecycleProjectionPending releases the stripe before this lock.
		// Cancel takes the opposite data path (stripe then l.mu.RLock), so the
		// two locks are never held in inverse order.
		l.mu.Lock()
		delete(l.cancels, task.ID)
		l.mu.Unlock()
	}()

	runningAt := l.clock.Now().UTC()
	running, err := l.store.CommitRunning(context.Background(), RunTask{
		TaskID:         task.ID,
		ExpectedStatus: TaskStatusDispatched,
		RunningAt:      runningAt,
	})
	if err != nil {
		if l.reconcileCancellingWinner(task, running, err, l.clock.Now().UTC()) {
			return
		}
		l.logAuthorityCommitError(task, "dispatch running authority commit failed", TaskStatusDispatched, err)
		return
	}
	if !running.Applied {
		return
	}
	task.Status = TaskStatusRunning
	l.emitTaskEvent(task, EventTaskRunning, TaskStatusRunning, runningAt)

	latest, ok := l.loadRunningTask(task, "dispatch")
	if !ok {
		return
	}

	result, execErr := worker.Execute(taskCtx, latest)
	if l.stopAfterWorkerReturn(task) {
		return
	}
	if execErr != nil {
		l.failTask(task, TaskStatusRunning, execErr.Error())
		return
	}
	if !l.persistWorkerMetadata(taskCtx, task, latest, result) {
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
			completedAt := l.clock.Now().UTC()
			completed, err := l.store.CommitCompleted(gateCtx, CompleteTask{
				TaskID:         task.ID,
				ExpectedStatus: TaskStatusRunning,
				Result:         result.Content,
				CompletedAt:    completedAt,
			})
			if err != nil {
				if l.reconcileCancellingWinner(task, completed, err, l.clock.Now().UTC()) {
					return
				}
				l.logAuthorityCommitError(task, "completion authority commit failed", TaskStatusRunning, err)
				return
			}
			if !completed.Applied {
				return
			}
			l.emitTaskEvent(task, EventTaskCompleted, TaskStatusCompleted, completedAt)
			// T030: completed task counter + end-to-end duration.
			l.taskCompletedCounter.Add(gateCtx, 1, attrs)
			var taskDurationMS int64
			if latest.DispatchedAt != nil {
				taskDurationMS = completedAt.Sub(*latest.DispatchedAt).Milliseconds()
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
		retryingAt := l.clock.Now().UTC()
		retrying, err := l.store.CommitRetrying(gateCtx, RetryTask{
			TaskID:         task.ID,
			ExpectedStatus: TaskStatusRunning,
			RetryingAt:     retryingAt,
		})
		if err != nil {
			if l.reconcileCancellingWinner(task, retrying, err, l.clock.Now().UTC()) {
				return
			}
			l.logAuthorityCommitError(task, "retrying authority commit failed", TaskStatusRunning, err)
			return
		}
		if !retrying.Applied {
			return
		}
		task.Status = TaskStatusRetrying
		l.emitTaskEvent(task, EventTaskRetrying, TaskStatusRetrying, retryingAt)

		dispatchedAt := l.clock.Now().UTC()
		dispatched, err := l.store.CommitDispatched(gateCtx, DispatchTask{
			TaskID:         task.ID,
			ExpectedStatus: TaskStatusRetrying,
			DispatchedAt:   dispatchedAt,
		})
		if err != nil {
			if l.reconcileCancellingWinner(task, dispatched, err, l.clock.Now().UTC()) {
				return
			}
			l.logAuthorityCommitError(task, "retry dispatch authority commit failed", TaskStatusRetrying, err)
			return
		}
		if !dispatched.Applied {
			return
		}
		task.Status = TaskStatusDispatched
		task.DispatchedAt = timePointer(dispatchedAt)
		l.emitTaskEvent(task, EventTaskDispatched, TaskStatusDispatched, dispatchedAt)

		runningAt := l.clock.Now().UTC()
		running, err := l.store.CommitRunning(gateCtx, RunTask{
			TaskID:         task.ID,
			ExpectedStatus: TaskStatusDispatched,
			RunningAt:      runningAt,
		})
		if err != nil {
			if l.reconcileCancellingWinner(task, running, err, l.clock.Now().UTC()) {
				return
			}
			l.logAuthorityCommitError(task, "retry running authority commit failed", TaskStatusDispatched, err)
			return
		}
		if !running.Applied {
			return
		}
		task.Status = TaskStatusRunning
		l.emitTaskEvent(task, EventTaskRunning, TaskStatusRunning, runningAt)

		var runnable bool
		latest, runnable = l.loadRunningTask(task, "dispatch retry")
		if !runnable {
			return
		}

		result, execErr = worker.Execute(taskCtx, latest)
		if l.stopAfterWorkerReturn(task) {
			return
		}
		if execErr != nil {
			l.failTask(task, TaskStatusRunning, execErr.Error())
			return
		}
		if !l.persistWorkerMetadata(taskCtx, task, latest, result) {
			return
		}
	}
}

func (l *LoomEngine) persistWorkerMetadata(ctx context.Context, task *Task, latest *Task, result *WorkerResult) bool {
	if latest == nil {
		return true
	}
	if len(latest.Metadata) == 0 && (result == nil || len(result.Metadata) == 0) {
		return true
	}
	metadata := mergeTaskMetadata(latest.Metadata, nil)
	if result != nil {
		metadata = mergeTaskMetadata(metadata, result.Metadata)
	}
	if err := l.store.SetMetadata(latest.ID, metadata); err != nil {
		l.logger.ErrorContext(ctx, "dispatch: store.SetMetadata failed",
			"module", "loom",
			"task_id", latest.ID,
			"project_id", latest.ProjectID,
			"worker_type", string(latest.WorkerType),
			"task_status", string(TaskStatusRunning),
			"request_id", latest.RequestID,
			"error_code", "store_set_metadata",
			"error", err,
		)
		l.failTask(task, TaskStatusRunning, fmt.Sprintf("dispatch: persist metadata failed: %v", err))
		return false
	}
	latest.Metadata = metadata
	return true
}

func mergeTaskMetadata(base map[string]any, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return map[string]any{}
	}
	merged := make(map[string]any, len(base)+len(overlay))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}
