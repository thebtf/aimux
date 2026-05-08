package review

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
)

func TestPassesRunDispatchesAllPassesWithMetadata(t *testing.T) {
	client := newMockLoom(map[loom.WorkerType]string{
		WorkerTypeReviewStructural:  passJSON("structural ok", nil),
		WorkerTypeReviewBehavioural: passJSON("behavioural ok", nil),
		WorkerTypeReviewAdversarial: passJSON("adversarial ok", nil),
	})
	passes, err := NewPasses(client)
	if err != nil {
		t.Fatalf("NewPasses returned error: %v", err)
	}

	results, err := passes.Run(context.Background(), "HEAD~1..HEAD", testCriteria())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("result count = %d, want 3", len(results))
	}
	assertPassOrder(t, results, []PassName{PassStructural, PassBehavioural, PassAdversarial})
	if len(client.submissions) != 3 {
		t.Fatalf("submission count = %d, want 3", len(client.submissions))
	}
	for _, req := range client.submissions {
		if req.ParentTaskID != "review-root" {
			t.Fatalf("ParentTaskID = %q, want review-root", req.ParentTaskID)
		}
		if req.ProjectID != "project-1" {
			t.Fatalf("ProjectID = %q, want project-1", req.ProjectID)
		}
		if req.Model != "review-model" {
			t.Fatalf("Model = %q, want review-model", req.Model)
		}
		if req.Effort != "high" {
			t.Fatalf("Effort = %q, want high", req.Effort)
		}
		if req.Env["SESSION_KEY"] != "session-value" {
			t.Fatalf("Env[SESSION_KEY] = %q, want session-value", req.Env["SESSION_KEY"])
		}
		if req.Metadata["parent_task_id"] != "review-root" {
			t.Fatalf("metadata parent_task_id = %#v", req.Metadata["parent_task_id"])
		}
		if req.Metadata["sandbox"] != "read-only" {
			t.Fatalf("metadata sandbox = %#v, want read-only", req.Metadata["sandbox"])
		}
		if !strings.Contains(req.Prompt, "HEAD~1..HEAD") {
			t.Fatalf("prompt missing target: %q", req.Prompt)
		}
	}
	assertSubmission(t, client.submissions[0], WorkerTypeReviewStructural, PassStructural)
	assertSubmission(t, client.submissions[1], WorkerTypeReviewBehavioural, PassBehavioural)
	assertSubmission(t, client.submissions[2], WorkerTypeReviewAdversarial, PassAdversarial)
}

func TestPassesRunContinuesAfterErrorFindings(t *testing.T) {
	client := newMockLoom(map[loom.WorkerType]string{
		WorkerTypeReviewStructural: passJSON("structural ok", nil),
		WorkerTypeReviewBehavioural: passJSON("behavioural found blocking issue", []Finding{
			{Severity: SeverityError, File: "pkg/example.go", Line: intPtr(12), Body: "nil branch panics"},
		}),
		WorkerTypeReviewAdversarial: passJSON("adversarial ok", nil),
	})
	passes, err := NewPasses(client)
	if err != nil {
		t.Fatalf("NewPasses returned error: %v", err)
	}

	results, err := passes.Run(context.Background(), "HEAD", testCriteria())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("result count = %d, want 3", len(results))
	}
	if len(results[1].Findings) != 1 {
		t.Fatalf("behavioural findings = %#v, want one blocking finding", results[1].Findings)
	}
	if results[1].Findings[0].Severity != SeverityError {
		t.Fatalf("severity = %s, want %s", results[1].Findings[0].Severity, SeverityError)
	}
	if len(client.submissions) != 3 {
		t.Fatalf("submission count = %d, want all passes to run after a finding", len(client.submissions))
	}
}

func TestPassesRunIncludesAdversarialFindings(t *testing.T) {
	client := newMockLoom(map[loom.WorkerType]string{
		WorkerTypeReviewStructural:  passJSON("structural ok", nil),
		WorkerTypeReviewBehavioural: passJSON("behavioural ok", nil),
		WorkerTypeReviewAdversarial: passJSON("adversarial found injection risk", []Finding{
			{Severity: SeverityError, File: "pkg/server/tool.go", Body: "unescaped tool output crosses trust boundary"},
		}),
	})
	passes, err := NewPasses(client)
	if err != nil {
		t.Fatalf("NewPasses returned error: %v", err)
	}

	results, err := passes.Run(context.Background(), "HEAD", testCriteria())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("result count = %d, want 3", len(results))
	}
	adversarial := results[2]
	if adversarial.Name != PassAdversarial {
		t.Fatalf("third pass = %s, want %s", adversarial.Name, PassAdversarial)
	}
	if len(adversarial.Findings) != 1 {
		t.Fatalf("adversarial findings = %#v, want one finding", adversarial.Findings)
	}
	if adversarial.Findings[0].File != "pkg/server/tool.go" {
		t.Fatalf("adversarial finding file = %q", adversarial.Findings[0].File)
	}
}

func TestPassesRunStopsAfterFailedPassTask(t *testing.T) {
	client := newMockLoom(map[loom.WorkerType]string{
		WorkerTypeReviewStructural:  passJSON("structural ok", nil),
		WorkerTypeReviewBehavioural: "",
	})
	client.statuses[WorkerTypeReviewBehavioural] = loom.TaskStatusFailed
	client.errors[WorkerTypeReviewBehavioural] = "behavioural worker failed"
	passes, err := NewPasses(client)
	if err != nil {
		t.Fatalf("NewPasses returned error: %v", err)
	}

	results, err := passes.Run(context.Background(), "HEAD", testCriteria())
	if err == nil {
		t.Fatal("Run returned nil error, want failed pass error")
	}
	if !strings.Contains(err.Error(), "behavioural worker failed") {
		t.Fatalf("error = %v, want behavioural worker failure", err)
	}
	if len(results) != 1 {
		t.Fatalf("partial results = %d, want only structural result", len(results))
	}
	if len(client.submissions) != 2 {
		t.Fatalf("submission count = %d, want structural + failed behavioural only", len(client.submissions))
	}
}

func TestPassesRunInvalidJSONFails(t *testing.T) {
	client := newMockLoom(map[loom.WorkerType]string{
		WorkerTypeReviewStructural: "not-json",
	})
	passes, err := NewPasses(client)
	if err != nil {
		t.Fatalf("NewPasses returned error: %v", err)
	}

	_, err = passes.Run(context.Background(), "HEAD", testCriteria())
	if err == nil {
		t.Fatal("Run returned nil error, want invalid JSON error")
	}
}

func TestPassesRunCancelsTimedOutPassTask(t *testing.T) {
	client := newMockLoom(map[loom.WorkerType]string{
		WorkerTypeReviewStructural: passJSON("structural ok", nil),
	})
	client.statuses[WorkerTypeReviewStructural] = loom.TaskStatusRunning
	passes, err := NewPasses(client)
	if err != nil {
		t.Fatalf("NewPasses returned error: %v", err)
	}

	_, err = passes.Run(context.Background(), "HEAD", Criteria{
		ParentTaskID: "review-root",
		ProjectID:    "project-1",
		TaskTimeout:  200 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run returned nil error, want timeout")
	}
	if len(client.canceled) != 1 {
		t.Fatalf("canceled tasks = %#v, want one timed-out pass task", client.canceled)
	}
	if client.canceled[0] != "review-task-1" {
		t.Fatalf("canceled task = %q, want review-task-1", client.canceled[0])
	}
}

type reviewPassContextKey struct{}

func TestWaitForTaskUsesContextScopedLookup(t *testing.T) {
	client := newMockLoom(nil)
	client.tasks["review-task-1"] = &loom.Task{
		ID:     "review-task-1",
		Status: loom.TaskStatusCompleted,
		Result: passJSON("structural ok", nil),
	}
	ctx := context.WithValue(context.Background(), reviewPassContextKey{}, "tenant-scope")

	task, err := waitForTask(ctx, client, "review-task-1", Criteria{
		TaskTimeout:  time.Second,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("waitForTask returned error: %v", err)
	}
	if task.ID != "review-task-1" {
		t.Fatalf("task ID = %q, want review-task-1", task.ID)
	}
	if client.getCalls != 0 {
		t.Fatalf("Get calls = %d, want 0 when GetContext is supported", client.getCalls)
	}
	if client.getContextCalls != 1 {
		t.Fatalf("GetContext calls = %d, want 1", client.getContextCalls)
	}
	if client.getContextScope != "tenant-scope" {
		t.Fatalf("GetContext scope = %q, want tenant-scope", client.getContextScope)
	}
}

type mockLoom struct {
	outputs         map[loom.WorkerType]string
	statuses        map[loom.WorkerType]loom.TaskStatus
	errors          map[loom.WorkerType]string
	submissions     []loom.TaskRequest
	tasks           map[string]*loom.Task
	canceled        []string
	getCalls        int
	getContextCalls int
	getContextScope string
}

func newMockLoom(outputs map[loom.WorkerType]string) *mockLoom {
	return &mockLoom{
		outputs:  outputs,
		statuses: map[loom.WorkerType]loom.TaskStatus{},
		errors:   map[loom.WorkerType]string{},
		tasks:    map[string]*loom.Task{},
	}
}

func (m *mockLoom) Submit(_ context.Context, req loom.TaskRequest) (string, error) {
	id := fmt.Sprintf("review-task-%d", len(m.submissions)+1)
	m.submissions = append(m.submissions, req)
	status := m.statuses[req.WorkerType]
	if status == "" {
		status = loom.TaskStatusCompleted
	}
	m.tasks[id] = &loom.Task{
		ID:           id,
		Status:       status,
		WorkerType:   req.WorkerType,
		ProjectID:    req.ProjectID,
		ParentTaskID: req.ParentTaskID,
		CLI:          req.CLI,
		Role:         req.Role,
		Metadata:     req.Metadata,
		Result:       m.outputs[req.WorkerType],
		Error:        m.errors[req.WorkerType],
	}
	return id, nil
}

func (m *mockLoom) Get(taskID string) (*loom.Task, error) {
	m.getCalls++
	task, ok := m.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	return task, nil
}

func (m *mockLoom) GetContext(ctx context.Context, taskID string) (*loom.Task, error) {
	m.getContextCalls++
	if value, _ := ctx.Value(reviewPassContextKey{}).(string); value != "" {
		m.getContextScope = value
	}
	task, ok := m.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	return task, nil
}

func (m *mockLoom) Cancel(taskID string) error {
	m.canceled = append(m.canceled, taskID)
	return nil
}

func testCriteria() Criteria {
	return Criteria{
		ParentTaskID: "review-root",
		ProjectID:    "project-1",
		RequestID:    "request-1",
		CWD:          "/workspace",
		Env:          map[string]string{"SESSION_KEY": "session-value"},
		CLI:          "codex",
		Model:        "review-model",
		Effort:       "high",
		TaskTimeout:  time.Second,
		PollInterval: time.Millisecond,
	}
}

func passJSON(summary string, findings []Finding) string {
	if findings == nil {
		findings = []Finding{}
	}
	body := fmt.Sprintf(`{"summary":%q,"findings":[`, summary)
	for i, finding := range findings {
		if i > 0 {
			body += ","
		}
		line := "null"
		if finding.Line != nil {
			line = fmt.Sprintf("%d", *finding.Line)
		}
		body += fmt.Sprintf(`{"severity":%q,"file":%q,"line":%s,"body":%q}`,
			finding.Severity, finding.File, line, finding.Body)
	}
	return body + "]}"
}

func assertPassOrder(t *testing.T, results []PassResult, want []PassName) {
	t.Helper()
	for i, name := range want {
		if results[i].Name != name {
			t.Fatalf("results[%d].Name = %s, want %s", i, results[i].Name, name)
		}
	}
}

func assertSubmission(t *testing.T, req loom.TaskRequest, wantWorker loom.WorkerType, wantPass PassName) {
	t.Helper()
	if req.WorkerType != wantWorker {
		t.Fatalf("WorkerType = %s, want %s", req.WorkerType, wantWorker)
	}
	if req.Metadata["review_pass"] != string(wantPass) {
		t.Fatalf("review_pass metadata = %#v, want %s", req.Metadata["review_pass"], wantPass)
	}
	if req.Role != string(wantPass) {
		t.Fatalf("Role = %q, want %q", req.Role, wantPass)
	}
}

func intPtr(value int) *int {
	return &value
}
