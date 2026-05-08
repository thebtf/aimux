package types_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/tenant"
)

func TestHydrateResumeMetadataSameWorker(t *testing.T) {
	tasks := fakeResumeTasks{tasks: map[string]*loom.Task{
		"task-1": {
			ID:         "task-1",
			WorkerType: loom.WorkerType("code"),
			Metadata: map[string]any{
				types.MetadataThreadID:   "thread-1",
				types.MetadataWorkerType: "code",
				"kept":                   "yes",
			},
		},
	}}

	meta, err := types.HydrateResumeMetadata(context.Background(), tasks, "task-1", loom.WorkerType("code"), types.MetadataThreadID)
	if err != nil {
		t.Fatalf("HydrateResumeMetadata returned error: %v", err)
	}
	if meta[types.MetadataThreadID] != "thread-1" {
		t.Fatalf("thread_id = %#v, want thread-1", meta[types.MetadataThreadID])
	}
	if meta[types.MetadataWorkerType] != "code" {
		t.Fatalf("worker_type = %#v, want code", meta[types.MetadataWorkerType])
	}
	if meta[types.MetadataResumeTaskID] != "task-1" {
		t.Fatalf("resume_task_id = %#v, want task-1", meta[types.MetadataResumeTaskID])
	}
	if meta["kept"] != "yes" {
		t.Fatalf("kept metadata = %#v, want yes", meta["kept"])
	}
}

func TestHydrateResumeMetadataRejectsCrossWorker(t *testing.T) {
	tasks := fakeResumeTasks{tasks: map[string]*loom.Task{
		"review-task": {
			ID:         "review-task",
			WorkerType: loom.WorkerType("review"),
			Metadata:   map[string]any{types.MetadataThreadID: "thread-1"},
		},
	}}

	_, err := types.HydrateResumeMetadata(context.Background(), tasks, "review-task", loom.WorkerType("code"), types.MetadataThreadID)
	assertResumeCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
}

func TestHydrateResumeMetadataRejectsSubTask(t *testing.T) {
	tasks := fakeResumeTasks{tasks: map[string]*loom.Task{
		"child-task": {
			ID:           "child-task",
			ParentTaskID: "root-task",
			WorkerType:   loom.WorkerType("code"),
			Metadata:     map[string]any{types.MetadataThreadID: "thread-1"},
		},
	}}

	_, err := types.HydrateResumeMetadata(context.Background(), tasks, "child-task", loom.WorkerType("code"), types.MetadataThreadID)
	assertResumeCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
	if !strings.Contains(err.Error(), "cannot resume sub-task; resume root") {
		t.Fatalf("error = %v, want root-only reason", err)
	}
}

func TestHydrateResumeMetadataUsesContextScopedLookup(t *testing.T) {
	tasks := &contextOnlyResumeTasks{
		taskID: "task-1",
		task: &loom.Task{
			ID:         "task-1",
			WorkerType: loom.WorkerType("review"),
			ProjectID:  "project-a",
			TenantID:   "tenant-a",
			Metadata: map[string]any{
				types.MetadataThreadID:   "thread-1",
				types.MetadataWorkerType: "review",
			},
		},
	}
	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-a"})

	meta, err := types.HydrateResumeMetadata(ctx, tasks, "task-1", loom.WorkerType("review"), types.MetadataThreadID)
	if err != nil {
		t.Fatalf("HydrateResumeMetadata returned error: %v", err)
	}
	if meta[types.MetadataThreadID] != "thread-1" {
		t.Fatalf("thread_id = %#v, want thread-1", meta[types.MetadataThreadID])
	}
	if tasks.getCalled {
		t.Fatal("unscoped Get was called; want context-scoped GetContext")
	}
	if tasks.getContextCalls != 1 {
		t.Fatalf("GetContext calls = %d, want 1", tasks.getContextCalls)
	}
	if tasks.getContextTenantID != "tenant-a" {
		t.Fatalf("GetContext tenant = %q, want tenant-a", tasks.getContextTenantID)
	}
}

func TestHydrateResumeMetadataRejectsCrossWorktreeScope(t *testing.T) {
	tasks := fakeResumeTasks{tasks: map[string]*loom.Task{
		"task-1": {
			ID:         "task-1",
			WorkerType: loom.WorkerType("review"),
			ProjectID:  "project-b",
			TenantID:   "tenant-a",
			Metadata:   map[string]any{types.MetadataThreadID: "thread-1"},
		},
	}}
	ctx := types.ContextWithResumeScope(context.Background(), "project-a", "tenant-a")

	_, err := types.HydrateResumeMetadata(ctx, tasks, "task-1", loom.WorkerType("review"), types.MetadataThreadID)
	assertResumeCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
	if !strings.Contains(err.Error(), "different worktree") {
		t.Fatalf("error = %v, want cross-worktree rejection", err)
	}
}

func TestHydrateResumeMetadataRejectsCrossTenantScope(t *testing.T) {
	tasks := fakeResumeTasks{tasks: map[string]*loom.Task{
		"task-1": {
			ID:         "task-1",
			WorkerType: loom.WorkerType("review"),
			ProjectID:  "project-a",
			TenantID:   "tenant-b",
			Metadata:   map[string]any{types.MetadataThreadID: "thread-1"},
		},
	}}
	ctx := types.ContextWithResumeScope(context.Background(), "project-a", "tenant-a")

	_, err := types.HydrateResumeMetadata(ctx, tasks, "task-1", loom.WorkerType("review"), types.MetadataThreadID)
	assertResumeCLIErrorCode(t, err, types.CLIErrorCodeResumeWorkerMismatch)
	if !strings.Contains(err.Error(), "different tenant") {
		t.Fatalf("error = %v, want cross-tenant rejection", err)
	}
}

func TestResumableWorkerInterface(t *testing.T) {
	var _ types.ResumableWorker = fakeResumableWorker{}
}

type fakeResumeTasks struct {
	tasks map[string]*loom.Task
}

func (f fakeResumeTasks) Get(taskID string) (*loom.Task, error) {
	task, ok := f.tasks[taskID]
	if !ok {
		return nil, loom.ErrTaskNotFound
	}
	return task, nil
}

type contextOnlyResumeTasks struct {
	taskID             string
	task               *loom.Task
	getCalled          bool
	getContextCalls    int
	getContextTenantID string
}

func (f *contextOnlyResumeTasks) Get(string) (*loom.Task, error) {
	f.getCalled = true
	return nil, errors.New("unscoped Get should not be called")
}

func (f *contextOnlyResumeTasks) GetContext(ctx context.Context, taskID string) (*loom.Task, error) {
	f.getContextCalls++
	tc, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, errors.New("tenant context missing")
	}
	f.getContextTenantID = tc.TenantID
	if taskID != f.taskID {
		return nil, loom.ErrTaskNotFound
	}
	return f.task, nil
}

type fakeResumableWorker struct{}

func (fakeResumableWorker) ResumeFromTask(context.Context, string) (map[string]any, error) {
	return map[string]any{}, nil
}

func assertResumeCLIErrorCode(t *testing.T, err error, want types.CLIErrorCode) {
	t.Helper()
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != want {
		t.Fatalf("CLIError code = %s, want %s", cliErr.Code, want)
	}
}
