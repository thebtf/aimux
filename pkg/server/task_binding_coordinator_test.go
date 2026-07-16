package server

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

// --- fixtures -------------------------------------------------------------

type taskBindingFixture struct {
	db      *sql.DB
	store   *loom.TaskStore
	engine  *loom.LoomEngine
	swarm   *swarm.Swarm
	runtime *workerruntime.WorkerRuntime
	coord   *taskBindingCoordinator
}

func newTaskBindingFixture(t *testing.T, factory func(string) (types.ExecutorV2, error)) *taskBindingFixture {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?cache=shared&mode=memory"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := loom.NewTaskStore(db, "task-binding-test")
	if err != nil {
		t.Fatalf("loom.NewTaskStore: %v", err)
	}
	fabric := swarm.New(factory, nil)
	t.Cleanup(func() { _ = fabric.Shutdown(context.Background()) })
	rt, err := workerruntime.New(fabric)
	if err != nil {
		t.Fatalf("workerruntime.New: %v", err)
	}
	coord := newTaskBindingCoordinator(store, fabric, rt, func(format string, args ...any) {
		t.Logf("task binding warn: "+format, args...)
	})
	engine := loom.New(store)
	return &taskBindingFixture{db: db, store: store, engine: engine, swarm: fabric, runtime: rt, coord: coord}
}

func (f *taskBindingFixture) seedTask(t *testing.T, id, tenantID, projectID string) {
	t.Helper()
	if err := f.store.Create(&loom.Task{
		ID:         id,
		Status:     loom.TaskStatusRunning,
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		TenantID:   tenantID,
		Prompt:     "test prompt",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed task %q: %v", id, err)
	}
}

type taskBindingRow struct {
	ID            string
	State         string
	LeaseOwner    sql.NullString
	SwarmHandleID sql.NullString
}

func (f *taskBindingFixture) runBindingsForTask(t *testing.T, taskID string) []taskBindingRow {
	t.Helper()
	rows, err := f.db.QueryContext(context.Background(),
		`SELECT id, state, lease_owner, swarm_handle_id FROM worker_run_bindings WHERE task_id=? ORDER BY created_at`, taskID)
	if err != nil {
		t.Fatalf("query run bindings: %v", err)
	}
	defer rows.Close()
	var out []taskBindingRow
	for rows.Next() {
		var row taskBindingRow
		if err := rows.Scan(&row.ID, &row.State, &row.LeaseOwner, &row.SwarmHandleID); err != nil {
			t.Fatalf("scan run binding row: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate run bindings: %v", err)
	}
	return out
}

func (f *taskBindingFixture) authorityForTask(t *testing.T, taskID string) loom.WorkerRunBindingAuthority {
	t.Helper()
	var authority loom.WorkerRunBindingAuthority
	var leaseOwner sql.NullString
	err := f.db.QueryRowContext(context.Background(),
		`SELECT id, lease_owner, lease_generation FROM worker_run_bindings WHERE task_id=? ORDER BY created_at DESC LIMIT 1`, taskID,
	).Scan(&authority.BindingID, &leaseOwner, &authority.LeaseGeneration)
	if err != nil {
		t.Fatalf("query run binding authority: %v", err)
	}
	authority.LeaseOwner = leaseOwner.String
	return authority
}

func (f *taskBindingFixture) workerSessionState(t *testing.T, sessionID string) (string, bool) {
	t.Helper()
	var state string
	err := f.db.QueryRowContext(context.Background(), `SELECT state FROM worker_sessions WHERE id=?`, sessionID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("query worker session: %v", err)
	}
	return state, true
}

var taskBindingTestScope = func() string {
	root, err := canonicalWorktreeRoot(filepath.Join(os.TempDir(), "aimux-task-binding-project"))
	if err != nil {
		panic(err)
	}
	return root
}()

// taskBindingTestOpts mirrors production's swarm.WithScope(scope) wiring
// (task_dispatch_runtime.go) so a test's durable SwarmScope evidence always
// matches the scope actually used to partition the live Swarm handle.
func taskBindingTestOpts() []swarm.GetOption {
	return []swarm.GetOption{swarm.WithScope(taskBindingTestScope)}
}

func defaultTaskIdent(taskID string) TaskBindingIdentity {
	return TaskBindingIdentity{
		TaskID:                taskID,
		TenantID:              "coord-tenant",
		ProjectID:             "coord-project",
		ProfileFingerprint:    "profile-fp",
		CapabilityFingerprint: "capability-fp",
	}
}

func successExecute(content string) executeFunc {
	return func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
		return &types.Response{Content: content}, true, nil
	}
}

// --- fake executor/session -------------------------------------------------

type coordinatorFakeSession struct {
	alive bool
	sends chan string
}

func (s *coordinatorFakeSession) ID() string { return "coordinator-fake-session" }
func (s *coordinatorFakeSession) Send(_ context.Context, content string) (*types.Result, error) {
	if s.sends != nil {
		s.sends <- content
	}
	return &types.Result{Content: "ok"}, nil
}

func (s *coordinatorFakeSession) Stream(context.Context, string) (<-chan types.Event, error) {
	ch := make(chan types.Event, 1)
	ch <- types.Event{Type: types.EventTypeComplete}
	close(ch)
	return ch, nil
}
func (s *coordinatorFakeSession) Close() error { return nil }
func (s *coordinatorFakeSession) Alive() bool  { return s.alive }
func (s *coordinatorFakeSession) PID() int     { return 4242 }

type coordinatorFakeExecutor struct {
	alive        types.HealthStatus
	persistent   bool
	identity     types.SessionIdentity
	closed       atomic.Bool
	closeErr     error
	messages     chan types.Message
	forkCalls    *atomic.Int32
	startArgs    chan types.SpawnArgs
	sessionSends chan string
	sessionMu    sync.Mutex
	boundSession types.Session
}

func (e *coordinatorFakeExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{Capabilities: types.ExecutorCapabilities{PersistentSessions: e.persistent}}
}

func (e *coordinatorFakeExecutor) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	e.sessionMu.Lock()
	session := e.boundSession
	e.sessionMu.Unlock()
	if session != nil {
		result, err := session.Send(ctx, msg.Content)
		if err != nil {
			return nil, err
		}
		return &types.Response{Content: result.Content, Stderr: result.Stderr, ExitCode: result.ExitCode, Partial: result.Partial, Error: result.Error}, nil
	}
	if e.messages != nil {
		e.messages <- msg
	}
	return &types.Response{Content: "ok"}, nil
}

func (e *coordinatorFakeExecutor) SendStream(ctx context.Context, msg types.Message, _ func(types.Chunk)) (*types.Response, error) {
	return e.Send(ctx, msg)
}
func (e *coordinatorFakeExecutor) IsAlive() types.HealthStatus { return e.alive }
func (e *coordinatorFakeExecutor) Close() error {
	e.closed.Store(true)
	return e.closeErr
}

func (e *coordinatorFakeExecutor) StartSession(_ context.Context, args types.SpawnArgs) (types.Session, error) {
	if e.startArgs != nil {
		e.startArgs <- args
	}
	return &coordinatorFakeSession{alive: true, sends: e.sessionSends}, nil
}

func (e *coordinatorFakeExecutor) WithSession(session types.Session) types.ExecutorV2 {
	e.sessionMu.Lock()
	e.boundSession = session
	e.sessionMu.Unlock()
	return e
}
func (e *coordinatorFakeExecutor) SessionIdentity() types.SessionIdentity { return e.identity }
func (e *coordinatorFakeExecutor) ForkSession(context.Context, types.SessionIdentity, types.SpawnArgs) (types.Session, error) {
	if e.forkCalls != nil {
		e.forkCalls.Add(1)
	}
	return &coordinatorFakeSession{alive: true, sends: e.sessionSends}, nil
}

// --- T007/T008 behavioral proofs -------------------------------------------

// TestTaskBindingCoordinatorDispatchStatelessLeavesLeaseActiveAfterReturn
// proves the default stateless path: reserve precedes acquire/execute, live
// identity is recorded before the provider call, a true native return is
// recorded as "returned" while its lease stays active (release/finalize is
// Loom-terminal-outcome integration's job, tracked separately), and the
// response is preserved unchanged.
func TestTaskBindingCoordinatorDispatchStatelessLeavesLeaseActiveAfterReturn(t *testing.T) {
	var factoryCalls atomic.Int32
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		factoryCalls.Add(1)
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-stateless-1", "coord-tenant", "coord-project")

	response, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
		defaultTaskIdent("task-stateless-1"), taskSessionRequest{}, taskBindingTestOpts(), successExecute("stateless-ok"))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if response == nil || response.Content != "stateless-ok" {
		t.Fatalf("response = %#v, want content preserved", response)
	}
	if factoryCalls.Load() != 1 {
		t.Fatalf("factory calls = %d, want exactly 1 (Swarm actually used)", factoryCalls.Load())
	}
	rows := f.runBindingsForTask(t, "task-stateless-1")
	if len(rows) != 1 {
		t.Fatalf("run binding rows = %d, want 1", len(rows))
	}
	if rows[0].State != string(loom.WorkerRunBindingStateReturned) {
		t.Fatalf("run binding state = %q, want %q", rows[0].State, loom.WorkerRunBindingStateReturned)
	}
	if !rows[0].LeaseOwner.Valid || rows[0].LeaseOwner.String == "" {
		t.Fatal("lease owner cleared after native return; release/finalize must stay deferred to Loom terminal-outcome integration")
	}
	if !rows[0].SwarmHandleID.Valid || rows[0].SwarmHandleID.String == "" {
		t.Fatal("swarm handle ID not recorded")
	}
}

// TestTaskBindingCoordinatorDistinctRunBindingPerAttempt proves every
// fallback/retry attempt for the same task gets its own Run Binding row.
func TestTaskBindingCoordinatorDistinctRunBindingPerAttempt(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-retry-1", "coord-tenant", "coord-project")

	for i := range 2 {
		if _, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
			defaultTaskIdent("task-retry-1"), taskSessionRequest{}, taskBindingTestOpts(), successExecute("attempt-ok")); err != nil {
			t.Fatalf("dispatch attempt %d: %v", i, err)
		}
	}
	rows := f.runBindingsForTask(t, "task-retry-1")
	if len(rows) != 2 {
		t.Fatalf("run binding rows = %d, want 2 distinct attempts", len(rows))
	}
	if rows[0].ID == rows[1].ID {
		t.Fatalf("both attempts reused the same Run Binding ID %q", rows[0].ID)
	}
}

// TestTaskBindingCoordinatorReserveConflictNeverReachesSwarm proves a Loom
// reserve rejection (here: tenant/project mismatch against the durable task)
// fails closed before Swarm is ever touched and before execute runs.
func TestTaskBindingCoordinatorReserveConflictNeverReachesSwarm(t *testing.T) {
	var factoryCalls atomic.Int32
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		factoryCalls.Add(1)
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-conflict-1", "coord-tenant", "coord-project")

	var executeCalls atomic.Int32
	ident := defaultTaskIdent("task-conflict-1")
	ident.TenantID = "wrong-tenant"
	_, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project", ident, taskSessionRequest{}, taskBindingTestOpts(),
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			executeCalls.Add(1)
			return &types.Response{}, true, nil
		})
	if !errors.Is(err, loom.ErrAuthorityConflict) {
		t.Fatalf("err = %v, want ErrAuthorityConflict", err)
	}
	if factoryCalls.Load() != 0 {
		t.Fatalf("factory calls = %d, want 0: Swarm must never be touched before reserve succeeds", factoryCalls.Load())
	}
	if executeCalls.Load() != 0 {
		t.Fatalf("execute calls = %d, want 0", executeCalls.Load())
	}
}

// TestTaskBindingCoordinatorSameWorkerSessionContentionFailsBeforeSecondSpawn
// proves one active turn per Worker Session: a second attempt against a
// session still leased by the first native-returned (but not yet released)
// attempt fails closed before any second Swarm spawn or execute call.
func TestTaskBindingCoordinatorSameWorkerSessionContentionFailsBeforeSecondSpawn(t *testing.T) {
	var factoryCalls atomic.Int32
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		factoryCalls.Add(1)
		return &coordinatorFakeExecutor{
			alive: types.HealthAlive, persistent: true,
			identity: types.SessionIdentity{Provider: "neutral", ID: "sess-live", Generation: 1},
		}, nil
	})
	f.seedTask(t, "task-session-1", "coord-tenant", "coord-project")
	f.seedTask(t, "task-session-2", "coord-tenant", "coord-project")

	sessionReq := taskSessionRequest{Mode: types.SessionBindingModeNew, WorkerSessionID: "contended-session"}

	if _, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
		defaultTaskIdent("task-session-1"), sessionReq, taskBindingTestOpts(), successExecute("first-ok")); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if factoryCalls.Load() != 1 {
		t.Fatalf("factory calls after first dispatch = %d, want 1", factoryCalls.Load())
	}
	if state, ok := f.workerSessionState(t, "contended-session"); !ok || state != string(loom.WorkerSessionStateLeased) {
		t.Fatalf("worker session state = %q ok=%t, want leased and retained (no release on native return)", state, ok)
	}

	var executeCalls atomic.Int32
	_, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
		defaultTaskIdent("task-session-2"), sessionReq, taskBindingTestOpts(),
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			executeCalls.Add(1)
			return &types.Response{}, true, nil
		})
	if !errors.Is(err, loom.ErrAuthorityConflict) {
		t.Fatalf("second dispatch err = %v, want ErrAuthorityConflict", err)
	}
	if factoryCalls.Load() != 1 {
		t.Fatalf("factory calls after contended second dispatch = %d, want still 1 (no second spawn)", factoryCalls.Load())
	}
	if executeCalls.Load() != 0 {
		t.Fatalf("execute calls on contended attempt = %d, want 0", executeCalls.Load())
	}
}

// TestTaskBindingCoordinatorExactResumeMismatchFailsBeforeSpawnNoDowngrade
// proves an exact_resume request against a Worker Session Loom has never
// seen fails closed at reserve, before any Swarm call — and never silently
// downgrades to new/stateless.
func TestTaskBindingCoordinatorExactResumeMismatchFailsBeforeSpawnNoDowngrade(t *testing.T) {
	var factoryCalls atomic.Int32
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		factoryCalls.Add(1)
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-resume-absent", "coord-tenant", "coord-project")

	sessionReq := taskSessionRequest{
		Mode:            types.SessionBindingModeExactResume,
		WorkerSessionID: "never-existed-session",
		Expected: &types.SessionBindingIdentity{
			HandleID:           "irrelevant-handle",
			HandleGeneration:   1,
			RegistryGeneration: 1,
			ProviderSession:    types.SessionIdentity{Provider: "neutral", ID: "irrelevant", Generation: 1},
		},
	}
	_, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
		defaultTaskIdent("task-resume-absent"), sessionReq, taskBindingTestOpts(),
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			t.Fatal("execute must never run for an absent exact_resume session")
			return nil, false, nil
		})
	if !errors.Is(err, loom.ErrAuthorityConflict) {
		t.Fatalf("err = %v, want ErrAuthorityConflict", err)
	}
	if factoryCalls.Load() != 0 {
		t.Fatalf("factory calls = %d, want 0", factoryCalls.Load())
	}
}

// TestTaskBindingCoordinatorExactResumeSwarmIdentityMismatchFailsBeforeSpawn
// proves the independent Swarm-side fence: even once Loom's durable session
// is available and matches every scoping field, an exact_resume Expected
// identity that does not resolve to a real live handle fails before any
// execution and leaves durable authority finalized (never silently reused
// as new/stateless).
func TestTaskBindingCoordinatorExactResumeSwarmIdentityMismatchFailsBeforeSpawn(t *testing.T) {
	var factoryCalls atomic.Int32
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		factoryCalls.Add(1)
		return &coordinatorFakeExecutor{
			alive: types.HealthAlive, persistent: true,
			identity: types.SessionIdentity{Provider: "neutral", ID: "resume-mismatch-identity", Generation: 1},
		}, nil
	})
	f.seedTask(t, "task-resume-setup", "coord-tenant", "coord-project")
	sessionReq := taskSessionRequest{Mode: types.SessionBindingModeNew, WorkerSessionID: "resume-mismatch-session"}
	if _, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
		defaultTaskIdent("task-resume-setup"), sessionReq, taskBindingTestOpts(), successExecute("setup-ok")); err != nil {
		t.Fatalf("setup dispatch: %v", err)
	}

	// Simulate Loom-terminal-outcome integration (T012, out of this slice's
	// scope) having already released the session back to available; the live
	// Swarm handle underneath is untouched.
	authority := f.authorityForTask(t, "task-resume-setup")
	if _, err := f.store.FinalizeWorkerRunBinding(context.Background(), loom.FinalizeWorkerRunBindingRequest{
		Authority: authority, TerminalReason: "test-simulated-terminal-release",
	}); err != nil {
		t.Fatalf("simulate terminal release: %v", err)
	}
	if state, ok := f.workerSessionState(t, "resume-mismatch-session"); !ok || state != string(loom.WorkerSessionStateAvailable) {
		t.Fatalf("worker session state = %q ok=%t, want available after simulated release", state, ok)
	}

	f.seedTask(t, "task-resume-attempt", "coord-tenant", "coord-project")
	resumeReq := taskSessionRequest{
		Mode:            types.SessionBindingModeExactResume,
		WorkerSessionID: "resume-mismatch-session",
		Expected: &types.SessionBindingIdentity{
			HandleID:           "nonexistent-handle-id",
			HandleGeneration:   1,
			RegistryGeneration: 1,
			ProviderSession:    types.SessionIdentity{Provider: "neutral", ID: "resume-mismatch-identity", Generation: 1},
		},
	}
	callsBefore := factoryCalls.Load()
	_, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
		defaultTaskIdent("task-resume-attempt"), resumeReq, taskBindingTestOpts(),
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			t.Fatal("execute must never run for a mismatched exact_resume identity")
			return nil, false, nil
		})
	if !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("err = %v, want ErrHandleNotFound", err)
	}
	if factoryCalls.Load() != callsBefore {
		t.Fatalf("factory calls changed (%d -> %d): a mismatched exact_resume must never spawn", callsBefore, factoryCalls.Load())
	}
}

// TestTaskBindingCoordinatorForkAbsentParentFailsBeforeSpawnNoDowngrade
// proves a fork request against a parent Worker Session Loom has never seen
// fails closed at reserve, before any Swarm call.
func TestTaskBindingCoordinatorForkAbsentParentFailsBeforeSpawnNoDowngrade(t *testing.T) {
	var factoryCalls atomic.Int32
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		factoryCalls.Add(1)
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-fork-1", "coord-tenant", "coord-project")

	sessionReq := taskSessionRequest{
		Mode:                  types.SessionBindingModeFork,
		WorkerSessionID:       "fork-child-session",
		ParentWorkerSessionID: "never-existed-parent",
		Parent: &types.SessionBindingIdentity{
			HandleID:           "irrelevant-parent-handle",
			HandleGeneration:   1,
			RegistryGeneration: 1,
			ProviderSession:    types.SessionIdentity{Provider: "neutral", ID: "irrelevant-parent", Generation: 1},
		},
	}
	_, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
		defaultTaskIdent("task-fork-1"), sessionReq, taskBindingTestOpts(),
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			t.Fatal("execute must never run for an absent fork parent")
			return nil, false, nil
		})
	if !errors.Is(err, loom.ErrAuthorityConflict) {
		t.Fatalf("err = %v, want ErrAuthorityConflict", err)
	}
	if factoryCalls.Load() != 0 {
		t.Fatalf("factory calls = %d, want 0", factoryCalls.Load())
	}
}

func TestTaskBindingCoordinatorForkParentProviderMismatchFailsBeforeFork(t *testing.T) {
	var factoryCalls, forkCalls, executeCalls atomic.Int32
	liveParent := &coordinatorFakeExecutor{
		alive: types.HealthAlive, persistent: true,
		identity:  types.SessionIdentity{Provider: "neutral", ID: "live-parent-b", Generation: 1},
		forkCalls: &forkCalls,
	}
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		factoryCalls.Add(1)
		return liveParent, nil
	})
	f.seedTask(t, "task-fork-parent-mismatch", "coord-tenant", "coord-project")
	parentBinding, err := f.swarm.AcquireSessionBinding(tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "coord-tenant"}), "codex",
		types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope(taskBindingTestScope))
	if err != nil {
		t.Fatalf("acquire live parent B: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO worker_sessions (
		id, tenant_id, engine_name, project_id, canonical_worktree_root,
		profile_fingerprint, capability_fingerprint, requested_mode,
		provider_name, provider_session_id, provider_session_generation, state,
		lease_generation, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"durable-parent-a", "coord-tenant", "task-binding-test", "coord-project", taskBindingTestScope,
		"profile-fp", "capability-fp", loom.RuntimeBindingModeNew,
		"neutral", "durable-parent-a", int64(1), loom.WorkerSessionStateAvailable,
		0, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed durable parent A: %v", err)
	}
	parent := types.SessionBindingIdentity{
		HandleID: parentBinding.HandleID, HandleGeneration: parentBinding.HandleGeneration,
		RegistryGeneration: parentBinding.RegistryGeneration, ProviderSession: *parentBinding.ProviderSession,
	}
	_, err = f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-fork-parent-mismatch"), taskSessionRequest{
		Mode: types.SessionBindingModeFork, WorkerSessionID: "fork-mismatch-child", ParentWorkerSessionID: "durable-parent-a", Parent: &parent,
	}, nil, func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
		executeCalls.Add(1)
		return nil, true, nil
	})
	if !errors.Is(err, loom.ErrAuthorityConflict) {
		t.Fatalf("dispatch err = %v, want ErrAuthorityConflict", err)
	}
	if factoryCalls.Load() != 1 || forkCalls.Load() != 0 || executeCalls.Load() != 0 {
		t.Fatalf("mismatched fork reached Swarm: factories=%d forks=%d executes=%d", factoryCalls.Load(), forkCalls.Load(), executeCalls.Load())
	}
	if state, ok := f.workerSessionState(t, "fork-mismatch-child"); ok {
		t.Fatalf("atomic fork rejection created child state %q", state)
	}
}

// TestTaskBindingCoordinatorLiveIdentityRecordedBeforeExecution proves
// StartWorkerRunBinding commits the live handle identity before the provider
// is ever invoked: the execute closure observes the durable row already in
// the running state with its Swarm handle recorded.
func TestTaskBindingCoordinatorLiveIdentityRecordedBeforeExecution(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-live-identity", "coord-tenant", "coord-project")

	var sawRunningWithHandle bool
	_, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
		defaultTaskIdent("task-live-identity"), taskSessionRequest{}, taskBindingTestOpts(),
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			rows := f.runBindingsForTask(t, "task-live-identity")
			if len(rows) == 1 && rows[0].State == string(loom.WorkerRunBindingStateRunning) && rows[0].SwarmHandleID.Valid && rows[0].SwarmHandleID.String != "" {
				sawRunningWithHandle = true
			}
			return &types.Response{Content: "ok"}, true, nil
		})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !sawRunningWithHandle {
		t.Fatal("execute observed no running run-binding row with a recorded Swarm handle before it ran")
	}
}

// TestTaskBindingCoordinatorStampsTenantContextForDownstreamCalls proves the
// coordinator reconstructs the real task tenant for Swarm acquisition and
// execution even though loom.go's own worker-dispatch context never carries
// it (context.Background()-derived, FR-4). Without this, non-legacy tenants
// would collapse into Swarm's shared legacy registry partition.
func TestTaskBindingCoordinatorStampsTenantContextForDownstreamCalls(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-tenant-stamp", "real-tenant-42", "coord-project")
	ident := defaultTaskIdent("task-tenant-stamp")
	ident.TenantID = "real-tenant-42"

	var observedTenant string
	var observedOK bool
	_, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project", ident, taskSessionRequest{}, taskBindingTestOpts(),
		func(execCtx context.Context, _ swarm.LiveSessionBinding, _ types.ExecutionID) (*types.Response, bool, error) {
			tc, ok := tenant.FromContext(execCtx)
			observedTenant, observedOK = tc.TenantID, ok
			return &types.Response{}, true, nil
		})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !observedOK || observedTenant != "real-tenant-42" {
		t.Fatalf("execution context tenant = (%q, ok=%t), want (\"real-tenant-42\", true)", observedTenant, observedOK)
	}
}

// TestTaskBindingCoordinatorBlankTenantFailsClosed proves a genuinely blank
// task tenant (e.g. a direct-store test fixture that bypassed
// LoomEngine.Submit's own tenant normalization) is rejected rather than
// silently minted into tenant.LegacyDefault or any other authorization.
func TestTaskBindingCoordinatorBlankTenantFailsClosed(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	if err := f.store.Create(&loom.Task{
		ID:         "task-blank-tenant",
		Status:     loom.TaskStatusRunning,
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  "coord-project",
		TenantID:   "",
		Prompt:     "test prompt",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	ident := defaultTaskIdent("task-blank-tenant")
	ident.TenantID = ""

	var executeCalls atomic.Int32
	_, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project", ident, taskSessionRequest{}, taskBindingTestOpts(),
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			executeCalls.Add(1)
			return &types.Response{}, true, nil
		})
	if err == nil {
		t.Fatal("dispatch with blank tenant ID error = nil, want fail-closed validation error")
	}
	if executeCalls.Load() != 0 {
		t.Fatalf("execute calls with blank tenant = %d, want 0", executeCalls.Load())
	}
}

// TestTaskBindingCoordinatorBlankProjectPersistsBlank proves a stateless
// no-project attempt matches the owning blank-project task and stores the
// same canonical blank value rather than inventing a mismatched sentinel.
func TestTaskBindingCoordinatorBlankProjectPersistsBlank(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	if err := f.store.Create(&loom.Task{
		ID:         "task-blank-project",
		Status:     loom.TaskStatusRunning,
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  "",
		TenantID:   "coord-tenant",
		Prompt:     "test prompt",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	ident := defaultTaskIdent("task-blank-project")
	ident.ProjectID = ""

	if _, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project", ident, taskSessionRequest{}, taskBindingTestOpts(), successExecute("ok")); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var storedProject string
	if err := f.db.QueryRowContext(context.Background(), `SELECT project_id FROM worker_run_bindings WHERE task_id=?`, "task-blank-project").Scan(&storedProject); err != nil {
		t.Fatalf("query stored project ID: %v", err)
	}
	if storedProject != "" {
		t.Fatalf("stored project ID = %q, want canonical blank", storedProject)
	}
}

func TestTaskBindingCoordinatorCanonicalScopeOverridesCallerOption(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-canonical-scope", "coord-tenant", "coord-project")

	scope := filepath.Join(os.TempDir(), "aimux-canonical-scope", "nested", "..", "project") + string(os.PathSeparator)
	wantScope, err := canonicalWorktreeRoot(scope)
	if err != nil {
		t.Fatalf("canonicalWorktreeRoot: %v", err)
	}
	var liveScope string
	_, err = f.coord.dispatch(context.Background(), "codex", scope, defaultTaskIdent("task-canonical-scope"),
		taskSessionRequest{}, []swarm.GetOption{swarm.WithScope("caller-wrong-scope")},
		func(_ context.Context, binding swarm.LiveSessionBinding, _ types.ExecutionID) (*types.Response, bool, error) {
			liveScope = binding.Scope
			return &types.Response{Content: "ok"}, true, nil
		})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var durableScope string
	if err := f.db.QueryRowContext(context.Background(), `SELECT swarm_scope FROM worker_run_bindings WHERE task_id=?`, "task-canonical-scope").Scan(&durableScope); err != nil {
		t.Fatalf("query canonical scope: %v", err)
	}
	if liveScope != wantScope || durableScope != liveScope {
		t.Fatalf("scope mismatch: want=%q live=%q durable=%q", wantScope, liveScope, durableScope)
	}
}

func TestDispatchTaskRuntimeUsesOneCanonicalScopeForExecutorAndDurableAuthority(t *testing.T) {
	messages := make(chan types.Message, 2)
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive, messages: messages}, nil
	})
	f.seedTask(t, "task-canonical-cwd", "coord-tenant", "coord-project")
	srv := &Server{
		loom:                 f.engine,
		taskSwarm:            f.swarm,
		taskRuntime:          f.runtime,
		taskBindingCoord:     f.coord,
		taskBindingCoordLoom: f.engine,
	}
	rawScope := filepath.Join(os.TempDir(), "aimux-canonical-cwd", "nested", "..", "project") + string(os.PathSeparator)
	wantScope, err := canonicalWorktreeRoot(rawScope)
	if err != nil {
		t.Fatalf("canonicalWorktreeRoot: %v", err)
	}
	spawnArgs := types.SpawnArgs{CWD: rawScope, Stdin: "hello"}
	if _, err := srv.dispatchTaskRuntime(context.Background(), "codex", spawnArgs, spawnArgs, "hello", nil, defaultTaskIdent("task-canonical-cwd"), taskSessionRequest{}); err != nil {
		t.Fatalf("dispatchTaskRuntime: %v", err)
	}
	var msg types.Message
	select {
	case msg = <-messages:
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not receive task message")
	}
	if msg.Spawn == nil || msg.Spawn.CWD != wantScope {
		t.Fatalf("executor SpawnArgs.CWD = %#v, want %q", msg.Spawn, wantScope)
	}
	var durableScope string
	if err := f.db.QueryRowContext(context.Background(), `SELECT swarm_scope FROM worker_run_bindings WHERE task_id=?`, "task-canonical-cwd").Scan(&durableScope); err != nil {
		t.Fatalf("query durable scope: %v", err)
	}
	if durableScope != wantScope {
		t.Fatalf("durable authority scope=%q want=%q", durableScope, wantScope)
	}
}

func TestDispatchTaskRuntimeSessionPreservesLaunchAndTurnInputs(t *testing.T) {
	startArgs := make(chan types.SpawnArgs, 1)
	sessionSends := make(chan string, 1)
	exec := &coordinatorFakeExecutor{
		alive:        types.HealthAlive,
		persistent:   true,
		identity:     types.SessionIdentity{Provider: "neutral", ID: "runtime-session", Generation: 1},
		startArgs:    startArgs,
		sessionSends: sessionSends,
	}
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) { return exec, nil })
	f.seedTask(t, "task-session-inputs", "coord-tenant", "coord-project")
	srv := &Server{
		loom:                 f.engine,
		taskSwarm:            f.swarm,
		taskRuntime:          f.runtime,
		taskBindingCoord:     f.coord,
		taskBindingCoordLoom: f.engine,
	}
	rawScope := filepath.Join(os.TempDir(), "aimux-session-inputs", "nested", "..", "project")
	wantScope, err := canonicalWorktreeRoot(rawScope)
	if err != nil {
		t.Fatalf("canonicalWorktreeRoot: %v", err)
	}
	turnPrompt := "execute this exact session turn"
	spawnArgs := types.SpawnArgs{Command: "codex", Args: []string{"exec", turnPrompt}, CWD: rawScope, Env: map[string]string{"TURN": "value"}}
	launchArgs := types.SpawnArgs{Command: "codex", Args: []string{"app-server"}, CWD: rawScope, Env: map[string]string{"TURN": "value"}}
	_, err = srv.dispatchTaskRuntime(context.Background(), "codex", spawnArgs, launchArgs, turnPrompt, nil,
		defaultTaskIdent("task-session-inputs"), taskSessionRequest{Mode: types.SessionBindingModeNew, WorkerSessionID: "runtime-worker-session"})
	if err != nil {
		t.Fatalf("dispatchTaskRuntime: %v", err)
	}
	select {
	case got := <-startArgs:
		if got.Command != "codex" || !stringSlicesEqual(got.Args, []string{"app-server"}) || got.CWD != wantScope || got.Env["TURN"] != "value" {
			t.Fatalf("session launch args = %#v, want canonical command/argv/CWD/env", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session factory did not receive launch args")
	}
	select {
	case got := <-sessionSends:
		if got != turnPrompt {
			t.Fatalf("session turn content = %q, want %q", got, turnPrompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bound session did not receive turn content")
	}
}

func TestTaskBindingCoordinatorUnknownModeFailsBeforeReserveOrSpawn(t *testing.T) {
	var factoryCalls atomic.Int32
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		factoryCalls.Add(1)
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-unknown-mode", "coord-tenant", "coord-project")

	_, err := f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-unknown-mode"),
		taskSessionRequest{Mode: types.SessionBindingMode("mystery")}, nil, successExecute("unexpected"))
	rows := f.runBindingsForTask(t, "task-unknown-mode")
	if err == nil || !strings.Contains(err.Error(), "unknown session binding mode") {
		t.Fatalf("dispatch err = %v, want unknown-mode rejection", err)
	}
	if factoryCalls.Load() != 0 || len(rows) != 0 {
		t.Fatalf("unknown mode reached reserve/spawn: factory=%d rows=%d", factoryCalls.Load(), len(rows))
	}
}

func TestTaskBindingCoordinatorPreProviderRejectionClosesAndStoresSafeReason(t *testing.T) {
	exec := &coordinatorFakeExecutor{alive: types.HealthAlive, persistent: true, identity: types.SessionIdentity{Provider: "neutral", ID: "pre-provider", Generation: 1}}
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) { return exec, nil })
	f.seedTask(t, "task-pre-provider", "coord-tenant", "coord-project")
	const secret = "sk-secret-must-not-be-durable"

	_, err := f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-pre-provider"),
		taskSessionRequest{Mode: types.SessionBindingModeNew, WorkerSessionID: "pre-provider-session"}, nil,
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			return nil, false, errors.New(secret)
		})
	if err == nil || !strings.Contains(err.Error(), secret) {
		t.Fatalf("dispatch err = %v, want original provider-gate error preserved", err)
	}
	if !exec.closed.Load() {
		t.Fatal("pre-provider rejection did not close the owned stateless executor")
	}
	var state string
	var leaseOwner, terminalReason sql.NullString
	if err := f.db.QueryRowContext(context.Background(), `SELECT state, lease_owner, terminal_reason FROM worker_run_bindings WHERE task_id=?`, "task-pre-provider").Scan(&state, &leaseOwner, &terminalReason); err != nil {
		t.Fatalf("query rejected binding: %v", err)
	}
	if state != string(loom.WorkerRunBindingStateTerminal) || leaseOwner.Valid || !terminalReason.Valid || terminalReason.String != taskBindingReasonPreProviderRejected {
		t.Fatalf("rejected binding = state=%q lease=%#v reason=%#v", state, leaseOwner, terminalReason)
	}
	if strings.Contains(terminalReason.String, secret) {
		t.Fatalf("terminal reason leaked secret: %q", terminalReason.String)
	}
	if state, ok := f.workerSessionState(t, "pre-provider-session"); !ok || state != string(loom.WorkerSessionStateUnavailable) {
		t.Fatalf("pre-provider worker session state = %q ok=%t, want unavailable", state, ok)
	}
}

func TestTaskBindingCoordinatorAcquireFailureFinalizesWithSafeReason(t *testing.T) {
	const secret = "sk-acquire-secret-must-not-be-durable"
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return nil, errors.New(secret)
	})
	f.seedTask(t, "task-acquire-fail", "coord-tenant", "coord-project")

	_, err := f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-acquire-fail"), taskSessionRequest{Mode: types.SessionBindingModeNew, WorkerSessionID: "acquire-fail-session"}, nil, successExecute("unexpected"))
	if err == nil || !strings.Contains(err.Error(), secret) {
		t.Fatalf("dispatch err = %v, want acquisition failure preserved", err)
	}
	var state string
	var leaseOwner, terminalReason sql.NullString
	if err := f.db.QueryRowContext(context.Background(), `SELECT state, lease_owner, terminal_reason FROM worker_run_bindings WHERE task_id=?`, "task-acquire-fail").Scan(&state, &leaseOwner, &terminalReason); err != nil {
		t.Fatalf("query acquire-failed binding: %v", err)
	}
	if state != string(loom.WorkerRunBindingStateTerminal) || leaseOwner.Valid || terminalReason.String != taskBindingReasonAcquireFailed {
		t.Fatalf("acquire-failed binding = state=%q lease=%#v reason=%#v", state, leaseOwner, terminalReason)
	}
	if strings.Contains(terminalReason.String, secret) {
		t.Fatalf("terminal reason leaked secret: %q", terminalReason.String)
	}
	if state, ok := f.workerSessionState(t, "acquire-fail-session"); !ok || state != string(loom.WorkerSessionStateUnavailable) {
		t.Fatalf("acquire-failed worker session state = %q ok=%t, want unavailable", state, ok)
	}
}

func TestTaskBindingCoordinatorReleaseFailureLeavesLeaseForReconciliation(t *testing.T) {
	exec := &coordinatorFakeExecutor{alive: types.HealthAlive, closeErr: errors.New("forced close failure")}
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) { return exec, nil })
	f.seedTask(t, "task-release-fail", "coord-tenant", "coord-project")

	_, err := f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-release-fail"), taskSessionRequest{}, nil,
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			return nil, false, errors.New("pre-provider rejection")
		})
	if err == nil || !strings.Contains(err.Error(), "forced close failure") {
		t.Fatalf("dispatch err = %v, want close failure surfaced", err)
	}
	if !exec.closed.Load() {
		t.Fatal("release failure did not attempt executor close")
	}
	rows := f.runBindingsForTask(t, "task-release-fail")
	if len(rows) != 1 || rows[0].State != string(loom.WorkerRunBindingStateRunning) || !rows[0].LeaseOwner.Valid {
		t.Fatalf("release-failed binding = %#v, want running authority retained", rows)
	}
}

func TestTaskBindingCoordinatorLiveGenerationConversionFailureClosesBeforeFinalize(t *testing.T) {
	exec := &coordinatorFakeExecutor{
		alive:      types.HealthAlive,
		persistent: true,
		identity: types.SessionIdentity{
			Provider:   "neutral",
			ID:         "overflow-live",
			Generation: uint64(math.MaxInt64) + 1,
		},
	}
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) { return exec, nil })
	f.seedTask(t, "task-live-generation-fail", "coord-tenant", "coord-project")

	session := taskSessionRequest{Mode: types.SessionBindingModeNew, WorkerSessionID: "overflow-session"}
	_, err := f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-live-generation-fail"), session, nil, successExecute("unexpected"))
	if err == nil || !strings.Contains(err.Error(), "exceeds representable range") {
		t.Fatalf("dispatch err = %v, want generation conversion failure", err)
	}
	if !exec.closed.Load() {
		t.Fatal("generation conversion failure did not close the acquired executor")
	}
	var state string
	var leaseOwner, terminalReason sql.NullString
	if err := f.db.QueryRowContext(context.Background(), `SELECT state, lease_owner, terminal_reason FROM worker_run_bindings WHERE task_id=?`, "task-live-generation-fail").Scan(&state, &leaseOwner, &terminalReason); err != nil {
		t.Fatalf("query generation-failed binding: %v", err)
	}
	if state != string(loom.WorkerRunBindingStateTerminal) || leaseOwner.Valid || terminalReason.String != taskBindingReasonLiveGenerationInvalid {
		t.Fatalf("generation-failed binding = state=%q lease=%#v reason=%#v", state, leaseOwner, terminalReason)
	}
	if state, ok := f.workerSessionState(t, "overflow-session"); !ok || state != string(loom.WorkerSessionStateUnavailable) {
		t.Fatalf("generation-failed worker session state = %q ok=%t, want unavailable", state, ok)
	}
}

func TestTaskBindingCoordinatorStartFailureClosesBeforeFinalize(t *testing.T) {
	exec := &coordinatorFakeExecutor{alive: types.HealthAlive, persistent: true, identity: types.SessionIdentity{Provider: "neutral", ID: "start-fail", Generation: 1}}
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) { return exec, nil })
	f.seedTask(t, "task-start-fail", "coord-tenant", "coord-project")
	f.coord.store = startFailStore{TaskStore: f.store}

	_, err := f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-start-fail"), taskSessionRequest{Mode: types.SessionBindingModeNew, WorkerSessionID: "start-fail-session"}, nil, successExecute("unexpected"))
	if err == nil || !strings.Contains(err.Error(), "forced start failure") {
		t.Fatalf("dispatch err = %v, want start failure", err)
	}
	if !exec.closed.Load() {
		t.Fatal("start failure did not close the acquired executor")
	}
	var state string
	var leaseOwner, terminalReason sql.NullString
	if err := f.db.QueryRowContext(context.Background(), `SELECT state, lease_owner, terminal_reason FROM worker_run_bindings WHERE task_id=?`, "task-start-fail").Scan(&state, &leaseOwner, &terminalReason); err != nil {
		t.Fatalf("query start-failed binding: %v", err)
	}
	if state != string(loom.WorkerRunBindingStateTerminal) || leaseOwner.Valid || terminalReason.String != taskBindingReasonStartFailed {
		t.Fatalf("start-failed binding = state=%q lease=%#v reason=%#v", state, leaseOwner, terminalReason)
	}
	if state, ok := f.workerSessionState(t, "start-fail-session"); !ok || state != string(loom.WorkerSessionStateUnavailable) {
		t.Fatalf("start-failed worker session state = %q ok=%t, want unavailable", state, ok)
	}
}

func TestTaskBindingCoordinatorRecordsReturnAfterCallerCancellation(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-cancelled-return", "coord-tenant", "coord-project")
	ctx, cancel := context.WithCancel(context.Background())

	_, err := f.coord.dispatch(ctx, "codex", taskBindingTestScope, defaultTaskIdent("task-cancelled-return"), taskSessionRequest{}, nil,
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			cancel()
			return &types.Response{Content: "native return"}, true, context.Canceled
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("dispatch err = %v, want context.Canceled", err)
	}
	rows := f.runBindingsForTask(t, "task-cancelled-return")
	if len(rows) != 1 || rows[0].State != string(loom.WorkerRunBindingStateReturned) || !rows[0].LeaseOwner.Valid {
		t.Fatalf("cancelled native return binding = %#v, want returned with lease retained", rows)
	}
}

func TestTaskBindingCoordinatorRecordReturnFailureSurfacesAndLeavesAuthority(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-record-return-fail", "coord-tenant", "coord-project")
	f.coord.store = recordReturnFailStore{TaskStore: f.store}

	_, err := f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-record-return-fail"), taskSessionRequest{}, nil, successExecute("native return"))
	if err == nil || !strings.Contains(err.Error(), "forced record-return failure") {
		t.Fatalf("dispatch err = %v, want record-return failure", err)
	}
	rows := f.runBindingsForTask(t, "task-record-return-fail")
	if len(rows) != 1 || rows[0].State != string(loom.WorkerRunBindingStateRunning) || !rows[0].LeaseOwner.Valid {
		t.Fatalf("record-return failure binding = %#v, want running authority retained", rows)
	}
}

func TestTaskBindingCoordinatorRenewsWhileAcquireIsBlocked(t *testing.T) {
	factoryEntered := make(chan struct{})
	releaseFactory := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFactory) }) }
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		close(factoryEntered)
		<-releaseFactory
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-renew-acquire", "coord-tenant", "coord-project")
	var renewCalls atomic.Int32
	renewed := make(chan struct{}, 1)
	f.coord.store = &renewalControlledStore{TaskStore: f.store, renewCalls: &renewCalls, renewed: renewed}
	tick := make(chan time.Time, 1)
	f.coord.newTicker = func(time.Duration) leaseRenewalTicker { return &fakeLeaseTicker{c: tick} }
	done := make(chan error, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		release()
		select {
		case <-joined:
		case <-time.After(2 * time.Second):
			t.Error("blocked acquire dispatch did not exit during cleanup")
		}
	})
	go func() {
		defer close(joined)
		_, err := f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-renew-acquire"), taskSessionRequest{}, nil, successExecute("ok"))
		done <- err
	}()

	select {
	case <-factoryEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("factory did not block")
	}
	tick <- time.Now()
	select {
	case <-renewed:
	case <-time.After(2 * time.Second):
		t.Fatal("lease was not renewed while acquire remained blocked")
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if renewCalls.Load() == 0 {
		t.Fatal("renew calls = 0")
	}
	rows := f.runBindingsForTask(t, "task-renew-acquire")
	if len(rows) != 1 || rows[0].State != string(loom.WorkerRunBindingStateReturned) || !rows[0].LeaseOwner.Valid {
		t.Fatalf("renewed binding = %#v, want returned with lease retained", rows)
	}
}

func TestTaskBindingCoordinatorRenewsAfterCallerCancellationUntilExecuteReturns(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-renew-cancelled-caller", "coord-tenant", "coord-project")
	var renewCalls atomic.Int32
	renewed := make(chan struct{}, 1)
	f.coord.store = &renewalControlledStore{TaskStore: f.store, renewCalls: &renewCalls, renewed: renewed}
	tick := make(chan time.Time, 1)
	f.coord.newTicker = func(time.Duration) leaseRenewalTicker { return &fakeLeaseTicker{c: tick} }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	executeEntered := make(chan struct{})
	releaseExecute := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseExecute) }) }
	done := make(chan error, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		release()
		select {
		case <-joined:
		case <-time.After(2 * time.Second):
			t.Error("cancelled caller dispatch did not exit during cleanup")
		}
	})
	go func() {
		defer close(joined)
		_, err := f.coord.dispatch(ctx, "codex", taskBindingTestScope, defaultTaskIdent("task-renew-cancelled-caller"), taskSessionRequest{}, nil,
			func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
				close(executeEntered)
				<-releaseExecute
				return &types.Response{Content: "native return"}, true, nil
			})
		done <- err
	}()
	select {
	case <-executeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("execute did not start")
	}
	cancel()
	tick <- time.Now()
	select {
	case <-renewed:
	case <-time.After(2 * time.Second):
		t.Fatal("lease was not renewed after caller cancellation")
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if renewCalls.Load() == 0 {
		t.Fatal("renew calls = 0")
	}
	rows := f.runBindingsForTask(t, "task-renew-cancelled-caller")
	if len(rows) != 1 || rows[0].State != string(loom.WorkerRunBindingStateReturned) || !rows[0].LeaseOwner.Valid {
		t.Fatalf("cancelled caller return binding = %#v, want returned with lease retained", rows)
	}
}

func TestTaskBindingCoordinatorExactResumePreProviderRejectionKeepsLeaseForReconciliation(t *testing.T) {
	exec := &coordinatorFakeExecutor{
		alive:      types.HealthAlive,
		persistent: true,
		identity:   types.SessionIdentity{Provider: "neutral", ID: "exact-live", Generation: 1},
	}
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) { return exec, nil })
	f.seedTask(t, "task-exact-setup", "coord-tenant", "coord-project")
	var expected types.SessionBindingIdentity
	newSession := taskSessionRequest{Mode: types.SessionBindingModeNew, WorkerSessionID: "exact-session"}
	_, err := f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-exact-setup"), newSession, nil,
		func(_ context.Context, binding swarm.LiveSessionBinding, _ types.ExecutionID) (*types.Response, bool, error) {
			expected = types.SessionBindingIdentity{
				HandleID:           binding.HandleID,
				HandleGeneration:   binding.HandleGeneration,
				RegistryGeneration: binding.RegistryGeneration,
				ProviderSession:    *binding.ProviderSession,
			}
			return &types.Response{Content: "setup"}, true, nil
		})
	if err != nil {
		t.Fatalf("setup dispatch: %v", err)
	}
	if _, err := f.store.FinalizeWorkerRunBinding(context.Background(), loom.FinalizeWorkerRunBindingRequest{
		Authority: f.authorityForTask(t, "task-exact-setup"), TerminalReason: "test_setup_complete",
	}); err != nil {
		t.Fatalf("finalize setup: %v", err)
	}

	f.seedTask(t, "task-exact-rejected", "coord-tenant", "coord-project")
	resume := taskSessionRequest{Mode: types.SessionBindingModeExactResume, WorkerSessionID: "exact-session", Expected: &expected}
	_, err = f.coord.dispatch(context.Background(), "codex", taskBindingTestScope, defaultTaskIdent("task-exact-rejected"), resume, nil,
		func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
			return nil, false, errors.New("reject before provider")
		})
	if err == nil {
		t.Fatal("exact-resume rejection error = nil")
	}
	rows := f.runBindingsForTask(t, "task-exact-rejected")
	if len(rows) != 1 || rows[0].State != string(loom.WorkerRunBindingStateRunning) || !rows[0].LeaseOwner.Valid {
		t.Fatalf("exact-resume rejected binding = %#v, want running lease retained", rows)
	}
	if state, ok := f.workerSessionState(t, "exact-session"); !ok || state != string(loom.WorkerSessionStateLeased) {
		t.Fatalf("exact session state = %q ok=%t, want leased for reconciliation", state, ok)
	}
	if exec.closed.Load() {
		t.Fatal("shared exact-resume executor was closed by a rejected turn")
	}
}

type startFailStore struct{ *loom.TaskStore }

func (startFailStore) StartWorkerRunBinding(context.Context, loom.StartWorkerRunBindingRequest) (loom.WorkerRunBindingAuthority, error) {
	return loom.WorkerRunBindingAuthority{}, errors.New("forced start failure")
}

type recordReturnFailStore struct{ *loom.TaskStore }

func (recordReturnFailStore) RecordWorkerRunBindingReturned(context.Context, loom.ReturnWorkerRunBindingRequest) (loom.WorkerRunBindingAuthority, error) {
	return loom.WorkerRunBindingAuthority{}, errors.New("forced record-return failure")
}

// TestTaskBindingCoordinatorLeaseRenewalCancelsExecutionOnFailure proves the
// renewal loop actually calls RenewWorkerRunBindingLease while a turn is
// active, and that a renewal failure cancels the execution context (fencing
// an attempt that lost its lease) with the failure surfaced to the caller —
// using a fake ticker and a store wrapper that fails renewal on demand, no
// wall-clock sleeps.
func TestTaskBindingCoordinatorLeaseRenewalCancelsExecutionOnFailure(t *testing.T) {
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) {
		return &coordinatorFakeExecutor{alive: types.HealthAlive}, nil
	})
	f.seedTask(t, "task-renew-fail", "coord-tenant", "coord-project")

	var renewCalls atomic.Int32
	var failRenew atomic.Bool
	f.coord.store = &renewalControlledStore{TaskStore: f.store, renewCalls: &renewCalls, failRenew: &failRenew}

	tick := make(chan time.Time, 1)
	f.coord.newTicker = func(time.Duration) leaseRenewalTicker { return &fakeLeaseTicker{c: tick} }

	response, err := f.coord.dispatch(context.Background(), "codex", "D:/work/project",
		defaultTaskIdent("task-renew-fail"), taskSessionRequest{}, taskBindingTestOpts(),
		func(execCtx context.Context, _ swarm.LiveSessionBinding, _ types.ExecutionID) (*types.Response, bool, error) {
			failRenew.Store(true)
			tick <- time.Now()
			<-execCtx.Done()
			return &types.Response{Content: "partial"}, true, execCtx.Err()
		})

	if renewCalls.Load() == 0 {
		t.Fatal("renew calls = 0, want at least one renewal attempt")
	}
	if err == nil || !strings.Contains(err.Error(), "renew lease") {
		t.Fatalf("dispatch err = %v, want it to surface the renewal failure", err)
	}
	if response == nil || response.Content != "partial" {
		t.Fatalf("response = %#v, want the executor's own response preserved", response)
	}
	rows := f.runBindingsForTask(t, "task-renew-fail")
	if len(rows) != 1 || rows[0].State != string(loom.WorkerRunBindingStateRunning) || !rows[0].LeaseOwner.Valid {
		t.Fatalf("renewal-failed binding = %#v, want running authority retained", rows)
	}
}

func TestTaskBindingCoordinatorPanicStopsRenewalAndClosesOwnedBinding(t *testing.T) {
	exec := &coordinatorFakeExecutor{
		alive:      types.HealthAlive,
		persistent: true,
		identity:   types.SessionIdentity{Provider: "neutral", ID: "panic-session", Generation: 1},
	}
	f := newTaskBindingFixture(t, func(string) (types.ExecutorV2, error) { return exec, nil })
	f.seedTask(t, "task-panic-cleanup", "coord-tenant", "coord-project")
	ticker := &stopAwareLeaseTicker{c: make(chan time.Time), stopped: make(chan struct{})}
	f.coord.newTicker = func(time.Duration) leaseRenewalTicker { return ticker }

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = f.coord.dispatch(context.Background(), "codex", taskBindingTestScope,
			defaultTaskIdent("task-panic-cleanup"), taskSessionRequest{Mode: types.SessionBindingModeNew, WorkerSessionID: "panic-worker-session"}, nil,
			func(context.Context, swarm.LiveSessionBinding, types.ExecutionID) (*types.Response, bool, error) {
				panic("forced provider panic")
			})
	}()
	if recovered != "forced provider panic" {
		t.Fatalf("recovered panic = %#v, want original panic", recovered)
	}
	select {
	case <-ticker.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("lease renewal ticker was not stopped after panic")
	}
	if !exec.closed.Load() {
		t.Fatal("new session live binding was not closed after panic")
	}
	rows := f.runBindingsForTask(t, "task-panic-cleanup")
	if len(rows) != 1 || rows[0].State != string(loom.WorkerRunBindingStateRunning) || !rows[0].LeaseOwner.Valid {
		t.Fatalf("panic binding = %#v, want running authority retained for reconciliation", rows)
	}
}

type renewalControlledStore struct {
	*loom.TaskStore
	renewCalls *atomic.Int32
	failRenew  *atomic.Bool
	renewed    chan struct{}
}

func (r *renewalControlledStore) RenewWorkerRunBindingLease(ctx context.Context, request loom.RenewWorkerRunBindingLeaseRequest) (loom.WorkerRunBindingAuthority, error) {
	r.renewCalls.Add(1)
	if r.failRenew != nil && r.failRenew.Load() {
		return loom.WorkerRunBindingAuthority{}, errors.New("forced renewal failure")
	}
	authority, err := r.TaskStore.RenewWorkerRunBindingLease(ctx, request)
	if r.renewed != nil {
		select {
		case r.renewed <- struct{}{}:
		default:
		}
	}
	return authority, err
}

type fakeLeaseTicker struct{ c chan time.Time }

func (f *fakeLeaseTicker) C() <-chan time.Time { return f.c }
func (f *fakeLeaseTicker) Stop()               {}

type stopAwareLeaseTicker struct {
	c       chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func (f *stopAwareLeaseTicker) C() <-chan time.Time { return f.c }
func (f *stopAwareLeaseTicker) Stop() {
	f.once.Do(func() { close(f.stopped) })
}

// --- generation conversion and worktree canonicalization -------------------

func TestInt64FromGenerationFailsClosedOnZeroAndOverflow(t *testing.T) {
	if _, err := int64FromGeneration("field", 0); err == nil {
		t.Fatal("zero generation error = nil, want failure")
	}
	if _, err := int64FromGeneration("field", uint64(math.MaxInt64)+1); err == nil {
		t.Fatal("overflowing generation error = nil, want failure")
	}
	got, err := int64FromGeneration("field", uint64(math.MaxInt64))
	if err != nil || got != math.MaxInt64 {
		t.Fatalf("boundary generation = (%d, %v), want (%d, nil)", got, err, int64(math.MaxInt64))
	}
	got, err = int64FromGeneration("field", 42)
	if err != nil || got != 42 {
		t.Fatalf("generation = (%d, %v), want (42, nil)", got, err)
	}
}

func TestCanonicalWorktreeRootFallsBackToProcessCWDWhenScopeEmpty(t *testing.T) {
	root, err := canonicalWorktreeRoot("")
	if err != nil {
		t.Fatalf("canonicalWorktreeRoot(\"\"): %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	want := filepath.ToSlash(filepath.Clean(wd))
	if len(want) >= 2 && want[1] == ':' {
		want = strings.ToUpper(want[:1]) + want[1:]
	}
	if root != want {
		t.Fatalf("root = %q, want process CWD %q", root, want)
	}
}

func TestCanonicalWorktreeRootCleansHostAbsolutePath(t *testing.T) {
	base := filepath.Join(t.TempDir(), "project")
	scope := base + string(os.PathSeparator) + "nested" + string(os.PathSeparator) + ".." + string(os.PathSeparator)
	root, err := canonicalWorktreeRoot(scope)
	if err != nil {
		t.Fatalf("canonicalWorktreeRoot(%q): %v", scope, err)
	}
	want := filepath.ToSlash(filepath.Clean(base))
	if len(want) >= 2 && want[1] == ':' {
		want = strings.ToUpper(want[:1]) + want[1:]
	}
	if root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}
}

func TestCanonicalWorktreeRootResolvesRelativeHostPath(t *testing.T) {
	scope := filepath.Join("relative", "child")
	root, err := canonicalWorktreeRoot(scope)
	if err != nil {
		t.Fatalf("canonicalWorktreeRoot: %v", err)
	}
	abs, err := filepath.Abs(scope)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	want := filepath.ToSlash(filepath.Clean(abs))
	if len(want) >= 2 && want[1] == ':' {
		want = strings.ToUpper(want[:1]) + want[1:]
	}
	if root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}
}

func TestCanonicalWorktreeRootUsesHostSeparatorSemantics(t *testing.T) {
	if os.PathSeparator == '/' {
		scope := filepath.Join(t.TempDir(), `literal\name`)
		root, err := canonicalWorktreeRoot(scope)
		if err != nil {
			t.Fatalf("canonicalWorktreeRoot: %v", err)
		}
		if root != filepath.Clean(scope) || !strings.Contains(root, `\`) {
			t.Fatalf("POSIX root = %q, want literal backslash path %q", root, filepath.Clean(scope))
		}
		return
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	volume := filepath.VolumeName(wd)
	root, err := canonicalWorktreeRoot(`\work\project\`)
	if err != nil {
		t.Fatalf("canonicalWorktreeRoot: %v", err)
	}
	want := filepath.ToSlash(filepath.Clean(volume + `\work\project\`))
	if root != want {
		t.Fatalf("Windows root-relative path = %q, want %q", root, want)
	}
}

func TestTaskProfileFingerprintIsStableAndTracksEffectiveProfile(t *testing.T) {
	profile := &config.CLIProfile{
		Name:                 "test",
		Binary:               "test-cli",
		DisplayName:          "Test CLI",
		Features:             types.CLIFeatures{Streaming: true, Headless: true, ReadOnly: true},
		OutputFormat:         "json",
		Command:              config.CommandConfig{Base: "test-cli", ArgsTemplate: "--run {{prompt}}"},
		PromptFlag:           "--prompt",
		PromptFlagType:       "separate",
		DefaultModel:         "model-a",
		ModelFlag:            "--model",
		Reasoning:            &config.ReasoningConfig{Flag: "--reasoning", FlagValueTemplate: "{{level}}", Levels: []string{"low", "high"}},
		TimeoutSeconds:       10,
		StdinThreshold:       100,
		StdinSentinel:        "-",
		CompletionPattern:    "done",
		ReadOnlyFlags:        []string{"--read-only"},
		WriteSandboxFlags:    []string{"--write"},
		HeadlessFlags:        []string{"--headless"},
		SearchPaths:          []string{"src"},
		MCPSuppressionFlags:  []string{"--no-mcp"},
		Capabilities:         []string{"task"},
		ModelFallback:        []string{"model-b"},
		FallbackSuffixStrip:  []string{"-mini"},
		CooldownSeconds:      30,
		WarmupTimeoutSeconds: 5,
		WarmupProbePrompt:    "ping",
		EnvPassthrough:       []string{"HOME"},
		RequiresTTY:          true,
		ResolvedPath:         "/bin/test-cli",
	}
	want := taskProfileFingerprint("test", profile)
	if got := taskProfileFingerprint("test", profile); got != want {
		t.Fatalf("fingerprint changed without profile mutation: got %q, want %q", got, want)
	}
	mutations := []struct {
		name   string
		mutate func(*config.CLIProfile)
	}{
		{"command", func(p *config.CLIProfile) { p.Command.Base = "other-cli" }},
		{"prompt", func(p *config.CLIProfile) { p.PromptFlag = "--input" }},
		{"model", func(p *config.CLIProfile) { p.DefaultModel = "model-b" }},
		{"reasoning", func(p *config.CLIProfile) {
			p.Reasoning = &config.ReasoningConfig{Flag: "--effort", Levels: []string{"max"}}
		}},
		{"flags", func(p *config.CLIProfile) { p.HeadlessFlags = []string{"--batch"} }},
		{"environment", func(p *config.CLIProfile) { p.EnvPassthrough = []string{"PATH"} }},
		{"capabilities", func(p *config.CLIProfile) { p.Capabilities = []string{"review"} }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := *profile
			mutation.mutate(&changed)
			if got := taskProfileFingerprint("test", &changed); got == want {
				t.Fatal("fingerprint did not change")
			}
		})
	}
}
