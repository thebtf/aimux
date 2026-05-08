package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	codeworker "github.com/thebtf/aimux/pkg/executor/code"
	applygate "github.com/thebtf/aimux/pkg/executor/code/gate"
)

// @critical - release blocker per Constitution rule #10.
func TestE2E_ResumeRejectsCrossWorktree(t *testing.T) {
	if os.Getenv("AIMUX21_E2E") != "1" {
		t.Skip("AIMUX21_E2E=1 not set - skipping cross-worktree resume e2e")
	}

	root := projectRoot()
	worktreeParent := t.TempDir()
	worktreeA := filepath.Join(worktreeParent, "worktree-a")
	worktreeB := filepath.Join(worktreeParent, "worktree-b")
	addResumeGitWorktree(t, root, worktreeA)
	addResumeGitWorktree(t, root, worktreeB)

	engine := newResumeE2ELoom(t)
	driver := &resumeProbeDriver{}
	engine.RegisterWorker(codeworker.WorkerTypeCodeDriver, driver)
	engine.RegisterWorker(codeworker.WorkerTypeCodeNavigator, resumeStaticNavigator{})
	codeEntry, err := codeworker.NewCodeWorker(codeworker.CodeWorkerConfig{
		Loom:         engine,
		DriverCLI:    "codex",
		NavigatorCLI: "claude",
		MaxRounds:    1,
		Apply: func(context.Context, string, codeworker.Project) (int, int, error) {
			return 1, 1, nil
		},
		GateRunner: codeworker.GateRunnerFunc(func(context.Context, applygate.Project) applygate.Result {
			return applygate.Result{Status: applygate.StatusSkipped, Reason: string(applygate.PhaseTests)}
		}),
	})
	if err != nil {
		t.Fatalf("NewCodeWorker: %v", err)
	}
	engine.RegisterWorker(codeworker.WorkerTypeCode, codeEntry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	taskAID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: codeworker.WorkerTypeCode,
		ProjectID:  "aimux21-worktree-a",
		RequestID:  "aimux21-worktree-a",
		Prompt:     "first code task in worktree A",
		CWD:        worktreeA,
		Metadata: map[string]any{
			codeworker.MetadataThreadID:   "thread-A",
			codeworker.MetadataWorkerType: string(codeworker.WorkerTypeCode),
		},
	})
	if err != nil {
		t.Fatalf("submit task A: %v", err)
	}
	taskA := waitForResumeTask(t, ctx, engine, taskAID)
	if taskA.Status != loom.TaskStatusCompleted {
		t.Fatalf("task A status = %s error=%q result=%q", taskA.Status, taskA.Error, taskA.Result)
	}

	taskBID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: codeworker.WorkerTypeCode,
		ProjectID:  "aimux21-worktree-b",
		RequestID:  "aimux21-worktree-b",
		Prompt:     "continue code task in worktree B",
		CWD:        worktreeB,
		Metadata: map[string]any{
			"resume_id": taskAID,
		},
	})
	if err != nil {
		t.Fatalf("submit task B: %v", err)
	}
	taskB := waitForResumeTask(t, ctx, engine, taskBID)
	if taskB.Status != loom.TaskStatusFailed {
		t.Fatalf("task B status = %s, want failed; error=%q result=%q", taskB.Status, taskB.Error, taskB.Result)
	}
	if !strings.Contains(taskB.Error, "ResumeWorkerMismatch") || !strings.Contains(taskB.Error, "cross-worktree resume rejected") {
		t.Fatalf("task B error = %q, want cross-worktree ResumeWorkerMismatch", taskB.Error)
	}
	if got := resumeDriverCallCount(driver); got != 1 {
		t.Fatalf("driver calls = %d, want only original worktree-A task to run", got)
	}
}

func addResumeGitWorktree(t *testing.T, root string, path string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "worktree", "add", "--detach", path, "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree add %s: %v\n%s", path, err, out)
	}
	t.Cleanup(func() {
		remove := exec.Command("git", "-C", root, "worktree", "remove", "--force", path)
		if out, err := remove.CombinedOutput(); err != nil {
			t.Logf("git worktree remove %s: %v\n%s", path, err, out)
		}
		prune := exec.Command("git", "-C", root, "worktree", "prune")
		if out, err := prune.CombinedOutput(); err != nil {
			t.Logf("git worktree prune: %v\n%s", err, out)
		}
	})
}

func resumeDriverCallCount(driver *resumeProbeDriver) int {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return len(driver.metadata)
}
