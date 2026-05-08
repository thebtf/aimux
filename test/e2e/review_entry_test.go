package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	reviewworker "github.com/thebtf/aimux/pkg/executor/review"
	_ "modernc.org/sqlite"
)

func TestE2E_ReviewEntry_DecisionShape(t *testing.T) {
	if os.Getenv("AIMUX21_E2E") != "1" {
		t.Skip("AIMUX21_E2E=1 not set - skipping review entry e2e")
	}

	engine := newReviewEntryLoom(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Logf("loom close: %v", err)
		}
	})
	worker, err := reviewworker.NewReviewWorker(reviewworker.ReviewWorkerConfig{Loom: engine})
	if err != nil {
		t.Fatalf("NewReviewWorker: %v", err)
	}
	engine.RegisterWorker(reviewworker.WorkerTypeReview, worker)
	engine.RegisterWorker(reviewworker.WorkerTypeReviewStructural, e2eReviewLeafWorker{
		workerType: reviewworker.WorkerTypeReviewStructural,
		output:     e2eReviewPassJSON(t, "structural ok", nil),
	})
	engine.RegisterWorker(reviewworker.WorkerTypeReviewBehavioural, e2eReviewLeafWorker{
		workerType: reviewworker.WorkerTypeReviewBehavioural,
		output:     e2eReviewPassJSON(t, "behavioural ok", nil),
	})
	engine.RegisterWorker(reviewworker.WorkerTypeReviewAdversarial, e2eReviewLeafWorker{
		workerType: reviewworker.WorkerTypeReviewAdversarial,
		output:     e2eReviewPassJSON(t, "adversarial ok", nil),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	taskID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: reviewworker.WorkerTypeReview,
		ProjectID:  "aimux21-review-e2e",
		RequestID:  "aimux21-review-entry",
		Prompt:     "review HEAD",
		CWD:        reviewEntryProjectRoot(t),
		Metadata: map[string]any{
			"task_class": "review",
			"target":     "HEAD",
			"gate":       true,
		},
	})
	if err != nil {
		t.Fatalf("submit review task: %v", err)
	}

	task := waitForReviewEntryTask(t, ctx, engine, taskID)
	if task.Status != loom.TaskStatusCompleted {
		t.Fatalf("review task status = %s error=%q result=%q", task.Status, task.Error, task.Result)
	}

	var decision map[string]any
	if err := json.Unmarshal([]byte(task.Result), &decision); err != nil {
		t.Fatalf("decode decision result: %v raw=%s", err, task.Result)
	}
	for _, key := range []string{"decision", "reason", "findings", "passes_completed"} {
		if _, ok := decision[key]; !ok {
			t.Fatalf("decision result missing %q: %#v", key, decision)
		}
	}
	if decision["decision"] != "allow" && decision["decision"] != "block" {
		t.Fatalf("decision = %#v, want allow or block", decision["decision"])
	}
}

type e2eReviewLeafWorker struct {
	workerType loom.WorkerType
	output     string
}

func (w e2eReviewLeafWorker) Type() loom.WorkerType { return w.workerType }

func (w e2eReviewLeafWorker) Execute(_ context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
	return &loom.WorkerResult{Content: w.output}, nil
}

func e2eReviewPassJSON(t *testing.T, summary string, findings []reviewworker.Finding) string {
	t.Helper()
	if findings == nil {
		findings = []reviewworker.Finding{}
	}
	data, err := json.Marshal(map[string]any{
		"findings": findings,
		"summary":  summary,
	})
	if err != nil {
		t.Fatalf("marshal review pass JSON: %v", err)
	}
	return string(data)
}

func newReviewEntryLoom(t *testing.T) *loom.LoomEngine {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:review_entry_%d?cache=shared&mode=memory", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("open loom sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	engine, err := loom.NewEngine(db, "aimux21-review-entry-e2e")
	if err != nil {
		t.Fatalf("loom.NewEngine: %v", err)
	}
	return engine
}

func reviewEntryProjectRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			return cwd
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			t.Fatalf("could not find go.mod above %s", cwd)
		}
		cwd = parent
	}
}

func waitForReviewEntryTask(t *testing.T, ctx context.Context, engine *loom.LoomEngine, taskID string) *loom.Task {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			task, _ := engine.Get(taskID)
			t.Fatalf("wait for review task %s: %v latest=%#v", taskID, ctx.Err(), task)
		case <-ticker.C:
			task, err := engine.Get(taskID)
			if err != nil {
				t.Fatalf("get review task %s: %v", taskID, err)
			}
			if task.Status.IsTerminal() {
				return task
			}
		}
	}
}
