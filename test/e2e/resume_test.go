package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	codeworker "github.com/thebtf/aimux/pkg/executor/code"
	applygate "github.com/thebtf/aimux/pkg/executor/code/gate"
	reviewworker "github.com/thebtf/aimux/pkg/executor/review"
	_ "modernc.org/sqlite"
)

func TestE2E_Resume_CodeContinuationAndReviewMismatch(t *testing.T) {
	if os.Getenv("AIMUX21_E2E") != "1" {
		t.Skip("AIMUX21_E2E=1 not set - skipping resume e2e")
	}

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

	reviewEntry, err := reviewworker.NewReviewWorker(reviewworker.ReviewWorkerConfig{
		Loom:       engine,
		PassRunner: resumeNoopReviewRunner{},
	})
	if err != nil {
		t.Fatalf("NewReviewWorker: %v", err)
	}
	engine.RegisterWorker(reviewworker.WorkerTypeReview, reviewEntry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	taskAID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: codeworker.WorkerTypeCode,
		ProjectID:  "aimux21-resume-e2e",
		RequestID:  "aimux21-resume-code-a",
		Prompt:     "first code task",
		Metadata: map[string]any{
			codeworker.MetadataThreadID:   "thread-A",
			codeworker.MetadataWorkerType: string(codeworker.WorkerTypeCode),
		},
	})
	if err != nil {
		t.Fatalf("submit code task A: %v", err)
	}
	taskA := waitForResumeTask(t, ctx, engine, taskAID)
	if taskA.Status != loom.TaskStatusCompleted {
		t.Fatalf("task A status = %s error=%q result=%q", taskA.Status, taskA.Error, taskA.Result)
	}
	assertResumeMetadataString(t, taskA.Metadata, codeworker.MetadataThreadID, "thread-A")

	taskBID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: codeworker.WorkerTypeCode,
		ProjectID:  "aimux21-resume-e2e",
		RequestID:  "aimux21-resume-code-b",
		Prompt:     "continue code task",
		Metadata: map[string]any{
			"resume_id": taskAID,
		},
	})
	if err != nil {
		t.Fatalf("submit resumed code task B: %v", err)
	}
	taskB := waitForResumeTask(t, ctx, engine, taskBID)
	if taskB.Status != loom.TaskStatusCompleted {
		t.Fatalf("task B status = %s error=%q result=%q", taskB.Status, taskB.Error, taskB.Result)
	}

	lastDriverMeta := driver.lastMetadata()
	assertResumeMetadataString(t, lastDriverMeta, codeworker.MetadataThreadID, "thread-A")
	assertResumeMetadataString(t, lastDriverMeta, codeworker.MetadataResumeTaskID, taskAID)

	reviewTaskID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: reviewworker.WorkerTypeReview,
		ProjectID:  "aimux21-resume-e2e",
		RequestID:  "aimux21-resume-review-mismatch",
		Prompt:     "HEAD",
		Metadata: map[string]any{
			"resume_id": taskAID,
			"target":    "HEAD",
		},
	})
	if err != nil {
		t.Fatalf("submit review resume mismatch task: %v", err)
	}
	reviewTask := waitForResumeTask(t, ctx, engine, reviewTaskID)
	if reviewTask.Status != loom.TaskStatusFailed {
		t.Fatalf("review task status = %s error=%q result=%q", reviewTask.Status, reviewTask.Error, reviewTask.Result)
	}
	if !strings.Contains(reviewTask.Error, "ResumeWorkerMismatch") {
		t.Fatalf("review task error = %q, want ResumeWorkerMismatch", reviewTask.Error)
	}
}

type resumeProbeDriver struct {
	mu       sync.Mutex
	metadata []map[string]any
}

func (d *resumeProbeDriver) Type() loom.WorkerType { return codeworker.WorkerTypeCodeDriver }

func (d *resumeProbeDriver) Execute(_ context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	d.mu.Lock()
	d.metadata = append(d.metadata, cloneResumeMetadata(task.Metadata))
	d.mu.Unlock()
	return &loom.WorkerResult{Content: "--- a/note.txt\n+++ b/note.txt\n@@ -1 +1 @@\n-old\n+new\n"}, nil
}

func (d *resumeProbeDriver) lastMetadata() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.metadata) == 0 {
		return nil
	}
	return cloneResumeMetadata(d.metadata[len(d.metadata)-1])
}

type resumeStaticNavigator struct{}

func (resumeStaticNavigator) Type() loom.WorkerType { return codeworker.WorkerTypeCodeNavigator }

func (resumeStaticNavigator) Execute(_ context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
	data, err := json.Marshal(map[string]any{
		"verdict":    string(codeworker.StateApply),
		"confidence": 0.99,
		"evidence":   "resume e2e navigator approval",
	})
	if err != nil {
		return nil, err
	}
	return &loom.WorkerResult{Content: string(data)}, nil
}

type resumeNoopReviewRunner struct{}

func (resumeNoopReviewRunner) Run(context.Context, string, reviewworker.Criteria) ([]reviewworker.PassResult, error) {
	return []reviewworker.PassResult{}, nil
}

func newResumeE2ELoom(t *testing.T) *loom.LoomEngine {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:resume_e2e_%d?cache=shared&mode=memory", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("open loom sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	engine, err := loom.NewEngine(db, "aimux21-resume-e2e")
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

func waitForResumeTask(t *testing.T, ctx context.Context, engine *loom.LoomEngine, taskID string) *loom.Task {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			task, _ := engine.Get(taskID)
			t.Fatalf("wait for resume task %s: %v latest=%#v", taskID, ctx.Err(), task)
		case <-ticker.C:
			task, err := engine.Get(taskID)
			if err != nil {
				t.Fatalf("get resume task %s: %v", taskID, err)
			}
			if task.Status.IsTerminal() {
				return task
			}
		}
	}
}

func assertResumeMetadataString(t *testing.T, metadata map[string]any, key string, want string) {
	t.Helper()
	got, ok := metadata[key].(string)
	if !ok || got != want {
		t.Fatalf("%s metadata = %#v, want %q (metadata=%#v)", key, metadata[key], want, metadata)
	}
}

func cloneResumeMetadata(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
