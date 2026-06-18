package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/loom"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/tools/deepresearch"
)

const deepResearchWorkerType loom.WorkerType = "deepresearch"

type loomProgressAppender interface {
	AppendProgress(taskID string, line string) error
}

type deepResearchWorker struct {
	progress loomProgressAppender
}

func (w deepResearchWorker) Type() loom.WorkerType {
	return deepResearchWorkerType
}

func (w deepResearchWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	if task == nil {
		return nil, extypes.NewUserInputError("deepresearch worker task is nil", nil)
	}
	metadata := cloneTaskMetadata(task.Metadata)
	topic := strings.TrimSpace(task.Prompt)
	if value, ok := metadataString(metadata, "topic"); ok && strings.TrimSpace(value) != "" {
		topic = strings.TrimSpace(value)
	}
	if topic == "" {
		return nil, extypes.NewUserInputError("deepresearch topic is required", nil)
	}
	outputFormat, _ := metadataString(metadata, "output_format")
	model, _ := metadataString(metadata, "model")
	force, _ := metadataBool(metadata, "force")

	w.appendProgress(task.ID, "deepresearch: initializing provider client")
	client, clientErr := deepresearch.NewClient(model, task.Timeout)
	if clientErr != nil {
		return nil, extypes.NewCapabilityMismatch(
			fmt.Sprintf("DeepResearch unavailable: %v. Set GOOGLE_API_KEY or GEMINI_API_KEY.", clientErr),
			clientErr,
		)
	}
	defer client.Close()

	w.appendProgress(task.ID, "deepresearch: provider request started")
	content, cacheHit, researchErr := client.Research(ctx, topic, outputFormat, nil, force)
	if researchErr != nil {
		return nil, fmt.Errorf("DeepResearch failed: %w", researchErr)
	}

	if !cacheHit {
		w.appendProgress(task.ID, "deepresearch: persisting disk cache entry")
		if err := deepresearch.SaveEntryToDisk(task.CWD, topic, outputFormat, model, nil, content); err != nil {
			w.appendProgress(task.ID, "deepresearch: disk cache persistence skipped: "+err.Error())
		}
	}
	w.appendProgress(task.ID, "deepresearch: completed")

	metadata["worker_type"] = string(deepResearchWorkerType)
	metadata["cache_hit"] = cacheHit
	return &loom.WorkerResult{
		Content:  content,
		Metadata: metadata,
	}, nil
}

func (w deepResearchWorker) appendProgress(taskID string, line string) {
	if w.progress == nil || taskID == "" || strings.TrimSpace(line) == "" {
		return
	}
	_ = w.progress.AppendProgress(taskID, line)
}
