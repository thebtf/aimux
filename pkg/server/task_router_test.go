package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/code"
	"github.com/thebtf/aimux/pkg/executor/review"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/server/classifier"
)

func TestTaskRouterDispatchExplicitTaskClass(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router, err := NewTaskRouter(TaskRouterConfig{
		Loom:         fake,
		Classifier:   classifier.New(),
		WaitTimeout:  500 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewTaskRouter() error = %v", err)
	}

	result, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:    "review this change",
		TaskClass: classifier.TaskClassReview,
		ProjectID: "project-1",
		RequestID: "request-1",
		Target:    "HEAD",
		Gate:      true,
		Metadata:  map[string]any{"caller": "test"},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	req := fake.onlySubmission(t)
	if req.WorkerType != review.WorkerTypeReview {
		t.Fatalf("WorkerType = %s, want %s", req.WorkerType, review.WorkerTypeReview)
	}
	if req.ProjectID != "project-1" || req.RequestID != "request-1" {
		t.Fatalf("request identity = (%q, %q), want project/request", req.ProjectID, req.RequestID)
	}
	assertMetadataString(t, req.Metadata, "task_class", classifier.TaskClassReview)
	assertMetadataString(t, req.Metadata, "worker_type", string(review.WorkerTypeReview))
	assertMetadataString(t, req.Metadata, "target", "HEAD")
	assertMetadataString(t, req.Metadata, "review_target", "HEAD")
	assertMetadataBool(t, req.Metadata, "review_gate", true)
	assertMetadataString(t, req.Metadata, "caller", "test")

	if result.TaskClass != classifier.TaskClassReview {
		t.Fatalf("result task_class = %s, want review", result.TaskClass)
	}
	if result.TaskID == "" || result.Content == "" {
		t.Fatalf("result missing task_id/content: %#v", result)
	}
}

func TestTaskRouterDispatchClassifierResolved(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)

	result, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:    "Implement pkg/server/task_router.go cancellation handling and add tests.",
		ProjectID: "project-2",
		ResumeID:  "prev-code-task",
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	req := fake.onlySubmission(t)
	if req.WorkerType != code.WorkerTypeCode {
		t.Fatalf("WorkerType = %s, want %s", req.WorkerType, code.WorkerTypeCode)
	}
	assertMetadataString(t, req.Metadata, "task_class", classifier.TaskClassCode)
	assertMetadataString(t, req.Metadata, "worker_type", string(code.WorkerTypeCode))
	assertMetadataString(t, req.Metadata, "resume_id", "prev-code-task")
	assertMetadataString(t, req.Metadata, extypes.MetadataResumeTaskID, "prev-code-task")

	if result.TaskClass != classifier.TaskClassCode {
		t.Fatalf("result task_class = %s, want code", result.TaskClass)
	}
	if result.WorkerType != code.WorkerTypeCode {
		t.Fatalf("result worker_type = %s, want %s", result.WorkerType, code.WorkerTypeCode)
	}
	if result.ConfidenceScore < classifier.DefaultThreshold {
		t.Fatalf("confidence = %.3f, want >= %.3f", result.ConfidenceScore, classifier.DefaultThreshold)
	}
}

func TestTaskRouterClassifierRejectsUnroutableAutomaticClass(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)

	result, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt: "Research official documentation for the newest behavior.",
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want ClassificationAmbiguous")
	}
	var cliErr *extypes.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != extypes.CLIErrorCodeClassificationAmbiguous {
		t.Fatalf("code = %s, want %s", cliErr.Code, extypes.CLIErrorCodeClassificationAmbiguous)
	}
	if len(result.Candidates) == 0 {
		t.Fatal("candidates empty, want routable candidates")
	}
	for _, candidate := range result.Candidates {
		if candidate.TaskClass != classifier.TaskClassCode && candidate.TaskClass != classifier.TaskClassReview {
			t.Fatalf("candidate task_class = %s, want only routable classes", candidate.TaskClass)
		}
	}
	if got := fake.submissionCount(); got != 0 {
		t.Fatalf("submission count = %d, want 0", got)
	}
}

func TestTaskRouterDispatchAmbiguousPromptReturnsError(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)

	result, err := router.Dispatch(context.Background(), TaskRequest{Prompt: "Help me make this better."})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want ClassificationAmbiguous")
	}
	var cliErr *extypes.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != extypes.CLIErrorCodeClassificationAmbiguous {
		t.Fatalf("code = %s, want %s", cliErr.Code, extypes.CLIErrorCodeClassificationAmbiguous)
	}
	if len(result.Candidates) != 3 {
		t.Fatalf("candidates = %d, want top 3", len(result.Candidates))
	}
	if got := fake.submissionCount(); got != 0 {
		t.Fatalf("submission count = %d, want 0", got)
	}
}

func TestTaskRouterDispatchCallerCancellationCancelsTask(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	fake.completeOnSubmit = false
	router := mustTaskRouter(t, fake, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	fake.onGet = func(context.Context) error {
		cancel()
		return nil
	}

	_, err := router.Dispatch(ctx, TaskRequest{
		Prompt:    "Implement pkg/server/task_router.go cancellation handling.",
		TaskClass: classifier.TaskClassCode,
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want canceled")
	}
	var cliErr *extypes.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != extypes.CLIErrorCodeCanceled {
		t.Fatalf("code = %s, want %s", cliErr.Code, extypes.CLIErrorCodeCanceled)
	}
	if fake.cancelCount() != 1 {
		t.Fatalf("cancel count = %d, want 1", fake.cancelCount())
	}
}

func TestTaskRouterDispatchTimeoutCancelsTask(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	fake.completeOnSubmit = false
	router := mustTaskRouter(t, fake, 2*time.Millisecond)

	_, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:    "Implement pkg/server/task_router.go timeout handling.",
		TaskClass: classifier.TaskClassCode,
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want timeout")
	}
	var cliErr *extypes.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != extypes.CLIErrorCodeTimeout {
		t.Fatalf("code = %s, want %s", cliErr.Code, extypes.CLIErrorCodeTimeout)
	}
	if fake.cancelCount() != 1 {
		t.Fatalf("cancel count = %d, want 1", fake.cancelCount())
	}
}

func TestTaskRouterDispatchTimeoutCancelsBlockingGet(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	fake.completeOnSubmit = false
	fake.onGet = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	router := mustTaskRouter(t, fake, 2*time.Millisecond)

	_, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:    "Implement pkg/server/task_router.go blocking lookup cancellation.",
		TaskClass: classifier.TaskClassCode,
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want timeout")
	}
	var cliErr *extypes.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != extypes.CLIErrorCodeTimeout {
		t.Fatalf("code = %s, want %s", cliErr.Code, extypes.CLIErrorCodeTimeout)
	}
	if fake.cancelCount() != 1 {
		t.Fatalf("cancel count = %d, want 1", fake.cancelCount())
	}
}

func TestTaskRouterRejectsUnregisteredClassBeforeSubmit(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)

	_, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:    "Research official docs for the newest behavior.",
		TaskClass: classifier.TaskClassResearch,
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want unsupported task_class")
	}
	var cliErr *extypes.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != extypes.CLIErrorCodeUserInputError {
		t.Fatalf("code = %s, want %s", cliErr.Code, extypes.CLIErrorCodeUserInputError)
	}
	if got := fake.submissionCount(); got != 0 {
		t.Fatalf("submission count = %d, want 0", got)
	}
}

func mustTaskRouter(t *testing.T, l *fakeTaskRouterLoom, timeout time.Duration) *TaskRouter {
	t.Helper()
	router, err := NewTaskRouter(TaskRouterConfig{
		Loom:         l,
		Classifier:   classifier.New(),
		WaitTimeout:  timeout,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewTaskRouter() error = %v", err)
	}
	return router
}

type fakeTaskRouterLoom struct {
	mu               sync.Mutex
	completeOnSubmit bool
	submissions      []loom.TaskRequest
	tasks            map[string]*loom.Task
	cancels          []string
	onGet            func(context.Context) error
}

func newFakeTaskRouterLoom() *fakeTaskRouterLoom {
	return &fakeTaskRouterLoom{
		completeOnSubmit: true,
		tasks:            map[string]*loom.Task{},
	}
}

func (f *fakeTaskRouterLoom) Submit(_ context.Context, req loom.TaskRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	taskID := "task-" + string(rune('a'+len(f.submissions)))
	f.submissions = append(f.submissions, cloneTaskRequest(req))
	status := loom.TaskStatusRunning
	result := ""
	if f.completeOnSubmit {
		status = loom.TaskStatusCompleted
		result = "completed " + string(req.WorkerType)
	}
	f.tasks[taskID] = &loom.Task{
		ID:         taskID,
		Status:     status,
		WorkerType: req.WorkerType,
		ProjectID:  req.ProjectID,
		RequestID:  req.RequestID,
		Prompt:     req.Prompt,
		CLI:        req.CLI,
		Metadata:   cloneTaskMetadata(req.Metadata),
		Result:     result,
	}
	return taskID, nil
}

func (f *fakeTaskRouterLoom) GetContext(ctx context.Context, taskID string) (*loom.Task, error) {
	if f.onGet != nil {
		if err := f.onGet(ctx); err != nil {
			return nil, err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	task, ok := f.tasks[taskID]
	if !ok {
		return nil, loom.ErrTaskNotFound
	}
	cp := *task
	cp.Metadata = cloneTaskMetadata(task.Metadata)
	return &cp, nil
}

func (f *fakeTaskRouterLoom) Cancel(taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, taskID)
	if task := f.tasks[taskID]; task != nil {
		task.Status = loom.TaskStatusFailed
		task.Error = "canceled"
	}
	return nil
}

func (f *fakeTaskRouterLoom) onlySubmission(t *testing.T) loom.TaskRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.submissions) != 1 {
		t.Fatalf("submission count = %d, want 1", len(f.submissions))
	}
	return cloneTaskRequest(f.submissions[0])
}

func (f *fakeTaskRouterLoom) submissionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.submissions)
}

func (f *fakeTaskRouterLoom) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cancels)
}

func cloneTaskRequest(req loom.TaskRequest) loom.TaskRequest {
	req.Metadata = cloneTaskMetadata(req.Metadata)
	if req.Env != nil {
		env := make(map[string]string, len(req.Env))
		for key, value := range req.Env {
			env[key] = value
		}
		req.Env = env
	}
	return req
}

func assertMetadataString(t *testing.T, metadata map[string]any, key string, want string) {
	t.Helper()
	value, ok := metadata[key]
	if !ok {
		t.Fatalf("metadata[%q] missing", key)
	}
	got, ok := value.(string)
	if !ok {
		t.Fatalf("metadata[%q] = %#v, want string %q", key, value, want)
	}
	if got != want {
		t.Fatalf("metadata[%q] = %q, want %q", key, got, want)
	}
}

func assertMetadataBool(t *testing.T, metadata map[string]any, key string, want bool) {
	t.Helper()
	value, ok := metadata[key]
	if !ok {
		t.Fatalf("metadata[%q] missing", key)
	}
	got, ok := value.(bool)
	if !ok {
		t.Fatalf("metadata[%q] = %#v, want bool %v", key, value, want)
	}
	if got != want {
		t.Fatalf("metadata[%q] = %v, want %v", key, got, want)
	}
}
