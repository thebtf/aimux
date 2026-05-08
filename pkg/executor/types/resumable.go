package types

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/tenant"
)

const (
	MetadataThreadID     = "thread_id"
	MetadataWorkerType   = "worker_type"
	MetadataResumeTaskID = "resume_task_id"
)

type resumeScopeContextKey struct{}

// ResumeScope constrains resume lookup to the current worktree project and tenant.
type ResumeScope struct {
	ProjectID string
	TenantID  string
}

// ResumableWorker resumes a new root task from prior root-task metadata.
type ResumableWorker interface {
	ResumeFromTask(ctx context.Context, prevTaskID string) (map[string]any, error)
}

// ResumeTaskGetter is the task lookup surface required for resume hydration.
type ResumeTaskGetter interface {
	Get(taskID string) (*loom.Task, error)
}

// ResumeContextTaskGetter optionally scopes resume lookup to the caller context.
type ResumeContextTaskGetter interface {
	GetContext(ctx context.Context, taskID string) (*loom.Task, error)
}

// ContextWithResumeScope records the current worktree and tenant for resume validation.
func ContextWithResumeScope(ctx context.Context, projectID string, tenantID string) context.Context {
	scope := ResumeScope{ProjectID: strings.TrimSpace(projectID), TenantID: effectiveResumeTenantID(tenantID)}
	ctx = context.WithValue(ctx, resumeScopeContextKey{}, scope)
	if tc, ok := tenant.FromContext(ctx); ok {
		tc.TenantID = scope.TenantID
		return tenant.WithContext(ctx, tc)
	}
	return tenant.WithContext(ctx, tenant.TenantContext{TenantID: scope.TenantID})
}

// HydrateResumeMetadata validates a prior root task and returns resume metadata.
func HydrateResumeMetadata(ctx context.Context, tasks ResumeTaskGetter, prevTaskID string, expectedWorkerType loom.WorkerType, requiredKeys ...string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, NewCanceled("resume canceled", err)
	}
	if strings.TrimSpace(prevTaskID) == "" {
		return nil, NewUserInputError("resume_id is required", nil)
	}
	if tasks == nil {
		return nil, NewCapabilityMismatch("resume task lookup is required", nil)
	}
	if expectedWorkerType == "" {
		return nil, NewCapabilityMismatch("expected worker type is required", nil)
	}

	prev, err := getResumeTask(ctx, tasks, prevTaskID)
	if err != nil {
		return nil, resumeTaskLookupError(prevTaskID, err)
	}
	if prev == nil {
		return nil, NewUnknown(fmt.Sprintf("resume task %q lookup returned nil task", prevTaskID), nil)
	}
	if err := validateResumeScope(ctx, prev); err != nil {
		return nil, err
	}
	if prev.ParentTaskID != "" {
		return nil, NewResumeWorkerMismatch("cannot resume sub-task; resume root", nil)
	}
	if prev.WorkerType != expectedWorkerType {
		return nil, resumeWorkerMismatch(expectedWorkerType, prev.WorkerType)
	}
	if metaWorkerType, ok := resumeMetadataString(prev.Metadata, MetadataWorkerType); ok && metaWorkerType != string(expectedWorkerType) {
		return nil, resumeWorkerMismatch(expectedWorkerType, loom.WorkerType(metaWorkerType))
	}
	for _, key := range requiredKeys {
		if value, ok := resumeMetadataString(prev.Metadata, key); !ok || strings.TrimSpace(value) == "" {
			return nil, NewCapabilityMismatch(fmt.Sprintf("resume task is missing %s", key), nil)
		}
	}

	meta := cloneResumeMetadata(prev.Metadata)
	meta[MetadataWorkerType] = string(expectedWorkerType)
	meta[MetadataResumeTaskID] = prevTaskID
	return meta, nil
}

func getResumeTask(ctx context.Context, tasks ResumeTaskGetter, taskID string) (*loom.Task, error) {
	if getter, ok := tasks.(ResumeContextTaskGetter); ok {
		return getter.GetContext(ctx, taskID)
	}
	return tasks.Get(taskID)
}

func resumeTaskLookupError(prevTaskID string, err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return NewCanceled(fmt.Sprintf("resume task %q lookup canceled", prevTaskID), err)
	case errors.Is(err, loom.ErrTaskNotFound):
		return NewUserInputError(fmt.Sprintf("resume task %q not found", prevTaskID), err)
	default:
		return NewCapabilityMismatch(fmt.Sprintf("resume task %q lookup failed", prevTaskID), err)
	}
}

func validateResumeScope(ctx context.Context, prev *loom.Task) error {
	scope, ok := ctx.Value(resumeScopeContextKey{}).(ResumeScope)
	if !ok {
		return nil
	}
	if scope.ProjectID == "" {
		return NewResumeWorkerMismatch("cross-worktree resume rejected: current worktree project id is unavailable", nil)
	}
	if prev.ProjectID != scope.ProjectID {
		return NewResumeWorkerMismatch("cross-worktree resume rejected: resume_id belongs to a different worktree", nil)
	}
	if scope.TenantID == "" {
		return NewResumeWorkerMismatch("cross-tenant resume rejected: current tenant id is unavailable", nil)
	}
	if effectiveResumeTenantID(prev.TenantID) != scope.TenantID {
		return NewResumeWorkerMismatch("cross-tenant resume rejected: resume_id belongs to a different tenant", nil)
	}
	return nil
}

func effectiveResumeTenantID(tenantID string) string {
	if strings.TrimSpace(tenantID) == "" {
		return loom.LegacyTenantID
	}
	return strings.TrimSpace(tenantID)
}

func resumeWorkerMismatch(expected loom.WorkerType, actual loom.WorkerType) *CLIError {
	return NewResumeWorkerMismatch(
		fmt.Sprintf("resume_id worker mismatch: expected %s, got %s", expected, actual),
		nil,
	)
}

func resumeMetadataString(metadata map[string]any, key string) (string, bool) {
	value, ok := metadata[key]
	if !ok || value == nil {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func cloneResumeMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	next := make(map[string]any, len(metadata)+2)
	for key, value := range metadata {
		next[key] = value
	}
	return next
}
