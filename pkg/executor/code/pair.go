package code

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/code/prompts"
	"github.com/thebtf/aimux/pkg/executor/types"
)

const (
	WorkerTypeCodeDriver    loom.WorkerType = "code_driver"
	WorkerTypeCodeNavigator loom.WorkerType = "code_navigator"

	DefaultPairTaskTimeout  = 5 * time.Minute
	DefaultPairPollInterval = 25 * time.Millisecond
)

// LoomClient is the subset of Loom used by pair dispatch.
type LoomClient interface {
	Submit(ctx context.Context, req loom.TaskRequest) (string, error)
	Get(taskID string) (*loom.Task, error)
}

type taskCanceler interface {
	Cancel(taskID string) error
}

// PairConfig carries dispatch context for one Strong-Style pair round.
type PairConfig struct {
	Loom                LoomClient
	ParentTaskID        string
	ProjectID           string
	RequestID           string
	TenantID            string
	CWD                 string
	Env                 map[string]string
	ResumeMetadata      map[string]any
	DriverCLI           types.CLIName
	NavigatorCLI        types.CLIName
	DriverWorkerType    loom.WorkerType
	NavigatorWorkerType loom.WorkerType
	Model               string
	Effort              string
	Sandbox             string
	TaskTimeout         time.Duration
	PollInterval        time.Duration
}

// Verdict is the navigator's decision for a driver diff.
type Verdict struct {
	Action          State
	Confidence      float64
	Diff            string
	Feedback        string
	Evidence        string
	DriverTaskID    string
	NavigatorTaskID string
	ThreadID        string
}

type navigatorVerdict struct {
	Verdict    string  `json:"verdict"`
	Action     string  `json:"action"`
	Confidence float64 `json:"confidence"`
	Diff       string  `json:"diff"`
	Feedback   string  `json:"feedback"`
	Evidence   string  `json:"evidence"`
}

// RunRound runs one driver/navigator pair round through Loom sub-tasks.
func RunRound(ctx context.Context, prompt string, criteria SuccessCriteria, cfg PairConfig) (Verdict, error) {
	if err := validatePairConfig(cfg); err != nil {
		return Verdict{}, err
	}

	driverPrompt, err := renderDriverPrompt(prompt, criteria, cfg)
	if err != nil {
		return Verdict{}, err
	}
	driverTaskID, err := cfg.Loom.Submit(ctx, loom.TaskRequest{
		WorkerType:   driverWorkerType(cfg),
		ProjectID:    cfg.ProjectID,
		RequestID:    cfg.RequestID,
		ParentTaskID: cfg.ParentTaskID,
		TenantID:     cfg.TenantID,
		Prompt:       driverPrompt,
		CWD:          cfg.CWD,
		Env:          cloneEnv(cfg.Env),
		CLI:          cfg.DriverCLI,
		Role:         "driver",
		Model:        cfg.Model,
		Effort:       cfg.Effort,
		Metadata:     driverMetadata(cfg),
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("submit driver sub-task: %w", err)
	}

	driverTask, err := waitForTask(ctx, cfg, driverTaskID)
	if err != nil {
		return Verdict{}, fmt.Errorf("driver sub-task: %w", err)
	}
	diff := strings.TrimSpace(driverTask.Result)
	if diff == "" {
		return Verdict{}, types.NewUserInputError("driver sub-task returned empty diff", nil)
	}

	navPrompt, err := renderNavigatorPrompt(prompt, diff, criteria, cfg)
	if err != nil {
		return Verdict{}, err
	}
	navigatorTaskID, err := cfg.Loom.Submit(ctx, loom.TaskRequest{
		WorkerType:   navigatorWorkerType(cfg),
		ProjectID:    cfg.ProjectID,
		RequestID:    cfg.RequestID,
		ParentTaskID: cfg.ParentTaskID,
		TenantID:     cfg.TenantID,
		Prompt:       navPrompt,
		CWD:          cfg.CWD,
		Env:          cloneEnv(cfg.Env),
		CLI:          cfg.NavigatorCLI,
		Role:         "navigator",
		Model:        cfg.Model,
		Effort:       cfg.Effort,
		Metadata:     navigatorMetadata(cfg),
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("submit navigator sub-task: %w", err)
	}

	navigatorTask, err := waitForTask(ctx, cfg, navigatorTaskID)
	if err != nil {
		return Verdict{}, fmt.Errorf("navigator sub-task: %w", err)
	}

	verdict, err := parseNavigatorVerdict(navigatorTask.Result, diff)
	if err != nil {
		return Verdict{}, err
	}
	verdict.DriverTaskID = driverTaskID
	verdict.NavigatorTaskID = navigatorTaskID
	verdict.ThreadID = taskThreadID(driverTask)
	return verdict, nil
}

// SoloResult is the output of a solo driver round (no navigator).
type SoloResult struct {
	Content  string
	TaskID   string
	ThreadID string
}

// RunSoloRound runs one driver-only round with write access.
func RunSoloRound(ctx context.Context, prompt string, cfg PairConfig) (SoloResult, error) {
	if cfg.Loom == nil {
		return SoloResult{}, types.NewCapabilityMismatch("solo round Loom client is required", nil)
	}
	if cfg.DriverCLI == "" {
		return SoloResult{}, types.NewUserInputError("solo round driver CLI is required", nil)
	}

	soloPrompt, err := renderSoloDriverPrompt(prompt, cfg)
	if err != nil {
		return SoloResult{}, err
	}
	metadata := driverMetadata(cfg)
	metadata["solo_mode"] = true
	metadata["sandbox"] = "workspace-write"

	taskID, err := cfg.Loom.Submit(ctx, loom.TaskRequest{
		WorkerType:   driverWorkerType(cfg),
		ProjectID:    cfg.ProjectID,
		RequestID:    cfg.RequestID,
		ParentTaskID: cfg.ParentTaskID,
		TenantID:     cfg.TenantID,
		Prompt:       soloPrompt,
		CWD:          cfg.CWD,
		Env:          cloneEnv(cfg.Env),
		CLI:          cfg.DriverCLI,
		Role:         "solo",
		Model:        cfg.Model,
		Effort:       cfg.Effort,
		Metadata:     metadata,
	})
	if err != nil {
		return SoloResult{}, fmt.Errorf("submit solo driver sub-task: %w", err)
	}

	task, err := waitForTask(ctx, cfg, taskID)
	if err != nil {
		return SoloResult{}, fmt.Errorf("solo driver sub-task: %w", err)
	}
	content := strings.TrimSpace(task.Result)
	if content == "" {
		return SoloResult{}, types.NewUserInputError("solo driver sub-task returned empty output", nil)
	}
	return SoloResult{
		Content:  content,
		TaskID:   taskID,
		ThreadID: taskThreadID(task),
	}, nil
}

// RunSoloDiffRound runs one driver-only round that returns a unified diff (read-only).
func RunSoloDiffRound(ctx context.Context, prompt string, cfg PairConfig) (SoloResult, error) {
	if cfg.Loom == nil {
		return SoloResult{}, types.NewCapabilityMismatch("solo diff round Loom client is required", nil)
	}
	if cfg.DriverCLI == "" {
		return SoloResult{}, types.NewUserInputError("solo diff round driver CLI is required", nil)
	}

	criteria := prompts.RenderData{
		Prompt:         prompt,
		ProjectContext: pairProjectContext(cfg),
	}
	diffPrompt, err := prompts.RenderDriver(criteria)
	if err != nil {
		return SoloResult{}, err
	}
	metadata := driverMetadata(cfg)
	metadata["solo_mode"] = true

	taskID, err := cfg.Loom.Submit(ctx, loom.TaskRequest{
		WorkerType:   driverWorkerType(cfg),
		ProjectID:    cfg.ProjectID,
		RequestID:    cfg.RequestID,
		ParentTaskID: cfg.ParentTaskID,
		TenantID:     cfg.TenantID,
		Prompt:       diffPrompt,
		CWD:          cfg.CWD,
		Env:          cloneEnv(cfg.Env),
		CLI:          cfg.DriverCLI,
		Role:         "solo-diff",
		Model:        cfg.Model,
		Effort:       cfg.Effort,
		Metadata:     metadata,
	})
	if err != nil {
		return SoloResult{}, fmt.Errorf("submit solo diff driver sub-task: %w", err)
	}

	task, err := waitForTask(ctx, cfg, taskID)
	if err != nil {
		return SoloResult{}, fmt.Errorf("solo diff driver sub-task: %w", err)
	}
	content := strings.TrimSpace(task.Result)
	if content == "" {
		return SoloResult{}, types.NewUserInputError("solo diff driver returned empty output", nil)
	}
	return SoloResult{
		Content:  content,
		TaskID:   taskID,
		ThreadID: taskThreadID(task),
	}, nil
}

func renderSoloDriverPrompt(prompt string, cfg PairConfig) (string, error) {
	return prompts.RenderDriverSolo(prompts.RenderData{
		Prompt:         prompt,
		ProjectContext: pairProjectContext(cfg),
	})
}

func validatePairConfig(cfg PairConfig) error {
	if cfg.Loom == nil {
		return types.NewCapabilityMismatch("code pair Loom client is required", nil)
	}
	if cfg.ParentTaskID == "" {
		return types.NewUserInputError("code pair ParentTaskID is required", nil)
	}
	if cfg.DriverCLI == "" {
		return types.NewUserInputError("code pair driver CLI is required", nil)
	}
	if cfg.NavigatorCLI == "" {
		return types.NewUserInputError("code pair navigator CLI is required", nil)
	}
	return nil
}

func waitForTask(ctx context.Context, cfg PairConfig, taskID string) (*loom.Task, error) {
	timeout := cfg.TaskTimeout
	if timeout <= 0 {
		timeout = DefaultPairTaskTimeout
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = DefaultPairPollInterval
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-waitCtx.Done():
			cancelTaskIfSupported(cfg.Loom, taskID)
			return nil, waitCtx.Err()
		case <-timer.C:
			task, err := cfg.Loom.Get(taskID)
			if err != nil {
				return nil, err
			}
			if task.Status.IsTerminal() {
				if task.Status != loom.TaskStatusCompleted {
					if task.Error != "" {
						return nil, fmt.Errorf("%s", task.Error)
					}
					return nil, fmt.Errorf("task %s ended with status %s", taskID, task.Status)
				}
				return task, nil
			}
			timer.Reset(pollInterval)
		}
	}
}

func cancelTaskIfSupported(client LoomClient, taskID string) {
	canceler, ok := client.(taskCanceler)
	if !ok {
		return
	}
	_ = canceler.Cancel(taskID) // best-effort cleanup; preserve the wait failure as the primary error.
}

func parseNavigatorVerdict(output string, driverDiff string) (Verdict, error) {
	var raw navigatorVerdict
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return Verdict{}, types.NewUserInputError("navigator verdict must be JSON", err)
	}
	actionText := strings.ToUpper(strings.TrimSpace(raw.Verdict))
	if actionText == "" {
		actionText = strings.ToUpper(strings.TrimSpace(raw.Action))
	}

	verdict := Verdict{
		Action:     State(actionText),
		Confidence: raw.Confidence,
		Diff:       strings.TrimSpace(raw.Diff),
		Feedback:   raw.Feedback,
		Evidence:   raw.Evidence,
	}
	if verdict.Diff == "" && verdict.Action == StateApply {
		verdict.Diff = driverDiff
	}
	if verdict.Confidence < 0 || verdict.Confidence > 1 {
		return Verdict{}, types.NewUserInputError("navigator confidence must be between 0 and 1", nil)
	}
	switch verdict.Action {
	case StateApply, StateRevise, StateRetry, StateEscalate:
		return verdict, nil
	default:
		return Verdict{}, types.NewUserInputError(fmt.Sprintf("unsupported navigator verdict %q", actionText), nil)
	}
}

func taskThreadID(task *loom.Task) string {
	if task == nil {
		return ""
	}
	if threadID, ok := metadataString(task.Metadata, MetadataThreadID); ok && strings.TrimSpace(threadID) != "" {
		return strings.TrimSpace(threadID)
	}
	if sessionID, ok := metadataString(task.Metadata, "cli_session_id"); ok {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

func renderDriverPrompt(prompt string, criteria SuccessCriteria, cfg PairConfig) (string, error) {
	return prompts.RenderDriver(prompts.RenderData{
		Prompt:         prompt,
		ProjectContext: pairProjectContext(cfg),
		CriteriaList:   criteriaPromptViews(criteria),
	})
}

func renderNavigatorPrompt(prompt string, diff string, criteria SuccessCriteria, cfg PairConfig) (string, error) {
	return prompts.RenderNavigator(prompts.RenderData{
		Prompt:         prompt,
		ProjectContext: pairProjectContext(cfg),
		CriteriaList:   criteriaPromptViews(criteria),
		Diff:           diff,
	})
}

func criteriaPromptViews(criteria SuccessCriteria) []prompts.CriterionView {
	active := criteria.NormalizeWeights().Criteria()
	views := make([]prompts.CriterionView, 0, len(active))
	for _, criterion := range active {
		views = append(views, prompts.CriterionView{
			Name:        criterion.Name,
			Description: criterion.Description,
			Weight:      criterion.Weight,
		})
	}
	return views
}

func pairProjectContext(cfg PairConfig) string {
	return fmt.Sprintf("CWD=%s\nProjectID=%s\nParentTaskID=%s\nDriverCLI=%s\nNavigatorCLI=%s",
		cfg.CWD, cfg.ProjectID, cfg.ParentTaskID, cfg.DriverCLI, cfg.NavigatorCLI)
}

func driverWorkerType(cfg PairConfig) loom.WorkerType {
	if cfg.DriverWorkerType != "" {
		return cfg.DriverWorkerType
	}
	return WorkerTypeCodeDriver
}

func navigatorWorkerType(cfg PairConfig) loom.WorkerType {
	if cfg.NavigatorWorkerType != "" {
		return cfg.NavigatorWorkerType
	}
	return WorkerTypeCodeNavigator
}

func driverMetadata(cfg PairConfig) map[string]any {
	metadata := map[string]any{
		"worker_type":    string(driverWorkerType(cfg)),
		"round_role":     "driver",
		"driver_cli":     cfg.DriverCLI,
		"navigator_cli":  cfg.NavigatorCLI,
		"parent_task_id": cfg.ParentTaskID,
		"sandbox":        "read-only",
	}
	for _, key := range []string{MetadataThreadID, MetadataResumeTaskID, "resume_id"} {
		if value, ok := cfg.ResumeMetadata[key]; ok {
			metadata[key] = value
		}
	}
	return metadata
}

func navigatorMetadata(cfg PairConfig) map[string]any {
	metadata := map[string]any{
		"worker_type":    string(navigatorWorkerType(cfg)),
		"round_role":     "navigator",
		"driver_cli":     cfg.DriverCLI,
		"navigator_cli":  cfg.NavigatorCLI,
		"parent_task_id": cfg.ParentTaskID,
	}
	if cfg.Sandbox != "" {
		metadata["sandbox"] = cfg.Sandbox
	}
	return metadata
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
