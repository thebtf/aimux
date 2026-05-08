package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/types"
)

const WorkerTypeReview loom.WorkerType = "review"

var _ loom.Worker = (*ReviewWorker)(nil)

// ReviewWorkerConfig holds ReviewWorker dependencies and defaults.
type ReviewWorkerConfig struct {
	Loom                  LoomClient
	PassRunner            PassRunner
	Criteria              Criteria
	DefaultTimeoutSeconds int
}

// ReviewWorker executes multi-pass review tasks.
type ReviewWorker struct {
	loom                  LoomClient
	runner                PassRunner
	criteria              Criteria
	defaultTimeoutSeconds int
}

// NewReviewWorker constructs a Loom review worker.
func NewReviewWorker(cfg ReviewWorkerConfig) (*ReviewWorker, error) {
	runner := cfg.PassRunner
	if runner == nil {
		if cfg.Loom == nil {
			return nil, types.NewCapabilityMismatch("review worker pass runner or Loom client is required", nil)
		}
		passes, err := NewPasses(cfg.Loom)
		if err != nil {
			return nil, err
		}
		runner = passes
	}

	timeout := cfg.DefaultTimeoutSeconds
	if timeout <= 0 {
		timeout = DefaultGateTimeoutSeconds
	}
	return &ReviewWorker{
		loom:                  cfg.Loom,
		runner:                runner,
		criteria:              cfg.Criteria,
		defaultTimeoutSeconds: timeout,
	}, nil
}

// Type implements loom.Worker.
func (w *ReviewWorker) Type() loom.WorkerType {
	return WorkerTypeReview
}

// Execute implements loom.Worker.
func (w *ReviewWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	return w.Run(ctx, task)
}

// Run dispatches review passes and records aggregate/gate metadata on the task.
func (w *ReviewWorker) Run(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	if task == nil {
		return nil, types.NewUserInputError("review worker task is nil", nil)
	}
	if w == nil || w.runner == nil {
		return nil, types.NewCapabilityMismatch("review worker pass runner is required", nil)
	}
	if err := w.validateResume(ctx, task); err != nil {
		return nil, err
	}

	target := reviewTarget(task)
	if target == "" {
		return nil, types.NewUserInputError("review target is required", nil)
	}
	criteria := w.criteriaForTask(task)
	if reviewGateEnabled(task.Metadata) {
		return w.runGate(ctx, task, target, criteria)
	}
	return w.runAggregate(ctx, task, target, criteria)
}

func (w *ReviewWorker) validateResume(ctx context.Context, task *loom.Task) error {
	resumeTaskID := reviewResumeTaskID(task.Metadata)
	if resumeTaskID == "" {
		return nil
	}
	if w.loom == nil {
		return types.NewCapabilityMismatch("review worker Loom client is required for resume validation", nil)
	}
	_, err := types.HydrateResumeMetadata(ctx, w.loom, resumeTaskID, WorkerTypeReview)
	return err
}

func (w *ReviewWorker) runGate(ctx context.Context, task *loom.Task, target string, criteria Criteria) (*loom.WorkerResult, error) {
	decision, err := NewGate(w.runner, criteria).RunGate(ctx, target, timeoutSeconds(task.Metadata, task.Timeout, w.defaultTimeoutSeconds))
	if err != nil {
		return nil, err
	}
	metadata := reviewMetadata(task.Metadata, target, "gate", decision.PassesCompleted, decision.Severity, decision.Blocking)
	metadata["decision"] = string(decision.Decision)
	metadata["reason"] = decision.Reason
	task.Metadata = metadata

	content, err := marshalWorkerContent(decision)
	if err != nil {
		return nil, err
	}
	return &loom.WorkerResult{
		Content:  content,
		Metadata: cloneWorkerMetadata(metadata),
	}, nil
}

func (w *ReviewWorker) runAggregate(ctx context.Context, task *loom.Task, target string, criteria Criteria) (*loom.WorkerResult, error) {
	results, err := w.runner.Run(ctx, target, criteria)
	if err != nil {
		return nil, err
	}
	aggregated := Aggregator{}.Aggregate(results)
	decision := allowDecision(aggregated)
	if aggregated.Blocking {
		decision = blockDecision(aggregated)
	}

	metadata := reviewMetadata(task.Metadata, target, "aggregate", aggregated.PassesCompleted, aggregated.Severity, aggregated.Blocking)
	metadata["decision"] = string(decision.Decision)
	metadata["reason"] = decision.Reason
	task.Metadata = metadata

	content, err := marshalWorkerContent(aggregated)
	if err != nil {
		return nil, err
	}
	return &loom.WorkerResult{
		Content:  content,
		Metadata: cloneWorkerMetadata(metadata),
	}, nil
}

func (w *ReviewWorker) criteriaForTask(task *loom.Task) Criteria {
	criteria := w.criteria
	criteria.ParentTaskID = task.ID
	criteria.ProjectID = task.ProjectID
	criteria.RequestID = task.RequestID
	criteria.TenantID = task.TenantID
	criteria.CWD = task.CWD
	if task.CLI != "" {
		criteria.CLI = types.CLIName(task.CLI)
	}
	criteria.Model = task.Model
	criteria.Effort = task.Effort
	if task.Timeout > 0 {
		criteria.TaskTimeout = time.Duration(task.Timeout) * time.Second
	}
	return criteria
}

func reviewMetadata(base map[string]any, target string, subMode string, passes []PassName, severity Severity, blocking bool) map[string]any {
	metadata := cloneWorkerMetadata(base)
	metadata["worker_type"] = string(WorkerTypeReview)
	metadata["review_target"] = target
	metadata["review_gate"] = subMode == "gate"
	metadata["review_sub_mode"] = subMode
	metadata["passes_completed"] = passNamesForMetadata(passes)
	metadata["severity"] = string(severity)
	metadata["blocking"] = blocking
	return metadata
}

func passNamesForMetadata(passes []PassName) []string {
	names := make([]string, 0, len(passes))
	for _, pass := range passes {
		if pass != "" {
			names = append(names, string(pass))
		}
	}
	return names
}

func reviewTarget(task *loom.Task) string {
	for _, key := range []string{"target", "review_target"} {
		if value, ok := metadataString(task.Metadata, key); ok {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(task.Prompt)
}

func reviewGateEnabled(metadata map[string]any) bool {
	for _, key := range []string{"gate", "review_gate"} {
		if value, ok := metadataBool(metadata, key); ok {
			return value
		}
	}
	return false
}

func timeoutSeconds(metadata map[string]any, taskTimeout int, defaultTimeout int) int {
	if value, ok := metadataInt(metadata, "timeout_seconds"); ok && value > 0 {
		return value
	}
	if taskTimeout > 0 {
		return taskTimeout
	}
	return defaultTimeout
}

func metadataString(metadata map[string]any, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return "", false
	}
	return fmt.Sprint(value), true
}

func reviewResumeTaskID(metadata map[string]any) string {
	for _, key := range []string{"resume_id", types.MetadataResumeTaskID} {
		if value, ok := metadataString(metadata, key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func metadataBool(metadata map[string]any, key string) (bool, bool) {
	if metadata == nil {
		return false, false
	}
	switch value := metadata[key].(type) {
	case bool:
		return value, true
	case string:
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "true" {
			return true, true
		}
		if normalized == "false" {
			return false, true
		}
	}
	return false, false
}

func metadataInt(metadata map[string]any, key string) (int, bool) {
	if metadata == nil {
		return 0, false
	}
	switch value := metadata[key].(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	}
	return 0, false
}

func marshalWorkerContent(value any) (string, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return "", types.NewUnknown("marshal review worker result: "+err.Error(), err)
	}
	return string(content), nil
}

func cloneWorkerMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	next := make(map[string]any, len(metadata))
	for key, value := range metadata {
		next[key] = value
	}
	return next
}
