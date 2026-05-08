package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/code"
	"github.com/thebtf/aimux/pkg/executor/review"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/server/classifier"
)

const (
	defaultTaskRouterWaitTimeout  = 5 * time.Minute
	defaultTaskRouterPollInterval = 50 * time.Millisecond
	taskClassTask                 = "task"
	taskClassThink                = "think"
)

// TaskRouterLoom is the Loom surface used by TaskRouter.
type TaskRouterLoom interface {
	Submit(ctx context.Context, req loom.TaskRequest) (string, error)
	GetContext(ctx context.Context, taskID string) (*loom.Task, error)
	Cancel(taskID string) error
}

// TaskClassifier resolves prompts into ranked task_class candidates.
type TaskClassifier interface {
	Classify(prompt string) ([]classifier.Candidate, float64, error)
}

// TaskRouterConfig wires TaskRouter dependencies and operational bounds.
type TaskRouterConfig struct {
	Loom         TaskRouterLoom
	Classifier   TaskClassifier
	Routes       map[string]loom.WorkerType
	WaitTimeout  time.Duration
	PollInterval time.Duration
}

// TaskRequest is the canonical server-level request accepted by TaskRouter.
type TaskRequest struct {
	Prompt         string
	TaskClass      string
	ProjectID      string
	RequestID      string
	ParentTaskID   string
	TenantID       string
	CWD            string
	Env            map[string]string
	CLI            string
	Role           string
	Model          string
	Effort         string
	TimeoutSeconds int
	ResumeID       string
	Target         string
	Gate           bool
	Metadata       map[string]any
}

// TaskResult is the synchronous result returned by TaskRouter.Dispatch.
type TaskResult struct {
	TaskID          string                 `json:"task_id"`
	Content         string                 `json:"content"`
	CLI             string                 `json:"cli,omitempty"`
	TaskClass       string                 `json:"task_class"`
	WorkerType      loom.WorkerType        `json:"worker_type"`
	Status          loom.TaskStatus        `json:"status"`
	Rounds          int                    `json:"rounds,omitempty"`
	ConfidenceScore float64                `json:"confidence_score"`
	Metadata        map[string]any         `json:"metadata,omitempty"`
	Candidates      []classifier.Candidate `json:"candidates,omitempty"`
}

// TaskRouter dispatches task requests through Loom.
type TaskRouter struct {
	loom         TaskRouterLoom
	classifier   TaskClassifier
	routes       map[string]loom.WorkerType
	waitTimeout  time.Duration
	pollInterval time.Duration
}

// NewTaskRouter constructs a TaskRouter.
func NewTaskRouter(cfg TaskRouterConfig) (*TaskRouter, error) {
	if cfg.Loom == nil {
		return nil, fmt.Errorf("task router: loom is required")
	}
	taskClassifier := cfg.Classifier
	if taskClassifier == nil {
		taskClassifier = classifier.New()
	}
	routes := cloneRoutes(cfg.Routes)
	if len(routes) == 0 {
		routes = DefaultTaskRoutes()
	}
	waitTimeout := cfg.WaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = defaultTaskRouterWaitTimeout
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultTaskRouterPollInterval
	}
	return &TaskRouter{
		loom:         cfg.Loom,
		classifier:   taskClassifier,
		routes:       routes,
		waitTimeout:  waitTimeout,
		pollInterval: pollInterval,
	}, nil
}

// DefaultTaskRoutes maps task_class values to Loom worker types.
func DefaultTaskRoutes() map[string]loom.WorkerType {
	return map[string]loom.WorkerType{
		classifier.TaskClassCode:   code.WorkerTypeCode,
		classifier.TaskClassReview: review.WorkerTypeReview,
		taskClassThink:             loom.WorkerTypeThinker,
	}
}

// Dispatch resolves task_class, submits a Loom task, and waits for terminal status.
func (r *TaskRouter) Dispatch(ctx context.Context, req TaskRequest) (TaskResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return TaskResult{}, cliErrorFromContext("task router dispatch canceled", err)
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return TaskResult{}, extypes.NewUserInputError("task prompt is required", nil)
	}

	resolvedClass, confidence, candidates, err := r.resolveTaskClass(prompt, req.TaskClass)
	if err != nil {
		return TaskResult{
			TaskClass:       taskClassTask,
			ConfidenceScore: confidence,
			Candidates:      cloneCandidates(candidates),
		}, err
	}

	workerType, ok := r.routes[resolvedClass]
	if !ok || workerType == "" {
		return TaskResult{TaskClass: resolvedClass, ConfidenceScore: confidence, Candidates: cloneCandidates(candidates)},
			extypes.NewUserInputError(fmt.Sprintf("unsupported task_class %q", resolvedClass), nil)
	}

	loomReq := canonicalLoomRequest(req, prompt, resolvedClass, workerType, confidence, candidates)
	taskID, err := r.loom.Submit(ctx, loomReq)
	if err != nil {
		return TaskResult{TaskClass: resolvedClass, WorkerType: workerType, ConfidenceScore: confidence, Candidates: cloneCandidates(candidates)}, ensureCLIError(err)
	}

	result, err := r.wait(ctx, taskID, resolvedClass, workerType, confidence, candidates)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (r *TaskRouter) resolveTaskClass(prompt string, taskClass string) (string, float64, []classifier.Candidate, error) {
	normalized := strings.ToLower(strings.TrimSpace(taskClass))
	if normalized == "" || normalized == taskClassTask {
		candidates, confidence, err := r.classifier.Classify(prompt)
		if err != nil {
			return "", confidence, candidates, err
		}
		routable := r.routableCandidates(candidates)
		if len(routable) == 0 {
			return "", 0, candidates, extypes.NewClassificationAmbiguous("classification ambiguous: no routable candidates", nil)
		}
		if routable[0].Score < classifier.DefaultThreshold {
			return "", routable[0].Score, routable, extypes.NewClassificationAmbiguous(routableAmbiguousMessage(routable), nil)
		}
		return routable[0].TaskClass, routable[0].Score, routable, nil
	}
	return normalized, 1, nil, nil
}

func (r *TaskRouter) routableCandidates(candidates []classifier.Candidate) []classifier.Candidate {
	routable := make([]classifier.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if workerType, ok := r.routes[candidate.TaskClass]; ok && workerType != "" {
			routable = append(routable, candidate)
		}
	}
	return routable
}

func routableAmbiguousMessage(candidates []classifier.Candidate) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range topTaskCandidates(candidates, 3) {
		parts = append(parts, fmt.Sprintf("%s=%.2f", candidate.TaskClass, candidate.Score))
	}
	return "classification ambiguous among routable classes; pass explicit task_class; top candidates: " + strings.Join(parts, ", ")
}

func topTaskCandidates(candidates []classifier.Candidate, n int) []classifier.Candidate {
	if n > len(candidates) {
		n = len(candidates)
	}
	return candidates[:n]
}

func canonicalLoomRequest(req TaskRequest, prompt string, taskClass string, workerType loom.WorkerType, confidence float64, candidates []classifier.Candidate) loom.TaskRequest {
	metadata := cloneTaskMetadata(req.Metadata)
	metadata["task_class"] = taskClass
	metadata["worker_type"] = string(workerType)
	metadata["classification_confidence"] = confidence
	if len(candidates) > 0 {
		metadata["classification_candidates"] = cloneCandidates(candidates)
	}
	if req.ResumeID != "" {
		metadata["resume_id"] = req.ResumeID
		metadata[extypes.MetadataResumeTaskID] = req.ResumeID
	}
	if req.Target != "" {
		metadata["target"] = req.Target
		metadata["review_target"] = req.Target
	}
	if req.Gate {
		metadata["gate"] = true
		metadata["review_gate"] = true
	}
	if req.CLI != "" {
		metadata["driver_cli_override"] = req.CLI
	}

	return loom.TaskRequest{
		WorkerType:   workerType,
		ProjectID:    req.ProjectID,
		RequestID:    req.RequestID,
		ParentTaskID: req.ParentTaskID,
		TenantID:     req.TenantID,
		Prompt:       prompt,
		CWD:          req.CWD,
		Env:          cloneEnv(req.Env),
		CLI:          req.CLI,
		Role:         req.Role,
		Model:        req.Model,
		Effort:       req.Effort,
		Timeout:      req.TimeoutSeconds,
		Metadata:     metadata,
	}
}

func (r *TaskRouter) wait(ctx context.Context, taskID string, taskClass string, workerType loom.WorkerType, confidence float64, candidates []classifier.Candidate) (TaskResult, error) {
	waitCtx, cancel := context.WithTimeout(ctx, r.waitTimeout)
	defer cancel()

	var lastTask *loom.Task
	for {
		task, err := r.loom.GetContext(waitCtx, taskID)
		if err != nil {
			if waitCtx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				_ = r.loom.Cancel(taskID)
				result := buildTaskResult(lastTask, taskClass, confidence, candidates)
				result.TaskID = taskID
				result.WorkerType = workerType
				return result, cliErrorFromContext("task router wait ended", contextError(waitCtx, err))
			}
			result := buildTaskResult(lastTask, taskClass, confidence, candidates)
			result.TaskID = taskID
			result.WorkerType = workerType
			return result, ensureCLIError(fmt.Errorf("task router: get task %q: %w", taskID, err))
		}
		lastTask = task
		if task.Status.IsTerminal() {
			result := buildTaskResult(task, taskClass, confidence, candidates)
			if task.Status == loom.TaskStatusFailed || task.Status == loom.TaskStatusFailedCrash {
				msg := strings.TrimSpace(task.Error)
				if msg == "" {
					msg = string(task.Status)
				}
				return result, extypes.NewUnknown("task router: task failed: "+msg, nil)
			}
			return result, nil
		}

		timer := time.NewTimer(r.pollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			_ = r.loom.Cancel(taskID)
			result := buildTaskResult(task, taskClass, confidence, candidates)
			return result, cliErrorFromContext("task router wait ended", waitCtx.Err())
		case <-timer.C:
		}
	}
}

func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return context.Canceled
}

func buildTaskResult(task *loom.Task, taskClass string, confidence float64, candidates []classifier.Candidate) TaskResult {
	if task == nil {
		return TaskResult{TaskClass: taskClass, ConfidenceScore: confidence, Candidates: cloneCandidates(candidates)}
	}
	metadata := cloneTaskMetadata(task.Metadata)
	if value, ok := metadataString(metadata, "task_class"); ok && value != "" {
		taskClass = value
	}
	if value, ok := metadataFloat(metadata, "confidence_score"); ok {
		confidence = value
	}
	rounds, _ := metadataInt(metadata, "rounds")
	return TaskResult{
		TaskID:          task.ID,
		Content:         task.Result,
		CLI:             task.CLI,
		TaskClass:       taskClass,
		WorkerType:      task.WorkerType,
		Status:          task.Status,
		Rounds:          rounds,
		ConfidenceScore: confidence,
		Metadata:        metadata,
		Candidates:      cloneCandidates(candidates),
	}
}

func cliErrorFromContext(message string, err error) *extypes.CLIError {
	if errors.Is(err, context.DeadlineExceeded) {
		return extypes.NewTimeout(message+": "+err.Error(), err)
	}
	return extypes.NewCanceled(message+": "+err.Error(), err)
}

func ensureCLIError(err error) error {
	if err == nil {
		return nil
	}
	var cliErr *extypes.CLIError
	if errors.As(err, &cliErr) {
		return err
	}
	return extypes.NewUnknown(err.Error(), err)
}

func cloneTaskMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func cloneEnv(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		out[key] = value
	}
	return out
}

func cloneCandidates(candidates []classifier.Candidate) []classifier.Candidate {
	if candidates == nil {
		return nil
	}
	out := make([]classifier.Candidate, len(candidates))
	copy(out, candidates)
	return out
}

func cloneRoutes(routes map[string]loom.WorkerType) map[string]loom.WorkerType {
	if routes == nil {
		return nil
	}
	out := make(map[string]loom.WorkerType, len(routes))
	for taskClass, workerType := range routes {
		out[strings.ToLower(strings.TrimSpace(taskClass))] = workerType
	}
	return out
}

func metadataString(metadata map[string]any, key string) (string, bool) {
	value, ok := metadata[key]
	if !ok || value == nil {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func metadataFloat(metadata map[string]any, key string) (float64, bool) {
	value, ok := metadata[key]
	if !ok || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func metadataInt(metadata map[string]any, key string) (int, bool) {
	value, ok := metadata[key]
	if !ok || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
