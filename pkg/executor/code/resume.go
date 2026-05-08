package code

import (
	"context"
	"fmt"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/types"
)

const (
	MetadataThreadID     = "thread_id"
	MetadataWorkerType   = "worker_type"
	MetadataResumeTaskID = "resume_task_id"
)

// ResumeFromTask hydrates metadata for continuing a prior root code task.
func (w *CodeWorker) ResumeFromTask(ctx context.Context, prevTaskID string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, types.NewCanceled("code worker resume canceled", err)
	}
	if prevTaskID == "" {
		return nil, types.NewUserInputError("resume_id is required", nil)
	}
	if w.loom == nil {
		return nil, types.NewCapabilityMismatch("code worker Loom client is required for resume", nil)
	}

	prev, err := w.loom.Get(prevTaskID)
	if err != nil {
		return nil, types.NewUserInputError(fmt.Sprintf("resume task %q not found", prevTaskID), err)
	}
	if prev.WorkerType != WorkerTypeCode {
		return nil, resumeWorkerMismatch(prev.WorkerType)
	}
	if metaWorkerType, ok := metadataString(prev.Metadata, MetadataWorkerType); ok && metaWorkerType != string(WorkerTypeCode) {
		return nil, resumeWorkerMismatch(loom.WorkerType(metaWorkerType))
	}

	threadID, ok := metadataString(prev.Metadata, MetadataThreadID)
	if !ok || threadID == "" {
		return nil, types.NewCapabilityMismatch("code task is missing resumable thread_id", nil)
	}

	return map[string]any{
		MetadataThreadID:     threadID,
		MetadataWorkerType:   string(WorkerTypeCode),
		MetadataResumeTaskID: prevTaskID,
	}, nil
}

func resumeWorkerMismatch(actual loom.WorkerType) *types.CLIError {
	return types.NewResumeWorkerMismatch(
		fmt.Sprintf("resume_id worker mismatch: expected %s, got %s", WorkerTypeCode, actual),
		nil,
	)
}

func metadataString(metadata map[string]any, key string) (string, bool) {
	value, ok := metadata[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}
