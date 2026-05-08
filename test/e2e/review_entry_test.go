package e2e

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	reviewworker "github.com/thebtf/aimux/pkg/executor/review"
)

func TestE2E_ReviewEntry_DecisionShape(t *testing.T) {
	if os.Getenv("AIMUX21_E2E") != "1" {
		t.Skip("AIMUX21_E2E=1 not set - skipping review entry e2e")
	}

	engine := newCodeEntryLoom(t)
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
		CWD:        projectRoot(),
		Metadata: map[string]any{
			"task_class": "review",
			"target":     "HEAD",
			"gate":       true,
		},
	})
	if err != nil {
		t.Fatalf("submit review task: %v", err)
	}

	task := waitForE2ETask(t, ctx, engine, taskID)
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
