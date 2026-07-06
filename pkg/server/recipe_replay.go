package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/server/classifier"
)

const (
	recipeReplayKeyVersion = "v1"

	recipeReplayKeyVersionMetadata  = "recipe_replay_key_version"
	recipeReplayFingerprintMetadata = "recipe_replay_fingerprint"
	recipeReplayCacheHitMetadata    = "recipe_replay_cache_hit"
	recipeReplaySourceTaskMetadata  = "recipe_replay_source_task_id"
)

type recipeReplayTaskLister interface {
	List(projectID string, statuses ...loom.TaskStatus) ([]*loom.Task, error)
}

type recipeReplayFingerprintInput struct {
	KeyVersion      string   `json:"key_version"`
	RecipeID        string   `json:"recipe_id"`
	Prompt          string   `json:"prompt"`
	Target          string   `json:"target"`
	CWD             string   `json:"cwd"`
	TaskClass       string   `json:"task_class"`
	WorkerType      string   `json:"worker_type"`
	Gate            bool     `json:"gate"`
	SelectedCLI     string   `json:"selected_cli"`
	RequestedPolicy []string `json:"requested_policy"`
	SupportedPolicy []string `json:"supported_policy"`
	Model           string   `json:"model,omitempty"`
	Role            string   `json:"role,omitempty"`
	Effort          string   `json:"effort,omitempty"`
}

func attachRecipeReplayMetadata(req *loom.TaskRequest) (bool, error) {
	if req == nil {
		return false, nil
	}
	if !recipeReplayEligible(req.Metadata) {
		return false, nil
	}
	fingerprint, err := recipeReplayFingerprint(*req)
	if err != nil {
		return false, err
	}
	metadata := cloneTaskMetadata(req.Metadata)
	metadata[recipeReplayKeyVersionMetadata] = recipeReplayKeyVersion
	metadata[recipeReplayFingerprintMetadata] = fingerprint
	metadata[recipeReplayCacheHitMetadata] = false
	req.Metadata = metadata
	return true, nil
}

func recipeReplayEligible(metadata map[string]any) bool {
	recipeID, ok := metadataString(metadata, "recipe_id")
	if !ok || recipeID == "" {
		return false
	}
	readOnly, ok := metadataBool(metadata, "recipe_read_only")
	if !ok || !readOnly {
		return false
	}
	enforced, ok := metadataBool(metadata, recipePolicyEnforcedMetadataKey)
	return ok && enforced
}

func recipeReplayFingerprint(req loom.TaskRequest) (string, error) {
	recipeID, _ := metadataString(req.Metadata, "recipe_id")
	taskClass, _ := metadataString(req.Metadata, "task_class")
	target, _ := metadataString(req.Metadata, "target")
	selectedCLI, _ := metadataString(req.Metadata, recipePolicySelectedCLIMetadata)
	gate, _ := metadataBool(req.Metadata, "gate")

	input := recipeReplayFingerprintInput{
		KeyVersion:      recipeReplayKeyVersion,
		RecipeID:        recipeID,
		Prompt:          req.Prompt,
		Target:          target,
		CWD:             req.CWD,
		TaskClass:       taskClass,
		WorkerType:      string(req.WorkerType),
		Gate:            gate,
		SelectedCLI:     selectedCLI,
		RequestedPolicy: sortedMetadataStrings(req.Metadata, recipePolicyRequestedMetadata),
		SupportedPolicy: sortedMetadataStrings(req.Metadata, recipePolicySupportedMetadata),
		Model:           req.Model,
		Role:            req.Role,
		Effort:          req.Effort,
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("recipe replay fingerprint: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (r *TaskRouter) findRecipeReplayTask(req loom.TaskRequest) (*loom.Task, bool, error) {
	fingerprint, ok := metadataString(req.Metadata, recipeReplayFingerprintMetadata)
	if !ok || fingerprint == "" {
		return nil, false, nil
	}
	lister, ok := r.loom.(recipeReplayTaskLister)
	if !ok {
		return nil, false, nil
	}
	tasks, err := lister.List(req.ProjectID, loom.TaskStatusCompleted)
	if err != nil {
		return nil, false, fmt.Errorf("recipe replay: list completed tasks: %w", err)
	}
	for i := len(tasks) - 1; i >= 0; i-- {
		task := tasks[i]
		if task == nil || task.ID == "" {
			continue
		}
		if task.WorkerType != req.WorkerType || task.ProjectID != req.ProjectID || task.CWD != req.CWD {
			continue
		}
		if task.Prompt != req.Prompt || task.Model != req.Model || task.Role != req.Role || task.Effort != req.Effort {
			continue
		}
		taskFingerprint, ok := metadataString(task.Metadata, recipeReplayFingerprintMetadata)
		if !ok || taskFingerprint != fingerprint {
			continue
		}
		keyVersion, ok := metadataString(task.Metadata, recipeReplayKeyVersionMetadata)
		if !ok || keyVersion != recipeReplayKeyVersion {
			continue
		}
		if !recipeReplayWorkflowResultSuccessful(task.Metadata) {
			continue
		}
		return task, true, nil
	}
	return nil, false, nil
}

func recipeReplayWorkflowResultSuccessful(metadata map[string]any) bool {
	status, ok := metadataString(metadata, "workflow_result_status")
	return !ok || strings.TrimSpace(status) == "" || strings.TrimSpace(status) == "completed"
}

func (r *TaskRouter) recipeReplayResult(task *loom.Task, taskClass string, confidence float64, candidates []classifier.Candidate) (TaskResult, error) {
	metadata := cloneTaskMetadata(task.Metadata)
	metadata[recipeReplayCacheHitMetadata] = true
	metadata[recipeReplaySourceTaskMetadata] = task.ID
	task.Metadata = metadata
	return buildTaskResult(task, taskClass, confidence, candidates), nil
}

func metadataBool(metadata map[string]any, key string) (bool, bool) {
	if metadata == nil {
		return false, false
	}
	value, ok := metadata[key]
	if !ok {
		return false, false
	}
	got, ok := value.(bool)
	return got, ok
}

func sortedMetadataStrings(metadata map[string]any, key string) []string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return nil
	}
	var out []string
	switch typed := value.(type) {
	case []string:
		out = append(out, typed...)
	case []any:
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}
