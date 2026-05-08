package server

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/mcp-mux/muxcore"
)

const worktreeSwitchInitializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

func TestWorktreeSwitchDrainSuccessWaitsForPreviousProjectTasks(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 2})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1001))
	projectA := muxcore.ProjectContext{ID: "worktree-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "worktree-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	taskID, worker := submitBlockingLoomTask(t, srv, projectA.ID, "")

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
	ctx := contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1002))
	projectA := muxcore.ProjectContext{ID: "timeout-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "timeout-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	taskID, _ := submitBlockingLoomTask(t, srv, projectA.ID, "")

	start := time.Now()
	if _, err := delegate.projectStateForRequest(ctx, projectB); err != nil {
		t.Fatalf("switch projectStateForRequest: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < time.Second {
		t.Fatalf("switch returned before drain timeout: elapsed=%v", elapsed)
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

func TestWorktreeSwitchForcedSwitchCancelsPreviousProjectTasks(t *testing.T) {
	srv := testServerWithWorktreeSwitch(t, config.WorktreeConfig{DrainTimeoutSeconds: 30, ForcedSwitch: true})
	delegate := newWorktreeSwitchTestDelegate(srv)
	ctx := contextWithSessionMeta(context.Background(), worktreeSwitchSessionMeta(1003))
	projectA := muxcore.ProjectContext{ID: "forced-a", Cwd: t.TempDir()}
	projectB := muxcore.ProjectContext{ID: "forced-b", Cwd: t.TempDir()}
	delegate.OnProjectConnect(projectA)
	delegate.OnProjectConnect(projectB)
	if _, err := delegate.projectStateForRequest(ctx, projectA); err != nil {
		t.Fatalf("initial projectStateForRequest: %v", err)
	}
	taskID, _ := submitBlockingLoomTask(t, srv, projectA.ID, "")

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
