package types

import (
	"context"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/loom"
)

const (
	MetadataThreadID     = "thread_id"
	MetadataWorkerType   = "worker_type"
	MetadataResumeTaskID = "resume_task_id"
)

// ResumableWorker resumes a new root task from prior root-task metadata.
type ResumableWorker interface {
	ResumeFromTask(ctx context.Context, prevTaskID string) (map[string]any, error)
}

// ResumeTaskGetter is the task lookup surface required for resume hydration.
type ResumeTaskGetter interface {
	Get(taskID string) (*loom.Task, error)
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

	prev, err := tasks.Get(prevTaskID)
	if err != nil {
		return nil, NewUserInputError(fmt.Sprintf("resume task %q not found", prevTaskID), err)
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
