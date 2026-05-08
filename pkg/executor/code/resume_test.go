package code

import (
	"context"
	"errors"
	"testing"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/types"
)

func TestCodeWorkerResumeFromTaskHydratesMetadata(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["task-1"] = &loom.Task{
		ID:         "task-1",
		WorkerType: WorkerTypeCode,
		ProjectID:  "project-a",
		Metadata: map[string]any{
			MetadataThreadID:   "thread-1",
			MetadataWorkerType: string(WorkerTypeCode),
		},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	meta, err := worker.ResumeFromTask(contextWithResumeProjectID(context.Background(), "project-a"), "task-1")
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
		Metadata:   map[string]any{MetadataThreadID: "thread-1"},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	_, err := worker.ResumeFromTask(contextWithResumeProjectID(context.Background(), "project-a"), "review-task")
	assertCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
}

func TestCodeWorkerResumeFromTaskRejectsCrossWorktree(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["task-1"] = &loom.Task{
		ID:         "task-1",
		WorkerType: WorkerTypeCode,
		ProjectID:  "worktree-a",
		Metadata: map[string]any{
			MetadataThreadID:   "thread-1",
			MetadataWorkerType: string(WorkerTypeCode),
		},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	_, err := worker.ResumeFromTask(contextWithResumeProjectID(context.Background(), "worktree-b"), "task-1")
	cliErr := assertCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
	if cliErr.Retryable {
		t.Fatalf("Retryable = true, want false")
	}
	if cliErr.Message != "cross-worktree resume rejected: resume_id belongs to a different worktree" {
		t.Fatalf("message = %q, want cross-worktree rejection", cliErr.Message)
	}
}

func TestCodeWorkerResumeFromTaskRejectsMissingTask(t *testing.T) {
	worker := newTestCodeWorker(t, workerTestDeps{loom: newMockLoom(`{"verdict":"APPLY","confidence":1}`)})

	_, err := worker.ResumeFromTask(context.Background(), "missing-task")
	assertCLIErrorCode(t, err, types.CLIErrorCodeUserInputError)
}

func TestCodeWorkerResumeFromTaskRejectsMissingThreadID(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["task-1"] = &loom.Task{
		ID:         "task-1",
		WorkerType: WorkerTypeCode,
		ProjectID:  "project-a",
		Metadata:   map[string]any{MetadataWorkerType: string(WorkerTypeCode)},
	}
	worker := newTestCodeWorker(t, workerTestDeps{loom: loomClient})

	_, err := worker.ResumeFromTask(contextWithResumeProjectID(context.Background(), "project-a"), "task-1")
	assertCLIErrorCode(t, err, types.CLIErrorCodeCapabilityMismatch)
}

func TestCodeWorkerResumeFromTaskRejectsMissingCurrentProject(t *testing.T) {
	loomClient := newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	loomClient.tasks["task-1"] = &loom.Task{
		ID:         "task-1",
		WorkerType: WorkerTypeCode,
		ProjectID:  "project-a",
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
