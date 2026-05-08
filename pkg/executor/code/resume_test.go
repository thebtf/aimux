package code

import (
	"context"
	"errors"
	"testing"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/tenant"
)

func TestCodeWorkerResumeFromTaskHydratesMetadata(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["task-1"] = &loom.Task{
		ID:         "task-1",
		WorkerType: WorkerTypeCode,
		ProjectID:  "project-a",
		TenantID:   loom.LegacyTenantID,
		Metadata: map[string]any{
			MetadataThreadID:   "thread-1",
			MetadataWorkerType: string(WorkerTypeCode),
		},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	meta, err := worker.ResumeFromTask(contextWithResumeScope(context.Background(), "project-a", loom.LegacyTenantID), "task-1")
	if err != nil {
		t.Fatalf("ResumeFromTask returned error: %v", err)
	}
	if meta[MetadataThreadID] != "thread-1" {
		t.Fatalf("resume meta = %#v, want thread_id", meta)
	}
	if meta[MetadataWorkerType] != string(WorkerTypeCode) {
		t.Fatalf("resume meta = %#v, want worker_type=code", meta)
	}
	if meta[MetadataResumeTaskID] != "task-1" {
		t.Fatalf("resume meta = %#v, want resume_task_id", meta)
	}
}

func TestCodeWorkerResumeFromTaskRejectsCrossWorker(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["review-task"] = &loom.Task{
		ID:         "review-task",
		WorkerType: loom.WorkerType("review"),
		ProjectID:  "project-a",
		TenantID:   loom.LegacyTenantID,
		Metadata:   map[string]any{MetadataThreadID: "thread-1"},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	_, err := worker.ResumeFromTask(contextWithResumeScope(context.Background(), "project-a", loom.LegacyTenantID), "review-task")
	assertCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
}

func TestCodeWorkerResumeFromTaskRejectsCrossWorktree(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["task-1"] = &loom.Task{
		ID:         "task-1",
		WorkerType: WorkerTypeCode,
		ProjectID:  "worktree-a",
		TenantID:   loom.LegacyTenantID,
		Metadata: map[string]any{
			MetadataThreadID:   "thread-1",
			MetadataWorkerType: string(WorkerTypeCode),
		},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	_, err := worker.ResumeFromTask(contextWithResumeScope(context.Background(), "worktree-b", loom.LegacyTenantID), "task-1")
	cliErr := assertCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
	if cliErr.Retryable {
		t.Fatalf("Retryable = true, want false")
	}
	if cliErr.Message != "cross-worktree resume rejected: resume_id belongs to a different worktree" {
		t.Fatalf("message = %q, want cross-worktree rejection", cliErr.Message)
	}
}

func TestCodeWorkerResumeFromTaskRejectsCrossTenant(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["task-1"] = &loom.Task{
		ID:         "task-1",
		WorkerType: WorkerTypeCode,
		ProjectID:  "project-a",
		TenantID:   "tenant-a",
		Metadata: map[string]any{
			MetadataThreadID:   "thread-1",
			MetadataWorkerType: string(WorkerTypeCode),
		},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	_, err := worker.ResumeFromTask(contextWithResumeScope(context.Background(), "project-a", "tenant-b"), "task-1")
	cliErr := assertCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
	if cliErr.Message != "cross-tenant resume rejected: resume_id belongs to a different tenant" {
		t.Fatalf("message = %q, want cross-tenant rejection", cliErr.Message)
	}
}

func TestCodeWorkerResumeFromTaskRejectsMissingTask(t *testing.T) {
	worker := newTestCodeWorker(t, workerTestDeps{loom: newMockLoom(`{"verdict":"APPLY","confidence":1}`)})

	_, err := worker.ResumeFromTask(context.Background(), "missing-task")
	assertCLIErrorCode(t, err, types.CLIErrorCodeUserInputError)
}

func TestCodeWorkerResumeFromTaskUsesContextScopedLookup(t *testing.T) {
	loomClient := &contextOnlyResumeLoom{
		taskID: "task-1",
		task: &loom.Task{
			ID:         "task-1",
			WorkerType: WorkerTypeCode,
			ProjectID:  "project-a",
			TenantID:   "tenant-a",
			Metadata: map[string]any{
				MetadataThreadID:   "thread-1",
				MetadataWorkerType: string(WorkerTypeCode),
			},
		},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})
	resumeCtx := contextWithResumeScope(context.Background(), "project-a", "tenant-a")

	if err := worker.validateResumeProject(resumeCtx, "task-1"); err != nil {
		t.Fatalf("validateResumeProject returned error: %v", err)
	}
	if _, err := worker.ResumeFromTask(resumeCtx, "task-1"); err != nil {
		t.Fatalf("ResumeFromTask returned error: %v", err)
	}
	if loomClient.getCalled {
		t.Fatal("unscoped Get was called; want context-scoped GetContext")
	}
	if loomClient.getContextCalls != 2 {
		t.Fatalf("GetContext calls = %d, want 2", loomClient.getContextCalls)
	}
	if loomClient.getContextTenantID != "tenant-a" {
		t.Fatalf("GetContext tenant = %q, want tenant-a", loomClient.getContextTenantID)
	}
}

func TestCodeWorkerResumeFromTaskRejectsMissingThreadID(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["task-1"] = &loom.Task{
		ID:         "task-1",
		WorkerType: WorkerTypeCode,
		ProjectID:  "project-a",
		TenantID:   loom.LegacyTenantID,
		Metadata:   map[string]any{MetadataWorkerType: string(WorkerTypeCode)},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	_, err := worker.ResumeFromTask(contextWithResumeScope(context.Background(), "project-a", loom.LegacyTenantID), "task-1")
	assertCLIErrorCode(t, err, types.CLIErrorCodeCapabilityMismatch)
}

func TestCodeWorkerResumeFromTaskRejectsMissingCurrentProject(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["task-1"] = &loom.Task{
		ID:         "task-1",
		WorkerType: WorkerTypeCode,
		ProjectID:  "project-a",
		TenantID:   loom.LegacyTenantID,
		Metadata: map[string]any{
			MetadataThreadID:   "thread-1",
			MetadataWorkerType: string(WorkerTypeCode),
		},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	_, err := worker.ResumeFromTask(context.Background(), "task-1")
	cliErr := assertCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
	if cliErr.Message != "cross-worktree resume rejected: current worktree project id is unavailable" {
		t.Fatalf("message = %q, want missing current project rejection", cliErr.Message)
	}
}

func assertCLIErrorCode(t *testing.T, err error, want types.CLIErrorCode) *types.CLIError {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != want {
		t.Fatalf("CLIError code = %s, want %s", cliErr.Code, want)
	}
	return cliErr
}

type contextOnlyResumeLoom struct {
	taskID             string
	task               *loom.Task
	getCalled          bool
	getContextCalls    int
	getContextTenantID string
}

func (m *contextOnlyResumeLoom) Submit(context.Context, loom.TaskRequest) (string, error) {
	return "", errors.New("Submit should not be called")
}

func (m *contextOnlyResumeLoom) Get(string) (*loom.Task, error) {
	m.getCalled = true
	return nil, errors.New("unscoped Get should not be called")
}

func (m *contextOnlyResumeLoom) GetContext(ctx context.Context, taskID string) (*loom.Task, error) {
	m.getContextCalls++
	tc, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, errors.New("tenant context missing")
	}
	m.getContextTenantID = tc.TenantID
	if taskID != m.taskID {
		return nil, errors.New("task not found")
	}
	return m.task, nil
}
