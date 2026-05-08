package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
)

// @critical - release blocker per Constitution rule #10.
func TestReviewIntegrationThreePassGateAllow(t *testing.T) {
	engine := newReviewWorkerEngine(t)
	worker, err := NewReviewWorker(ReviewWorkerConfig{Loom: engine})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	engine.RegisterWorker(WorkerTypeReview, worker)
	registerReviewLeaves(t, engine, map[loom.WorkerType]string{
		WorkerTypeReviewStructural:  passJSON("structural ok", nil),
		WorkerTypeReviewBehavioural: passJSON("behavioural ok", nil),
		WorkerTypeReviewAdversarial: passJSON("adversarial ok", nil),
	})

	task := submitReviewIntegrationTask(t, engine, map[string]any{"target": "HEAD", "gate": true})
	decision := decodeReviewDecision(t, task.Result)
	if decision.Decision != DecisionAllow {
		t.Fatalf("decision = %s, want %s", decision.Decision, DecisionAllow)
	}
	assertMetadataPasses(t, task.Metadata["passes_completed"], []string{"structural", "behavioural", "adversarial"})
	assertReviewIntegrationTree(t, engine, task.ID)
}

// @critical - release blocker per Constitution rule #10.
func TestReviewIntegrationThreePassGateBlock(t *testing.T) {
	engine := newReviewWorkerEngine(t)
	worker, err := NewReviewWorker(ReviewWorkerConfig{Loom: engine})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	engine.RegisterWorker(WorkerTypeReview, worker)
	registerReviewLeaves(t, engine, map[loom.WorkerType]string{
		WorkerTypeReviewStructural: passJSON("structural ok", nil),
		WorkerTypeReviewBehavioural: passJSON("behavioural found error", []Finding{
			{Severity: SeverityError, File: "pkg/example.go", Body: "nil branch panics"},
		}),
		WorkerTypeReviewAdversarial: passJSON("adversarial ok", nil),
	})

	task := submitReviewIntegrationTask(t, engine, map[string]any{"target": "HEAD", "gate": true})
	decision := decodeReviewDecision(t, task.Result)
	if decision.Decision != DecisionBlock {
		t.Fatalf("decision = %s, want %s", decision.Decision, DecisionBlock)
	}
	if !strings.Contains(decision.Reason, "nil branch panics") {
		t.Fatalf("reason = %q, want blocking finding", decision.Reason)
	}
	assertMetadataString(t, task.Metadata, "decision", string(DecisionBlock))
	assertReviewIntegrationTree(t, engine, task.ID)
}

// @critical - release blocker per Constitution rule #10.
func TestReviewIntegrationFailOpenTimeout(t *testing.T) {
	runner := &blockingPassRunner{}
	worker, err := NewReviewWorker(ReviewWorkerConfig{PassRunner: runner})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	task := reviewWorkerTask(map[string]any{"target": "HEAD", "gate": true})

	result, err := worker.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	decision := decodeReviewDecision(t, result.Content)
	if decision.Decision != DecisionAllow {
		t.Fatalf("decision = %s, want fail-open %s", decision.Decision, DecisionAllow)
	}
	if decision.Reason != "timeout" {
		t.Fatalf("reason = %q, want timeout", decision.Reason)
	}
}

type blockingPassRunner struct{}

func (blockingPassRunner) Run(ctx context.Context, _ string, _ Criteria) ([]PassResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func registerReviewLeaves(t *testing.T, engine *loom.LoomEngine, outputs map[loom.WorkerType]string) {
	t.Helper()
	for _, workerType := range []loom.WorkerType{WorkerTypeReviewStructural, WorkerTypeReviewBehavioural, WorkerTypeReviewAdversarial} {
		output, ok := outputs[workerType]
		if !ok {
			t.Fatalf("missing output for %s", workerType)
		}
		engine.RegisterWorker(workerType, reviewLeafWorker{workerType: workerType, output: output})
	}
}

func submitReviewIntegrationTask(t *testing.T, engine *loom.LoomEngine, metadata map[string]any) *loom.Task {
	t.Helper()
	taskID, err := engine.Submit(context.Background(), loom.TaskRequest{
		WorkerType: WorkerTypeReview,
		ProjectID:  "project-1",
		RequestID:  "review-integration",
		Prompt:     "review HEAD",
		Metadata:   metadata,
	})
	if err != nil {
		t.Fatalf("Submit review task: %v", err)
	}
	return waitReviewTaskStatus(t, engine, taskID, loom.TaskStatusCompleted)
}

func decodeReviewDecision(t *testing.T, content string) Decision {
	t.Helper()
	var decision Decision
	if err := json.Unmarshal([]byte(content), &decision); err != nil {
		t.Fatalf("decode decision JSON: %v raw=%s", err, content)
	}
	return decision
}

func assertReviewIntegrationTree(t *testing.T, engine *loom.LoomEngine, taskID string) {
	t.Helper()
	nodes, err := engine.GetTree(taskID, 1)
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("tree node count = %d, want root + 3 pass children", len(nodes))
	}
	for i, workerType := range []loom.WorkerType{WorkerTypeReviewStructural, WorkerTypeReviewBehavioural, WorkerTypeReviewAdversarial} {
		if nodes[i+1].ParentTaskID != taskID {
			t.Fatalf("child %d parent = %q, want %q", i, nodes[i+1].ParentTaskID, taskID)
		}
		if nodes[i+1].WorkerType != workerType {
			t.Fatalf("child %d worker_type = %s, want %s", i, nodes[i+1].WorkerType, workerType)
		}
	}
}
