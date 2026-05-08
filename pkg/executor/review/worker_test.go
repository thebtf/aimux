package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/loom/deps"
	_ "modernc.org/sqlite"
)

func TestReviewWorkerExecuteRecordsAggregateMetadata(t *testing.T) {
	runner := &recordingPassRunner{results: []PassResult{
		{Name: PassStructural, Summary: "structure clean"},
		{Name: PassBehavioural, Summary: "warning", Findings: []Finding{
			{Severity: SeverityWarning, Body: "missing edge case assertion"},
		}},
		{Name: PassAdversarial, Summary: "secure"},
	}}
	worker, err := NewReviewWorker(ReviewWorkerConfig{PassRunner: runner})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	task := reviewWorkerTask(map[string]any{"target": "HEAD~1..HEAD"})

	result, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("pass runner calls = %d, want 1", runner.calls)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}
	var aggregate AggregatedFindings
	if err := json.Unmarshal([]byte(result.Content), &aggregate); err != nil {
		t.Fatalf("decode aggregate: %v", err)
	}
	if aggregate.Severity != SeverityWarning {
		t.Fatalf("aggregate severity = %s, want %s", aggregate.Severity, SeverityWarning)
	}
	assertMetadataString(t, task.Metadata, "worker_type", string(WorkerTypeReview))
	assertMetadataString(t, task.Metadata, "review_sub_mode", "aggregate")
	assertMetadataPasses(t, task.Metadata["passes_completed"], []string{"structural", "behavioural", "adversarial"})
	if result.Metadata["passes_completed"] == nil {
		t.Fatal("result metadata missing passes_completed")
	}
}

func TestReviewWorkerExecuteGateModeRecordsDecision(t *testing.T) {
	runner := &recordingPassRunner{results: []PassResult{
		{Name: PassStructural, Summary: "error", Findings: []Finding{
			{Severity: SeverityError, File: "pkg/a.go", Body: "nil branch panics"},
		}},
		{Name: PassBehavioural, Summary: "ok"},
		{Name: PassAdversarial, Summary: "ok"},
	}}
	worker, err := NewReviewWorker(ReviewWorkerConfig{PassRunner: runner})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	task := reviewWorkerTask(map[string]any{"target": "HEAD", "gate": true})

	result, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var decision Decision
	if err := json.Unmarshal([]byte(result.Content), &decision); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if decision.Decision != DecisionBlock {
		t.Fatalf("decision = %s, want %s", decision.Decision, DecisionBlock)
	}
	assertMetadataString(t, task.Metadata, "review_sub_mode", "gate")
	assertMetadataString(t, task.Metadata, "decision", string(DecisionBlock))
	if !strings.Contains(fmt.Sprint(task.Metadata["reason"]), "nil branch panics") {
		t.Fatalf("reason metadata = %#v, want blocking finding", task.Metadata["reason"])
	}
	assertMetadataPasses(t, task.Metadata["passes_completed"], []string{"structural", "behavioural", "adversarial"})
}

func TestReviewWorkerRejectsCrossWorktreeResume(t *testing.T) {
	engine := newReviewWorkerEngine(t)
	priorID, err := engine.Submit(context.Background(), loom.TaskRequest{
		WorkerType: WorkerTypeReview,
		ProjectID:  "project-b",
		TenantID:   "tenant-a",
		Prompt:     "review HEAD",
		Metadata: map[string]any{
			"target":      "HEAD",
			"worker_type": string(WorkerTypeReview),
		},
	})
	if err != nil {
		t.Fatalf("Submit prior review task: %v", err)
	}
	worker, err := NewReviewWorker(ReviewWorkerConfig{Loom: engine, PassRunner: &recordingPassRunner{}})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	task := reviewWorkerTask(map[string]any{"target": "HEAD", "resume_id": priorID})
	task.ProjectID = "project-a"
	task.TenantID = "tenant-a"

	_, err = worker.Execute(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "different worktree") {
		t.Fatalf("Execute error = %v, want cross-worktree resume rejection", err)
	}
}

func TestReviewWorkerSubtaskTreeShape(t *testing.T) {
	engine := newReviewWorkerEngine(t)
	worker, err := NewReviewWorker(ReviewWorkerConfig{Loom: engine})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	engine.RegisterWorker(WorkerTypeReview, worker)
	engine.RegisterWorker(WorkerTypeReviewStructural, reviewLeafWorker{workerType: WorkerTypeReviewStructural, output: passJSON("structural ok", nil)})
	engine.RegisterWorker(WorkerTypeReviewBehavioural, reviewLeafWorker{workerType: WorkerTypeReviewBehavioural, output: passJSON("behavioural ok", nil)})
	engine.RegisterWorker(WorkerTypeReviewAdversarial, reviewLeafWorker{workerType: WorkerTypeReviewAdversarial, output: passJSON("adversarial ok", nil)})

	rootID, err := engine.Submit(context.Background(), loom.TaskRequest{
		WorkerType: WorkerTypeReview,
		ProjectID:  "project-1",
		RequestID:  "request-1",
		Prompt:     "review HEAD",
		Metadata: map[string]any{
			"target": "HEAD",
			"gate":   true,
		},
	})
	if err != nil {
		t.Fatalf("Submit review root: %v", err)
	}

	root := waitReviewTaskStatus(t, engine, rootID, loom.TaskStatusCompleted)
	assertMetadataPasses(t, root.Metadata["passes_completed"], []string{"structural", "behavioural", "adversarial"})
	assertMetadataString(t, root.Metadata, "decision", string(DecisionAllow))

	nodes, err := engine.GetTree(rootID, 1)
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("tree node count = %d, want root + 3 children: %#v", len(nodes), nodes)
	}
	if len(nodes[0].SubtaskIDs) != 3 {
		t.Fatalf("root SubtaskIDs = %#v, want 3 children", nodes[0].SubtaskIDs)
	}
	wantTypes := []loom.WorkerType{WorkerTypeReviewStructural, WorkerTypeReviewBehavioural, WorkerTypeReviewAdversarial}
	for i, wantType := range wantTypes {
		child := nodes[i+1]
		if child.ParentTaskID != rootID {
			t.Fatalf("child %d parent = %q, want %q", i, child.ParentTaskID, rootID)
		}
		if child.WorkerType != wantType {
			t.Fatalf("child %d worker_type = %s, want %s", i, child.WorkerType, wantType)
		}
	}
}

type recordingPassRunner struct {
	results []PassResult
	calls   int
}

func (r *recordingPassRunner) Run(_ context.Context, target string, criteria Criteria) ([]PassResult, error) {
	r.calls++
	if target == "" {
		return nil, fmt.Errorf("target is empty")
	}
	if criteria.ParentTaskID == "" {
		return nil, fmt.Errorf("parent task ID is empty")
	}
	return r.results, nil
}

type reviewLeafWorker struct {
	workerType loom.WorkerType
	output     string
}

func (w reviewLeafWorker) Type() loom.WorkerType { return w.workerType }

func (w reviewLeafWorker) Execute(_ context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
	return &loom.WorkerResult{Content: w.output}, nil
}

func reviewWorkerTask(metadata map[string]any) *loom.Task {
	return &loom.Task{
		ID:         "review-root",
		Status:     loom.TaskStatusRunning,
		WorkerType: WorkerTypeReview,
		ProjectID:  "project-1",
		RequestID:  "request-1",
		Prompt:     "review HEAD",
		CWD:        "/workspace",
		Metadata:   metadata,
	}
}

func newReviewWorkerEngine(t *testing.T) *loom.LoomEngine {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:review_worker_%d?cache=shared&mode=memory", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	engine, err := loom.NewEngine(db, "review-worker-test", loom.WithIDGenerator(deps.NewSequentialIDGenerator()))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = engine.Close(ctx)
	})
	return engine
}

func waitReviewTaskStatus(t *testing.T, engine *loom.LoomEngine, taskID string, want loom.TaskStatus) *loom.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, err := engine.Get(taskID)
		if err == nil && task.Status == want {
			return task
		}
		if err == nil && task.Status.IsTerminal() {
			t.Fatalf("task %s reached terminal status %s, want %s; error=%s", taskID, task.Status, want, task.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, err := engine.Get(taskID)
	if err != nil {
		t.Fatalf("Get %s after timeout: %v", taskID, err)
	}
	t.Fatalf("task %s status = %s, want %s; error=%s", taskID, task.Status, want, task.Error)
	return nil
}

func assertMetadataString(t *testing.T, metadata map[string]any, key string, want string) {
	t.Helper()
	if fmt.Sprint(metadata[key]) != want {
		t.Fatalf("metadata[%s] = %#v, want %q", key, metadata[key], want)
	}
}

func assertMetadataPasses(t *testing.T, raw any, want []string) {
	t.Helper()
	got := make([]string, 0, len(want))
	switch values := raw.(type) {
	case []string:
		got = append(got, values...)
	case []PassName:
		for _, value := range values {
			got = append(got, string(value))
		}
	case []any:
		for _, value := range values {
			got = append(got, fmt.Sprint(value))
		}
	default:
		t.Fatalf("passes metadata type = %T, want slice", raw)
	}
	if len(got) != len(want) {
		t.Fatalf("passes metadata = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("passes metadata = %#v, want %#v", got, want)
		}
	}
}
