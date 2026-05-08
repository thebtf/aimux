package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	codeworker "github.com/thebtf/aimux/pkg/executor/code"
	applygate "github.com/thebtf/aimux/pkg/executor/code/gate"
	"github.com/thebtf/mcp-mux/muxcore"
	_ "modernc.org/sqlite"
)

const aimux21WorktreeReadmeMarker = "<!-- AIMUX-21 worktree e2e -->"

// @critical - release blocker per Constitution rule #10.
func TestE2E_AIMUX21WorktreeNativeIsolation(t *testing.T) {
	if os.Getenv("AIMUX21_E2E") != "1" {
		t.Skip("AIMUX21_E2E=1 not set - skipping AIMUX-21 worktree native e2e")
	}

	root := aimux21WorktreeProjectRoot(t)
	worktreeParent := t.TempDir()
	worktreeA := filepath.Join(worktreeParent, "worktree-a")
	worktreeB := filepath.Join(worktreeParent, "worktree-b")
	aimux21AddGitWorktree(t, root, worktreeA)
	aimux21AddGitWorktree(t, root, worktreeB)

	projectIDA := muxcore.ProjectContextID(worktreeA)
	projectIDB := muxcore.ProjectContextID(worktreeB)
	if projectIDA == projectIDB {
		t.Fatalf("project IDs unexpectedly match: %s", projectIDA)
	}

	outsidePath := filepath.Join(t.TempDir(), "outside-worktree.txt")
	engine := aimux21WorktreeLoom(t)
	engine.RegisterWorker(codeworker.WorkerTypeCodeDriver, aimux21WorktreeDriver{outsidePath: outsidePath})
	engine.RegisterWorker(codeworker.WorkerTypeCodeNavigator, aimux21WorktreeNavigator{})
	codeEntry, err := codeworker.NewCodeWorker(codeworker.CodeWorkerConfig{
		Loom:         engine,
		DriverCLI:    "codex",
		NavigatorCLI: "claude",
		MaxRounds:    1,
		GateRunner: codeworker.GateRunnerFunc(func(context.Context, applygate.Project) applygate.Result {
			return applygate.Result{Status: applygate.StatusSkipped, Reason: string(applygate.PhaseTests)}
		}),
	})
	if err != nil {
		t.Fatalf("NewCodeWorker: %v", err)
	}
	engine.RegisterWorker(codeworker.WorkerTypeCode, codeEntry)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	taskAID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: codeworker.WorkerTypeCode,
		ProjectID:  projectIDA,
		RequestID:  "aimux21-worktree-a",
		Prompt:     "add comment to README.md",
		CWD:        worktreeA,
		Metadata: map[string]any{
			codeworker.MetadataThreadID:   "thread-worktree-a",
			codeworker.MetadataWorkerType: string(codeworker.WorkerTypeCode),
		},
	})
	if err != nil {
		t.Fatalf("submit worktree A task: %v", err)
	}
	taskA := aimux21WaitWorktreeTask(t, ctx, engine, taskAID)
	if taskA.Status != loom.TaskStatusCompleted {
		t.Fatalf("task A status = %s error=%q result=%q", taskA.Status, taskA.Error, taskA.Result)
	}
	if taskA.ProjectID != projectIDA {
		t.Fatalf("task A ProjectID = %q, want %q", taskA.ProjectID, projectIDA)
	}
	aimux21AssertReadmeMarker(t, filepath.Join(worktreeA, "README.md"))
	aimux21AssertSubtreeProjectID(t, engine, taskAID, projectIDA)

	resumeTaskID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: codeworker.WorkerTypeCode,
		ProjectID:  projectIDB,
		RequestID:  "aimux21-worktree-b-resume",
		Prompt:     "continue",
		CWD:        worktreeB,
		Metadata: map[string]any{
			"resume_id": taskAID,
		},
	})
	if err != nil {
		t.Fatalf("submit worktree B resume task: %v", err)
	}
	resumeTask := aimux21WaitWorktreeTask(t, ctx, engine, resumeTaskID)
	if resumeTask.Status != loom.TaskStatusFailed {
		t.Fatalf("resume task status = %s, want failed; error=%q", resumeTask.Status, resumeTask.Error)
	}
	if !strings.Contains(resumeTask.Error, "ResumeWorkerMismatch") || !strings.Contains(resumeTask.Error, "cross-worktree resume rejected") {
		t.Fatalf("resume task error = %q, want cross-worktree ResumeWorkerMismatch", resumeTask.Error)
	}

	escapeTaskID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: codeworker.WorkerTypeCode,
		ProjectID:  projectIDB,
		RequestID:  "aimux21-worktree-b-escape",
		Prompt:     "add adversarial diff to outside path",
		CWD:        worktreeB,
		Metadata: map[string]any{
			codeworker.MetadataThreadID:   "thread-worktree-b",
			codeworker.MetadataWorkerType: string(codeworker.WorkerTypeCode),
		},
	})
	if err != nil {
		t.Fatalf("submit worktree B escape task: %v", err)
	}
	escapeTask := aimux21WaitWorktreeTask(t, ctx, engine, escapeTaskID)
	if escapeTask.Status != loom.TaskStatusFailed {
		t.Fatalf("escape task status = %s, want failed; error=%q", escapeTask.Status, escapeTask.Error)
	}
	if !strings.Contains(escapeTask.Error, "SandboxDenial") || !strings.Contains(escapeTask.Error, "path escapes worktree root") {
		t.Fatalf("escape task error = %q, want SandboxDenial path escape", escapeTask.Error)
	}
	if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
		t.Fatalf("outside path exists after sandbox denial: %s statErr=%v", outsidePath, err)
	}
}

type aimux21WorktreeDriver struct {
	outsidePath string
}

func (d aimux21WorktreeDriver) Type() loom.WorkerType { return codeworker.WorkerTypeCodeDriver }

func (d aimux21WorktreeDriver) Execute(_ context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	if strings.Contains(strings.ToLower(task.Prompt), "adversarial") {
		return &loom.WorkerResult{Content: aimux21EscapeDiff(d.outsidePath)}, nil
	}
	diff, err := aimux21ReadmeDiff(task.CWD)
	if err != nil {
		return nil, err
	}
	return &loom.WorkerResult{Content: diff}, nil
}

type aimux21WorktreeNavigator struct{}

func (aimux21WorktreeNavigator) Type() loom.WorkerType {
	return codeworker.WorkerTypeCodeNavigator
}

func (aimux21WorktreeNavigator) Execute(_ context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
	data, err := json.Marshal(map[string]any{
		"verdict":    string(codeworker.StateApply),
		"confidence": 0.97,
		"evidence":   "worktree e2e navigator approval",
	})
	if err != nil {
		return nil, err
	}
	return &loom.WorkerResult{Content: string(data)}, nil
}

func aimux21ReadmeDiff(cwd string) (string, error) {
	path := filepath.Join(cwd, "README.md")
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read README.md: %w", err)
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	firstLine := strings.SplitN(text, "\n", 2)[0]
	if firstLine == "" {
		return "", fmt.Errorf("README.md first line is empty")
	}
	return fmt.Sprintf("--- a/README.md\n+++ b/README.md\n@@ -1 +1,2 @@\n %s\n+%s\n", firstLine, aimux21WorktreeReadmeMarker), nil
}

func aimux21EscapeDiff(outsidePath string) string {
	target := filepath.ToSlash(outsidePath)
	return fmt.Sprintf("--- a/escape.txt\n+++ %s\n@@ -0,0 +1 @@\n+owned\n", target)
}

func aimux21WorktreeLoom(t *testing.T) *loom.LoomEngine {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:aimux21_worktree_%d?cache=shared&mode=memory", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("open loom sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	engine, err := loom.NewEngine(db, "aimux21-worktree-e2e")
	if err != nil {
		t.Fatalf("loom.NewEngine: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Logf("loom close: %v", err)
		}
	})
	return engine
}

func aimux21WaitWorktreeTask(t *testing.T, ctx context.Context, engine *loom.LoomEngine, taskID string) *loom.Task {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			task, _ := engine.Get(taskID)
			t.Fatalf("wait for task %s: %v latest=%#v", taskID, ctx.Err(), task)
		case <-ticker.C:
			task, err := engine.Get(taskID)
			if err != nil {
				t.Fatalf("get task %s: %v", taskID, err)
			}
			if task.Status.IsTerminal() {
				return task
			}
		}
	}
}

func aimux21AssertSubtreeProjectID(t *testing.T, engine *loom.LoomEngine, taskID string, wantProjectID string) {
	t.Helper()
	nodes, err := engine.GetTree(taskID, 4)
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	var hasRoot, hasDriver, hasNavigator bool
	for _, node := range nodes {
		if node.ProjectID != wantProjectID {
			t.Fatalf("subtree node %s ProjectID = %q, want %q; nodes=%#v", node.ID, node.ProjectID, wantProjectID, nodes)
		}
		switch node.WorkerType {
		case codeworker.WorkerTypeCode:
			hasRoot = true
		case codeworker.WorkerTypeCodeDriver:
			hasDriver = node.ParentTaskID == taskID
		case codeworker.WorkerTypeCodeNavigator:
			hasNavigator = node.ParentTaskID == taskID
		}
	}
	if !hasRoot || !hasDriver || !hasNavigator {
		t.Fatalf("subtree missing code/driver/navigator nodes: %#v", nodes)
	}
}

func aimux21AssertReadmeMarker(t *testing.T, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(content), aimux21WorktreeReadmeMarker) {
		t.Fatalf("README.md missing worktree marker:\n%s", content)
	}
}

func aimux21AddGitWorktree(t *testing.T, root string, path string) {
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

func aimux21WorktreeProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root containing go.mod")
		}
		dir = parent
	}
}
