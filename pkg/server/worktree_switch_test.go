package server

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/mcp-mux/muxcore"
)

const worktreeSwitchInitializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

func TestWorktreeSwitchDrainSuccessWaitsForPreviousProjectTasks(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 2})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1001)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	projectA := muxcore.ProjectContext{ID: "worktree-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "worktree-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	taskID, worker := submitBlockingLoomTaskWithTenant(t, srv, projectA.ID, worktreeSwitchSessionKey(1001), "tenant-a")

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(worker.release)
	}()

	if _, err := delegate.projectStateForRequest(ctx, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest: %v", err)
	}
	task := waitForWorktreeTaskTerminal(t, srv, taskID, 2*time.Second)
	if task.Status != loom.TaskStatusCompleted {
		t.Fatalf("task status = %s, want completed", task.Status)
	}
	if last := delegate.lastProjectForSession(worktreeSwitchSessionKey(1001)); last != projectB.ID {
		t.Fatalf("last project = %q, want %q", last, projectB.ID)
	}
}

func TestWorktreeSwitchDrainTimeoutAcceptsNewProject(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 1})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1002)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	projectA := muxcore.ProjectContext{ID: "timeout-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "timeout-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	taskID, _ := submitBlockingLoomTaskWithTenant(t, srv, projectA.ID, worktreeSwitchSessionKey(1002), "tenant-a")

	start := time.Now()
	if _, err := delegate.projectStateForRequest(ctx, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Fatalf("switch returned before drain timeout tolerance: elapsed=%v", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("switch took unexpectedly long: elapsed=%v", elapsed)
	}
	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("loom.Get: %v", err)
	}
	if task.Status != loom.TaskStatusRunning {
		t.Fatalf("task status = %s, want still running after timeout", task.Status)
	}
	if last := delegate.lastProjectForSession(worktreeSwitchSessionKey(1002)); last != projectB.ID {
		t.Fatalf("last project = %q, want %q", last, projectB.ID)
	}
}

func TestWorktreeSwitchDrainMissingTenantFailsClosed(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 1})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1011))
	projectA := muxcore.ProjectContext{ID: "drain-missing-tenant-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "drain-missing-tenant-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	taskID, _ := submitBlockingLoomTask(t, srv, projectA.ID, worktreeSwitchSessionKey(1011))

	start := time.Now()
	if _, err := delegate.projectStateForRequest(ctx, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("switch waited without tenant scope: elapsed=%v", elapsed)
	}
	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("loom.Get: %v", err)
	}
	if task.Status != loom.TaskStatusRunning {
		t.Fatalf("task status = %s, want still running when tenant scope is unavailable", task.Status)
	}
}

func TestWorktreeSwitchDrainIgnoresOtherTenantTasks(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 1})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1006)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	projectA := muxcore.ProjectContext{ID: "drain-tenant-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "drain-tenant-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	tenantBTaskID, _ := submitBlockingLoomTaskForTenant(t, srv, projectA.ID, "tenant-b")

	start := time.Now()
	if _, err := delegate.projectStateForRequest(ctx, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("switch waited on other tenant task: elapsed=%v", elapsed)
	}
	tenantBTask, err := srv.loom.Get(tenantBTaskID)
	if err != nil {
		t.Fatalf("loom.Get tenant B: %v", err)
	}
	if tenantBTask.Status != loom.TaskStatusRunning {
		t.Fatalf("tenant B task status = %s, want still running", tenantBTask.Status)
	}
}

func TestWorktreeSwitchDrainIgnoresOtherSessionTasks(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 1})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctxA := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1009)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	ctxB := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1010)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	projectA := muxcore.ProjectContext{ID: "drain-session-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "drain-session-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctxA, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest session A: %v", err)
	}
	if _, err := delegate.projectStateForRequest(ctxB, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest session B: %v", err)
	}
	sessionBTaskID, _ := submitBlockingLoomTaskWithTenant(t, srv, projectA.ID, worktreeSwitchSessionKey(1010), "tenant-a")

	start := time.Now()
	if _, err := delegate.projectStateForRequest(ctxA, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest session A: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("switch waited on other session task: elapsed=%v", elapsed)
	}
	sessionBTask, err := srv.loom.Get(sessionBTaskID)
	if err != nil {
		t.Fatalf("loom.Get session B: %v", err)
	}
	if sessionBTask.Status != loom.TaskStatusRunning {
		t.Fatalf("session B task status = %s, want still running", sessionBTask.Status)
	}
}

func TestWorktreeSwitchForcedSwitchCancelsPreviousProjectTasks(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 30, ForcedSwitch: true})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1003)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	projectA := muxcore.ProjectContext{ID: "forced-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "forced-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	sessionKey := worktreeSwitchSessionKey(1003)
	taskID, _ := submitBlockingLoomTaskWithTenant(t, srv, projectA.ID, sessionKey, "tenant-a")

	if _, err := delegate.projectStateForRequest(ctx, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest: %v", err)
	}
	task := waitForWorktreeTaskTerminal(t, srv, taskID, 2*time.Second)
	if task.Status != loom.TaskStatusFailed {
		t.Fatalf("task status = %s, want failed", task.Status)
	}
	if !strings.Contains(task.Error, "Canceled") || !strings.Contains(task.Error, "worktree switched mid-task") {
		t.Fatalf("task error = %q, want canceled worktree switch message", task.Error)
	}
}

func TestWorktreeSwitchForcedSwitchMissingTenantFailsClosed(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 30, ForcedSwitch: true})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1012))
	projectA := muxcore.ProjectContext{ID: "forced-missing-tenant-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "forced-missing-tenant-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	sessionKey := worktreeSwitchSessionKey(1012)
	taskID, _ := submitBlockingLoomTask(t, srv, projectA.ID, sessionKey)

	if _, err := delegate.projectStateForRequest(ctx, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest: %v", err)
	}
	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("loom.Get: %v", err)
	}
	if task.Status != loom.TaskStatusRunning {
		t.Fatalf("task status = %s, want still running when tenant scope is unavailable", task.Status)
	}
}

func TestWorktreeSwitchForcedSwitchCancelsOnlyCurrentTenant(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 30, ForcedSwitch: true})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1005)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	projectA := muxcore.ProjectContext{ID: "forced-tenant-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "forced-tenant-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	sessionKey := worktreeSwitchSessionKey(1005)
	tenantATaskID, _ := submitBlockingLoomTaskWithTenant(t, srv, projectA.ID, sessionKey, "tenant-a")
	tenantBTaskID, _ := submitBlockingLoomTaskWithTenant(t, srv, projectA.ID, sessionKey, "tenant-b")

	if _, err := delegate.projectStateForRequest(ctx, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest: %v", err)
	}
	tenantATask := waitForWorktreeTaskTerminal(t, srv, tenantATaskID, 2*time.Second)
	if tenantATask.Status != loom.TaskStatusFailed {
		t.Fatalf("tenant A task status = %s, want failed", tenantATask.Status)
	}
	tenantBTask, err := srv.loom.Get(tenantBTaskID)
	if err != nil {
		t.Fatalf("loom.Get tenant B: %v", err)
	}
	if tenantBTask.Status != loom.TaskStatusRunning {
		t.Fatalf("tenant B task status = %s, want still running", tenantBTask.Status)
	}
}

func TestWorktreeSwitchForcedSwitchCancelsOnlyCurrentSession(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 30, ForcedSwitch: true})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctxA := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1007)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	ctxB := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1008)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	projectA := muxcore.ProjectContext{ID: "forced-session-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "forced-session-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctxA, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest session A: %v", err)
	}
	if _, err := delegate.projectStateForRequest(ctxB, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest session B: %v", err)
	}
	sessionAKey := worktreeSwitchSessionKey(1007)
	sessionBKey := worktreeSwitchSessionKey(1008)
	sessionATaskID, _ := submitBlockingLoomTaskWithTenant(t, srv, projectA.ID, sessionAKey, "tenant-a")
	sessionBTaskID, _ := submitBlockingLoomTaskWithTenant(t, srv, projectA.ID, sessionBKey, "tenant-a")

	if _, err := delegate.projectStateForRequest(ctxA, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest session A: %v", err)
	}
	sessionATask := waitForWorktreeTaskTerminal(t, srv, sessionATaskID, 2*time.Second)
	if sessionATask.Status != loom.TaskStatusFailed {
		t.Fatalf("session A task status = %s, want failed", sessionATask.Status)
	}
	sessionBTask, err := srv.loom.Get(sessionBTaskID)
	if err != nil {
		t.Fatalf("loom.Get session B: %v", err)
	}
	if sessionBTask.Status != loom.TaskStatusRunning {
		t.Fatalf("session B task status = %s, want still running", sessionBTask.Status)
	}
}

func TestWorktreeSwitchDrainIgnoresClientCancellation(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 2})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := tenant.WithContext(
		contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1013)),
		tenant.TenantContext{TenantID: "tenant-a"},
	)
	projectA := muxcore.ProjectContext{ID: "drain-cancel-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "drain-cancel-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	taskID, worker := submitBlockingLoomTaskWithTenant(t, srv, projectA.ID, worktreeSwitchSessionKey(1013), "tenant-a")
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(worker.release)
	}()

	if _, err := delegate.projectStateForRequest(canceledCtx, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest with canceled client context: %v", err)
	}
	task := waitForWorktreeTaskTerminal(t, srv, taskID, 2*time.Second)
	if task.Status != loom.TaskStatusCompleted {
		t.Fatalf("task status = %s, want completed", task.Status)
	}
	if last := delegate.lastProjectForSession(worktreeSwitchSessionKey(1013)); last != projectB.ID {
		t.Fatalf("last project = %q, want %q", last, projectB.ID)
	}
}

func TestWorktreeSwitchSameProjectBypassesDrain(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 1})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1004))
	projectA := muxcore.ProjectContext{ID: "same-a", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	submitBlockingLoomTask(t, srv, projectA.ID, "")

	start := time.Now()
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("same projectStateForRequest: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("same-project request drained unexpectedly: elapsed=%v", elapsed)
	}
}

func TestHandleRequestWithSessionMetaTracksWorktree(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 1})
	handler := srv.SessionHandler()
	metaHandler, ok := handler.(muxcore.SessionHandlerWithSessionMeta)
	if !ok {
		t.Fatal("handler does not implement SessionHandlerWithSessionMeta")
	}
	h := handler.(*aimuxHandler)
	srv.swapDelegateToFull(h, time.Now())
	delegate := h.currentDelegate().(*fullDelegate)

	projectA := muxcore.ProjectContext{ID: "meta-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "meta-b", Cwd: t.TempDir()}
	lifecycle := handler.(muxcore.ProjectLifecycle)
	lifecycle.OnProjectConnect(projectA)
	lifecycle.OnProjectConnect(projectB)

	meta := worktreeSwitchSessionMeta(2001)
	req := []byte(worktreeSwitchInitializeRequest)
	if _, err := metaHandler.HandleRequestWithSessionMeta(context.Background(), projectA, meta, req); err != nil {
		t.Fatalf("HandleRequestWithSessionMeta projectA: %v", err)
	}
	if _, err := metaHandler.HandleRequestWithSessionMeta(context.Background(), projectB, meta, req); err != nil {
		t.Fatalf("HandleRequestWithSessionMeta projectB: %v", err)
	}

	if last := delegate.lastProjectForSession(worktreeSwitchSessionKey(2001)); last != projectB.ID {
		t.Fatalf("last project = %q, want %q", last, projectB.ID)
	}
}

func TestWorktreeSessionKeyPrefersMuxSessionID(t *testing.T) {
	ctx := contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(2001))
	ctx = contextWithMuxSessionID(ctx, "sess_abc12345")

	key, ok := worktreeSessionKeyFromContext(ctx)
	if !ok {
		t.Fatal("worktreeSessionKeyFromContext ok = false, want true")
	}
	if key != "mux:sess_abc12345" {
		t.Fatalf("key = %q, want mux session id key", key)
	}
}

func TestMuxSessionIDFromRequest(t *testing.T) {
	got := muxSessionIDFromRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"muxSessionId":"sess_abc12345"}}}`))
	if got != "sess_abc12345" {
		t.Fatalf("mux session id = %q, want sess_abc12345", got)
	}
}

func testServerWithWorktreeSwitch(t *testing.T, cfg config.WorktreeConfig) *Server {
	t.Helper()
	srv := testServerWithLoom(t)
	srv.cfg.Worktree = cfg
	return srv
}

func newWorktreeSwitchTestDelegate(srv *Server) *fullDelegate {
	return &fullDelegate{srv: srv}
}

func worktreeSwitchSessionMeta(pid int) muxcore.SessionMeta {
	return muxcore.SessionMeta{
		Conn: muxcore.ConnInfo{
			PeerPid:  pid,
			Platform: muxcore.PlatformLinuxUnix,
		},
	}
}

func worktreeSwitchSessionKey(pid int) string {
	return "linux-unix-stream:pid:" + strconv.Itoa(pid)
}

func waitForWorktreeTaskTerminal(t *testing.T, srv *Server, taskID string, timeout time.Duration) *loom.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := srv.loom.Get(taskID)
		if err != nil {
			t.Fatalf("loom.Get: %v", err)
		}
		if task.Status.IsTerminal() {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("loom.Get: %v", err)
	}
	t.Fatalf("task %s did not reach terminal status within %v; status=%s", taskID, timeout, task.Status)
	return nil
}
