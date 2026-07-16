package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/code"
	"github.com/thebtf/aimux/pkg/executor/review"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/server/classifier"
	"github.com/thebtf/aimux/pkg/types"
)

func TestPersistedMetadataNumbersPreservePublicControls(t *testing.T) {
	task := &loom.Task{Metadata: map[string]any{
		"confidence_score": json.Number("0.25"),
		"rounds":           json.Number("3"),
		"max_attempts":     json.Number("2"),
	}}
	result := buildTaskResult(task, "", 1, nil)
	if result.ConfidenceScore != 0.25 || result.Rounds != 3 {
		t.Fatalf("TaskResult = %#v, want persisted confidence and rounds", result)
	}
	if got := fallbackOptionsFromTaskMetadata(task.Metadata).MaxAttempts; got != 2 {
		t.Fatalf("max_attempts = %d, want 2", got)
	}
	for _, value := range []json.Number{"2.5", "9223372036854775808"} {
		if _, ok := metadataInt(map[string]any{"value": value}, "value"); ok {
			t.Fatalf("metadataInt(%q) accepted non-integral or out-of-range value", value)
		}
	}
}

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
	if _, ok := req.Metadata[taskWorkerSessionRequestMetadata]; ok {
		t.Fatalf("public default unexpectedly persisted %s metadata: %#v", taskWorkerSessionRequestMetadata, req.Metadata)
	}

	if result.TaskClass != classifier.TaskClassReview {
		t.Fatalf("result task_class = %s, want review", result.TaskClass)
	}
	if result.TaskID == "" || result.JobID != result.TaskID {
		t.Fatalf("result missing task/job id alias: %#v", result)
	}
	if result.Content != "" {
		t.Fatalf("accepted result content = %q, want empty until status(include_content=true)", result.Content)
	}
	if result.Status != loom.TaskStatusDispatched {
		t.Fatalf("accepted status = %s, want dispatched", result.Status)
	}
	if result.StatusCommand == "" || result.CancelCommand == "" || result.TaskURI == "" || result.ProgressURI == "" {
		t.Fatalf("accepted result missing observation fields: %#v", result)
	}
}

func TestTaskRouterPersistsInternalWorkerSessionRequest(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)
	expected := types.SessionBindingIdentity{
		HandleID:           "resume-handle",
		HandleGeneration:   4,
		RegistryGeneration: 7,
		ProviderSession: types.SessionIdentity{
			Provider:   "neutral",
			ID:         "provider-session",
			Generation: 9,
		},
	}
	want := taskSessionRequest{
		Mode:            types.SessionBindingModeExactResume,
		WorkerSessionID: "worker-session-42",
		Expected:        &expected,
	}
	result, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:         "Continue the durable internal worker session.",
		TaskClass:      classifier.TaskClassCode,
		ProjectID:      "project-session",
		SessionRequest: want,
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if _, ok := result.Metadata[taskWorkerSessionRequestMetadata]; ok {
		t.Fatalf("accepted TaskResult leaked %s metadata: %#v", taskWorkerSessionRequestMetadata, result.Metadata)
	}

	submitted := fake.onlySubmission(t)
	got, err := taskSessionRequestFromMetadata(submitted.Metadata)
	if err != nil {
		t.Fatalf("taskSessionRequestFromMetadata: %v", err)
	}
	if got.Mode != want.Mode || got.WorkerSessionID != want.WorkerSessionID || got.ParentWorkerSessionID != "" || got.Expected == nil || *got.Expected != expected || got.Parent != nil {
		t.Fatalf("persisted Worker Session request = %#v, want %#v", got, want)
	}
	projected := buildTaskResult(&loom.Task{ID: "session-projection", Metadata: submitted.Metadata}, classifier.TaskClassCode, 1, nil)
	if _, ok := projected.Metadata[taskWorkerSessionRequestMetadata]; ok {
		t.Fatalf("public TaskResult leaked %s metadata: %#v", taskWorkerSessionRequestMetadata, projected.Metadata)
	}
}

func TestTaskRouterSessionRequestBypassesRecipeReplay(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)
	base := TaskRequest{
		Prompt:    "Review the durable session boundary.",
		TaskClass: classifier.TaskClassReview,
		ProjectID: "project-session-replay",
		Metadata: map[string]any{
			"recipe_id":                     "session-safe-recipe",
			"recipe_read_only":              true,
			recipePolicyEnforcedMetadataKey: true,
		},
	}
	first, err := router.Dispatch(context.Background(), base)
	if err != nil {
		t.Fatalf("first Dispatch() error = %v", err)
	}
	expected := types.SessionBindingIdentity{
		HandleID:           "resume-handle",
		HandleGeneration:   2,
		RegistryGeneration: 3,
		ProviderSession: types.SessionIdentity{
			Provider:   "neutral",
			ID:         "resume-provider-session",
			Generation: 4,
		},
	}
	resume := base
	resume.SessionRequest = taskSessionRequest{
		Mode:            types.SessionBindingModeExactResume,
		WorkerSessionID: "resume-worker-session",
		Expected:        &expected,
	}
	second, err := router.Dispatch(context.Background(), resume)
	if err != nil {
		t.Fatalf("session Dispatch() error = %v", err)
	}
	if second.TaskID == first.TaskID {
		t.Fatalf("session-bound request replayed task %q instead of submitting a fresh execution", second.TaskID)
	}
	if got := fake.submissionCount(); got != 2 {
		t.Fatalf("submission count = %d, want 2", got)
	}
	if _, ok := second.Metadata[recipeReplayCacheHitMetadata]; ok {
		t.Fatalf("session-bound accepted result carried replay metadata: %#v", second.Metadata)
	}
}

func TestTaskRouterRejectsInvalidInternalWorkerSessionRequestBeforeSubmit(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)
	_, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:    "Fork from an invalid parent.",
		TaskClass: classifier.TaskClassCode,
		SessionRequest: taskSessionRequest{
			Mode:            types.SessionBindingModeFork,
			WorkerSessionID: "fork-child",
		},
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want invalid internal Worker Session request rejection")
	}
	if got := fake.submissionCount(); got != 0 {
		t.Fatalf("submission count = %d, want 0", got)
	}
}

func TestTaskRouterDispatchNilContextUsesBackground(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)

	if _, err := router.Dispatch(nil, TaskRequest{
		Prompt:    "Implement pkg/server/task_router.go nil context handling.",
		TaskClass: classifier.TaskClassCode,
		ProjectID: "project-1",
	}); err != nil {
		t.Fatalf("Dispatch(nil) error = %v", err)
	}
	if got := fake.submissionCount(); got != 1 {
		t.Fatalf("submission count = %d, want 1", got)
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

func TestTaskRouterDriverCLIOverrideStaysMetadataOnly(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)

	_, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:    "Implement pkg/server/task_router.go CLI override handling.",
		TaskClass: classifier.TaskClassCode,
		CLI:       "gemini",
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	req := fake.onlySubmission(t)
	if req.CLI != "" {
		t.Fatalf("TaskRequest.CLI = %q, want empty", req.CLI)
	}
	assertMetadataString(t, req.Metadata, "driver_cli_override", "gemini")
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

func TestTaskRouterDispatchCallerCancellationAfterSubmitDoesNotCancelTask(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	fake.completeOnSubmit = false
	router := mustTaskRouter(t, fake, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	fake.onSubmit = func() {
		cancel()
	}

	result, err := router.Dispatch(ctx, TaskRequest{
		Prompt:    "Implement pkg/server/task_router.go cancellation handling.",
		TaskClass: classifier.TaskClassCode,
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v, want accepted task", err)
	}
	if result.Status != loom.TaskStatusDispatched {
		t.Fatalf("status = %s, want dispatched", result.Status)
	}
	if fake.cancelCount() != 0 {
		t.Fatalf("cancel count = %d, want 0 after accepted submit", fake.cancelCount())
	}
}

func TestTaskRouterDispatchTimeoutDoesNotWaitAfterSubmit(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	fake.completeOnSubmit = false
	router := mustTaskRouter(t, fake, 2*time.Millisecond)

	result, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:    "Implement pkg/server/task_router.go timeout handling.",
		TaskClass: classifier.TaskClassCode,
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v, want accepted task", err)
	}
	if result.TaskID == "" {
		t.Fatalf("TaskID empty in accepted result: %#v", result)
	}
	if fake.cancelCount() != 0 {
		t.Fatalf("cancel count = %d, want 0", fake.cancelCount())
	}
}

func TestTaskRouterLegacyWaitTimeoutCancelsBlockingGet(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	fake.completeOnSubmit = false
	fake.onGet = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	router := mustTaskRouter(t, fake, 2*time.Millisecond)

	taskID, submitErr := fake.Submit(context.Background(), loom.TaskRequest{
		WorkerType: code.WorkerTypeCode,
		Prompt:     "legacy waiter",
	})
	if submitErr != nil {
		t.Fatalf("fake Submit() error = %v", submitErr)
	}

	_, err := router.wait(context.Background(), taskID, classifier.TaskClassCode, code.WorkerTypeCode, 1, nil, 2*time.Millisecond)
	if err == nil {
		t.Fatal("wait() error = nil, want timeout")
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

func TestTaskRouterWaitTimeoutHonorsRequestTimeout(t *testing.T) {
	t.Parallel()

	router := mustTaskRouter(t, newFakeTaskRouterLoom(), 5*time.Second)

	if got, want := router.waitTimeoutForRequest(1), time.Second+time.Millisecond; got != want {
		t.Fatalf("wait timeout = %v, want %v", got, want)
	}
	if got, want := router.waitTimeoutForRequest(0), 5*time.Second; got != want {
		t.Fatalf("default wait timeout = %v, want %v", got, want)
	}
}

func TestTaskRouterRejectsExplicitThinkByDefault(t *testing.T) {
	t.Parallel()

	fake := newFakeTaskRouterLoom()
	router := mustTaskRouter(t, fake, 500*time.Millisecond)

	_, err := router.Dispatch(context.Background(), TaskRequest{
		Prompt:    "Think through this architecture.",
		TaskClass: "think",
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
	onSubmit         func()
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
	if f.onSubmit != nil {
		f.onSubmit()
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

func (f *fakeTaskRouterLoom) List(projectID string, statuses ...loom.TaskStatus) ([]*loom.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	allowed := make(map[loom.TaskStatus]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	tasks := make([]*loom.Task, 0, len(f.tasks))
	for _, task := range f.tasks {
		if task == nil || (projectID != "" && task.ProjectID != projectID) {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[task.Status]; !ok {
				continue
			}
		}
		cp := *task
		cp.Metadata = cloneTaskMetadata(task.Metadata)
		tasks = append(tasks, &cp)
	}
	return tasks, nil
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
