package server

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/loom/deps"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/mcp-mux/muxcore"
	_ "modernc.org/sqlite"
)

type blockingLoomWorker struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingLoomWorker() *blockingLoomWorker {
	return &blockingLoomWorker{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *blockingLoomWorker) Type() loom.WorkerType {
	return loom.WorkerTypeCLI
}

func (w *blockingLoomWorker) Execute(ctx context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
	close(w.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.release:
		return &loom.WorkerResult{Content: "loom output"}, nil
	}
}

func testServerWithLoom(t *testing.T) *Server {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{
			LogLevel:              "error",
			LogFile:               filepath.Join(t.TempDir(), "test.log"),
			DefaultTimeoutSeconds: 10,
		},
		Roles: map[string]types.RolePreference{
			"default": {CLI: "codex"},
			"coding":  {CLI: "codex"},
		},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 3,
			CooldownSeconds:  5,
			HalfOpenMaxCalls: 1,
		},
		CLIProfiles: map[string]*config.CLIProfile{
			"codex": {
				Name:           "codex",
				Binary:         testBinary(),
				TimeoutSeconds: 10,
				Capabilities:   []string{"coding", "default"},
			},
		},
	}

	log, err := logger.New(cfg.Server.LogFile, logger.LevelError, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	registry := driver.NewRegistry(cfg.CLIProfiles)
	router := routing.NewRouterWithProfiles(cfg.Roles, registry.EnabledCLIs(), cfg.CLIProfiles)
	srv := New(cfg, log, registry, router)

	db, err := sql.Open("sqlite", "file:"+t.Name()+"?cache=shared&mode=memory")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	store, err := loom.NewTaskStore(db, "test")
	if err != nil {
		t.Fatalf("loom.NewTaskStore: %v", err)
	}
	srv.loom = loom.New(store, loom.WithLogger(deps.NoopLogger()))
	t.Cleanup(func() {
		srv.Shutdown()
		_ = log.Close()
		_ = db.Close()
	})
	return srv
}

func projectCtx(projectID string) context.Context {
	return context.WithValue(context.Background(), projectContextKey{}, muxcore.ProjectContext{
		ID: muxcore.ProjectContextID(projectID),
	})
}

func projectCtxAndID(projectID string) (context.Context, string) {
	ctx := projectCtx(projectID)
	return ctx, projectIDFromContext(ctx)
}

func submitBlockingLoomTask(t *testing.T, srv *Server, projectID, sessionID string) (string, *blockingLoomWorker) {
	t.Helper()

	worker := newBlockingLoomWorker()
	srv.loom.RegisterWorker(loom.WorkerTypeCLI, worker)
	taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "block",
		Metadata:   map[string]any{"session_id": sessionID},
	})
	if err != nil {
		t.Fatalf("loom.Submit: %v", err)
	}
	select {
	case <-worker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("loom worker did not start")
	}
	t.Cleanup(func() {
		select {
		case <-worker.release:
		default:
			close(worker.release)
		}
	})
	return taskID, worker
}

func TestHandleStatus_LoomTaskPrimary(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-status")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")
	if err := srv.loom.AppendProgress(taskID, "loom progress"); err != nil {
		t.Fatalf("AppendProgress: %v", err)
	}

	result, err := srv.handleStatus(ctx, makeRequest("status", map[string]any{"job_id": taskID}))
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	data := parseResult(t, result)
	if data["job_id"] != taskID {
		t.Fatalf("job_id = %v, want %s", data["job_id"], taskID)
	}
	if data["status"] != string(loom.TaskStatusRunning) {
		t.Fatalf("status = %v, want running", data["status"])
	}
	if data["progress_tail"] != "loom progress" {
		t.Fatalf("progress_tail = %v, want loom progress", data["progress_tail"])
	}
	if data["progress_lines"].(float64) < 1 {
		t.Fatalf("progress_lines = %v, want >= 1", data["progress_lines"])
	}
}

func TestSessionsHealth_CountsLoomRunningTasks(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-health")
	submitBlockingLoomTask(t, srv, projectID, "")

	result, err := srv.handleSessions(ctx, makeRequest("sessions", map[string]any{"action": "health"}))
	if err != nil {
		t.Fatalf("handleSessions health: %v", err)
	}
	data := parseResult(t, result)
	if data["running_jobs"] != float64(1) {
		t.Fatalf("running_jobs = %v, want 1", data["running_jobs"])
	}
	if data["loom_tasks"] != float64(1) {
		t.Fatalf("loom_tasks = %v, want 1", data["loom_tasks"])
	}
}

func TestSessionsList_IncludesLoomTasksWithoutProjectContext(t *testing.T) {
	srv := testServerWithLoom(t)
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, t.TempDir())
	taskID, _ := submitBlockingLoomTask(t, srv, "proj-list-default", sess.ID)
	if err := srv.loom.AppendProgress(taskID, "loom list progress"); err != nil {
		t.Fatalf("AppendProgress: %v", err)
	}

	result, err := srv.handleSessions(context.Background(), makeRequest("sessions", map[string]any{
		"action": "list",
	}))
	if err != nil {
		t.Fatalf("handleSessions list: %v", err)
	}
	data := parseResult(t, result)

	sessions, ok := data["sessions"].([]any)
	if !ok {
		t.Fatalf("sessions type = %T, want []any", data["sessions"])
	}
	foundSession := false
	for _, raw := range sessions {
		row := raw.(map[string]any)
		if row["id"] == sess.ID {
			foundSession = true
			if row["job_count"] != float64(1) {
				t.Fatalf("job_count = %v, want 1", row["job_count"])
			}
		}
	}
	if !foundSession {
		t.Fatalf("sessions list did not include session %s: %v", sess.ID, sessions)
	}

	loomTasks, ok := data["loom_tasks"].([]any)
	if !ok {
		t.Fatalf("loom_tasks type = %T, want []any", data["loom_tasks"])
	}
	for _, raw := range loomTasks {
		row := raw.(map[string]any)
		if row["id"] == taskID {
			if row["progress_line_count"] != float64(1) {
				t.Fatalf("progress_line_count = %v, want 1", row["progress_line_count"])
			}
			return
		}
	}
	t.Fatalf("sessions list did not include loom task %s: %v", taskID, loomTasks)
}

func TestSessionsInfo_IncludesLoomTasksBySessionMetadata(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-info")
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, t.TempDir())
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, sess.ID)

	result, err := srv.handleSessions(ctx, makeRequest("sessions", map[string]any{
		"action":     "info",
		"session_id": sess.ID,
	}))
	if err != nil {
		t.Fatalf("handleSessions info: %v", err)
	}
	data := parseResult(t, result)
	jobs, ok := data["jobs"].([]any)
	if !ok {
		t.Fatalf("jobs type = %T, want []any", data["jobs"])
	}
	for _, raw := range jobs {
		job := raw.(map[string]any)
		if job["id"] == taskID {
			return
		}
	}
	t.Fatalf("sessions info did not include loom task %s: %v", taskID, jobs)
}

func TestSessionsKill_FailsLoomTasksBySessionMetadata(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-kill")
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, t.TempDir())
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, sess.ID)

	result, err := srv.handleSessions(ctx, makeRequest("sessions", map[string]any{
		"action":     "kill",
		"session_id": sess.ID,
	}))
	if err != nil {
		t.Fatalf("handleSessions kill: %v", err)
	}
	if result.IsError {
		t.Fatalf("kill returned error: %v", parseResult(t, result))
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		task, getErr := srv.loom.Get(taskID)
		if getErr != nil {
			t.Fatalf("loom.Get: %v", getErr)
		}
		if task.Status == loom.TaskStatusFailed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("task status = %s, want failed", task.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSessionsCancel_FailsPendingLoomTask(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-cancel")
	taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeInvestigator,
		ProjectID:  projectID,
		Prompt:     "no worker registered",
	})
	if err != nil {
		t.Fatalf("loom.Submit: %v", err)
	}
	if _, ok, getErr := srv.getLoomTask(ctx, taskID); getErr != nil || !ok {
		t.Fatalf("getLoomTask ok=%v err=%v", ok, getErr)
	}

	result, err := srv.handleSessions(ctx, makeRequest("sessions", map[string]any{
		"action": "cancel",
		"job_id": taskID,
	}))
	if err != nil {
		t.Fatalf("handleSessions cancel: %v", err)
	}
	if result.IsError {
		t.Fatalf("cancel returned error: %v", parseResult(t, result))
	}

	task, err := srv.loom.Get(taskID)
	if err != nil && !errors.Is(err, loom.ErrTaskNotFound) {
		t.Fatalf("loom.Get: %v", err)
	}
	if task != nil && task.Status.IsActive() {
		t.Fatalf("task status = %s, want terminal after cancel", task.Status)
	}
}
