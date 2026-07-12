package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/loom/deps"
	_ "modernc.org/sqlite"
)

const (
	testReviewPrivateReasoning = "PRIVATE_REASONING_SENTINEL"
	testReviewHiddenThought    = "hidden-thought-trace"
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
	if runner.criteria[0].Env["SESSION_KEY"] != "session-value" {
		t.Fatalf("criteria Env[SESSION_KEY] = %q, want session-value", runner.criteria[0].Env["SESSION_KEY"])
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

func TestReviewWorkerAggregateMetadataUsesSafeBoundedReason(t *testing.T) {
	runner := &recordingPassRunner{results: []PassResult{
		{Name: PassStructural, Summary: "structure clean " + testReviewSecret + " " + strings.Repeat("界", 300)},
		{Name: PassBehavioural, Summary: "behaviour clean"},
		{Name: PassAdversarial, Summary: "adversarial clean"},
	}}
	worker, err := NewReviewWorker(ReviewWorkerConfig{PassRunner: runner})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	task := reviewWorkerTask(map[string]any{"target": "HEAD"})

	result, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	reason, ok := result.Metadata["reason"].(string)
	if !ok {
		t.Fatalf("reason metadata = %#v, want string", result.Metadata["reason"])
	}
	assertSafeBoundedReviewReason(t, reason)
	if taskReason, _ := task.Metadata["reason"].(string); taskReason != reason {
		t.Fatalf("task reason = %q, worker result reason = %q", taskReason, reason)
	}
}

func TestReviewWorkerDurableResultsRedactAllReviewText(t *testing.T) {
	line := 17
	tainted := func(s, private string) string {
		return s + " " + testReviewSecret + " " + private
	}
	tests := []struct {
		name     string
		metadata map[string]any
		results  []PassResult
		err      error
		assert   func(*testing.T, *loom.Task)
	}{
		{
			name:     "final aggregate",
			metadata: map[string]any{"target": "HEAD"},
			results: []PassResult{
				{Name: PassStructural, Summary: tainted("structural private summary", testReviewPrivateReasoning), Findings: []Finding{
					{Severity: SeverityError, File: "pkg/safe.go", Line: &line, Body: "useful public reasoning " + testReviewSecret},
					{Severity: Severity(tainted("warning", testReviewHiddenThought)), File: tainted("pkg/typed.go", testReviewPrivateReasoning), Line: &line, Body: tainted("typed-field private finding", testReviewHiddenThought)},
				}},
				{Name: PassBehavioural, Summary: tainted("behavioural private summary", testReviewHiddenThought)},
				{Name: PassAdversarial, Summary: "useful adversarial summary " + testReviewSecret},
			},
			assert: func(t *testing.T, task *loom.Task) {
				var aggregate AggregatedFindings
				if err := json.Unmarshal([]byte(task.Result), &aggregate); err != nil {
					t.Fatalf("decode durable aggregate: %v", err)
				}
				if !aggregate.Blocking || aggregate.Findings[0].Line == nil || *aggregate.Findings[0].Line != line {
					t.Fatalf("aggregate = %#v, want blocking verdict and preserved line", aggregate)
				}
			},
		},
		{
			name:     "partial fail-closed gate",
			metadata: map[string]any{"target": "HEAD", "gate": true},
			results: []PassResult{{Name: PassStructural, Summary: tainted("partial private summary", testReviewHiddenThought), Findings: []Finding{
				{
					Severity: Severity(tainted("warning", testReviewPrivateReasoning)),
					File:     tainted("pkg/partial.go", testReviewHiddenThought),
					Line:     &line,
					Body:     tainted("partial private finding", testReviewPrivateReasoning),
				},
				{Severity: SeverityInfo, File: "pkg/credential.go", Line: &line, Body: "useful credential evidence " + testReviewSecret},
			}}},
			err: errors.New(tainted("behavioural backend unavailable", testReviewPrivateReasoning)),
			assert: func(t *testing.T, task *loom.Task) {
				decision := decodeReviewDecision(t, task.Result)
				if decision.Decision != DecisionBlock || !decision.Blocking || decision.ReviewComplete || decision.ConfidenceScore != 0 {
					t.Fatalf("decision = %#v, want incomplete fail-closed block", decision)
				}
				if len(decision.PassesCompleted) != 1 || decision.PassesCompleted[0] != PassStructural {
					t.Fatalf("passes_completed = %#v, want preserved structural partial", decision.PassesCompleted)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := newReviewWorkerEngine(t)
			worker, err := NewReviewWorker(ReviewWorkerConfig{PassRunner: &recordingPassRunner{results: tt.results, err: tt.err}})
			if err != nil {
				t.Fatalf("NewReviewWorker returned error: %v", err)
			}
			engine.RegisterWorker(WorkerTypeReview, worker)
			taskID, err := engine.Submit(context.Background(), loom.TaskRequest{
				WorkerType: WorkerTypeReview,
				ProjectID:  "project-1",
				RequestID:  "request-1",
				Prompt:     "review HEAD",
				Metadata:   tt.metadata,
			})
			if err != nil {
				t.Fatalf("Submit review root: %v", err)
			}
			task := waitReviewTaskStatus(t, engine, taskID, loom.TaskStatusCompleted)
			assertSafeDurableReviewContent(t, task.Result)
			if reason := fmt.Sprint(task.Metadata["reason"]); strings.Contains(reason, testReviewSecret) ||
				strings.Contains(reason, testReviewPrivateReasoning) || strings.Contains(reason, testReviewHiddenThought) {
				t.Errorf("durable reason metadata leaked sensitive review text: %q", reason)
			}
			tt.assert(t, task)
		})
	}
}

func TestReviewWorkerPassesWorkflowRecipeMetadataToRunner(t *testing.T) {
	runner := &recordingPassRunner{results: []PassResult{{Name: PassStructural, Summary: "structure clean"}}}
	worker, err := NewReviewWorker(ReviewWorkerConfig{PassRunner: runner})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	task := reviewWorkerTask(map[string]any{
		"target":                 "HEAD",
		"recipe_id":              "debug-investigation",
		"recipe_workflow_id":     "debug",
		"recipe_workflow_source": "pkg/workflow/debug.go",
		"recipe_workflow_steps":  []any{"symptom_capture", "hypothesis_gen", "fix_plan"},
	})
	task.Prompt = "Workflow-backed curated recipe: debug-investigation\nCompiled workflow: debug"

	_, err = worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("pass runner calls = %d, want 1", runner.calls)
	}
	criteria := runner.criteria[0]
	if criteria.RecipeID != "debug-investigation" {
		t.Fatalf("RecipeID = %q, want debug-investigation", criteria.RecipeID)
	}
	if criteria.RecipeWorkflowID != "debug" {
		t.Fatalf("RecipeWorkflowID = %q, want debug", criteria.RecipeWorkflowID)
	}
	if criteria.RecipeWorkflowSource != "pkg/workflow/debug.go" {
		t.Fatalf("RecipeWorkflowSource = %q, want pkg/workflow/debug.go", criteria.RecipeWorkflowSource)
	}
	if got, want := strings.Join(criteria.RecipeWorkflowSteps, ","), "symptom_capture,hypothesis_gen,fix_plan"; got != want {
		t.Fatalf("RecipeWorkflowSteps = %q, want %q", got, want)
	}
	if !strings.Contains(criteria.RecipeWorkflowPrompt, "Workflow-backed curated recipe: debug-investigation") {
		t.Fatalf("RecipeWorkflowPrompt = %q, want parent workflow prompt", criteria.RecipeWorkflowPrompt)
	}
}

func TestReviewWorkerPassesFallbackPolicyToRunner(t *testing.T) {
	runner := &recordingPassRunner{results: []PassResult{
		{Name: PassStructural, Summary: "structure clean"},
		{Name: PassBehavioural, Summary: "behaviour clean"},
		{Name: PassAdversarial, Summary: "adversarial clean"},
	}}
	worker, err := NewReviewWorker(ReviewWorkerConfig{PassRunner: runner})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	task := reviewWorkerTask(map[string]any{
		"target":           "HEAD",
		"gate":             true,
		"fallback_enabled": false,
		"max_attempts":     2,
	})

	if _, err := worker.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(runner.criteria) != 1 {
		t.Fatalf("criteria count = %d, want 1", len(runner.criteria))
	}
	criteria := runner.criteria[0]
	if criteria.FallbackEnabled == nil || *criteria.FallbackEnabled {
		t.Fatalf("FallbackEnabled = %#v, want false", criteria.FallbackEnabled)
	}
	if criteria.MaxAttempts != 2 {
		t.Fatalf("MaxAttempts = %d, want 2", criteria.MaxAttempts)
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

func TestReviewWorkerGateFailureIsBlockingAndLowersVerdictConfidence(t *testing.T) {
	runner := &recordingPassRunner{
		results: []PassResult{{Name: PassStructural, Summary: "structure clean"}},
		err:     errors.New("behavioural review pass unavailable after retries"),
	}
	worker, err := NewReviewWorker(ReviewWorkerConfig{PassRunner: runner})
	if err != nil {
		t.Fatalf("NewReviewWorker returned error: %v", err)
	}
	task := reviewWorkerTask(map[string]any{
		"target":                    "HEAD",
		"gate":                      true,
		"classification_confidence": 1.0,
	})

	result, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	decision := decodeReviewDecision(t, result.Content)
	if decision.Decision != DecisionBlock || !decision.Blocking {
		t.Fatalf("decision = %#v, want fail-closed block", decision)
	}
	if decision.ReviewComplete {
		t.Fatal("ReviewComplete = true, want false for incomplete gate")
	}
	assertMetadataPasses(t, task.Metadata["passes_completed"], []string{"structural"})
	if complete, ok := task.Metadata["review_complete"].(bool); !ok || complete {
		t.Fatalf("review_complete = %#v, want false", task.Metadata["review_complete"])
	}
	if confidence, ok := task.Metadata["confidence_score"].(float64); !ok || confidence != 0 {
		t.Fatalf("confidence_score = %#v, want 0 for incomplete gate", task.Metadata["confidence_score"])
	}
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
	childTypeCounts := map[loom.WorkerType]int{}
	for i, child := range nodes[1:] {
		if child.ParentTaskID != rootID {
			t.Fatalf("child %d parent = %q, want %q", i, child.ParentTaskID, rootID)
		}
		childTypeCounts[child.WorkerType]++
	}
	for _, wantType := range wantTypes {
		if childTypeCounts[wantType] != 1 {
			t.Fatalf("child worker_type %s count = %d, want 1; nodes=%#v", wantType, childTypeCounts[wantType], nodes)
		}
	}
}

type recordingPassRunner struct {
	results  []PassResult
	err      error
	calls    int
	criteria []Criteria
}

func (r *recordingPassRunner) Run(_ context.Context, target string, criteria Criteria) ([]PassResult, error) {
	r.calls++
	r.criteria = append(r.criteria, criteria)
	if target == "" {
		return nil, fmt.Errorf("target is empty")
	}
	if criteria.ParentTaskID == "" {
		return nil, fmt.Errorf("parent task ID is empty")
	}
	return r.results, r.err
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
		Env:        map[string]string{"SESSION_KEY": "session-value"},
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

func assertSafeDurableReviewContent(t *testing.T, content string) {
	t.Helper()
	for _, raw := range []string{testReviewSecret, testReviewPrivateReasoning, testReviewHiddenThought} {
		if strings.Contains(content, raw) {
			t.Errorf("durable review content leaked %q: %s", raw, content)
		}
	}
	for _, marker := range []string{"[REDACTED:openai-key-project]", "[REDACTED:private-reasoning]"} {
		if !strings.Contains(content, marker) {
			t.Errorf("durable review content = %s, want %q", content, marker)
		}
	}
	for _, useful := range []string{"useful", "pkg/"} {
		if !strings.Contains(content, useful) {
			t.Errorf("durable review content lost non-sensitive %q text: %s", useful, content)
		}
	}
	if !json.Valid([]byte(content)) {
		t.Fatalf("durable review content is not valid JSON: %s", content)
	}
}
