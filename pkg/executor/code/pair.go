package code

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/loom"
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

// PairConfig carries dispatch context for one Strong-Style pair round.
type PairConfig struct {
	Loom                LoomClient
	ParentTaskID        string
	ProjectID           string
	RequestID           string
	TenantID            string
	CWD                 string
	DriverCLI           types.CLIName
	NavigatorCLI        types.CLIName
	DriverWorkerType    loom.WorkerType
	NavigatorWorkerType loom.WorkerType
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

	driverTaskID, err := cfg.Loom.Submit(ctx, loom.TaskRequest{
		WorkerType:   driverWorkerType(cfg),
		ProjectID:    cfg.ProjectID,
		RequestID:    cfg.RequestID,
		ParentTaskID: cfg.ParentTaskID,
		TenantID:     cfg.TenantID,
		Prompt:       prompt,
		CWD:          cfg.CWD,
		CLI:          cfg.DriverCLI,
		Role:         "driver",
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

	navigatorTaskID, err := cfg.Loom.Submit(ctx, loom.TaskRequest{
		WorkerType:   navigatorWorkerType(cfg),
		ProjectID:    cfg.ProjectID,
		RequestID:    cfg.RequestID,
		ParentTaskID: cfg.ParentTaskID,
		TenantID:     cfg.TenantID,
		Prompt:       navigatorPrompt(prompt, diff, criteria),
		CWD:          cfg.CWD,
		CLI:          cfg.NavigatorCLI,
		Role:         "navigator",
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
	return verdict, nil
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

func navigatorPrompt(prompt string, diff string, criteria SuccessCriteria) string {
	var b strings.Builder
	b.WriteString("Original prompt:\n")
	b.WriteString(prompt)
	b.WriteString("\n\nDriver diff:\n")
	b.WriteString(diff)
	b.WriteString("\n\nSuccess criteria:\n")
	for _, criterion := range criteria.Criteria() {
		b.WriteString("- ")
		b.WriteString(criterion.Name)
		b.WriteString(" (weight ")
		b.WriteString(fmt.Sprintf("%.4f", criterion.Weight))
		b.WriteString("): ")
		b.WriteString(criterion.Description)
		b.WriteByte('\n')
	}
	b.WriteString("\nReturn JSON with verdict, confidence, diff, feedback, and evidence.")
	return b.String()
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
	return map[string]any{
		"worker_type":    string(driverWorkerType(cfg)),
		"round_role":     "driver",
		"driver_cli":     cfg.DriverCLI,
		"navigator_cli":  cfg.NavigatorCLI,
		"parent_task_id": cfg.ParentTaskID,
	}
}

func navigatorMetadata(cfg PairConfig) map[string]any {
	return map[string]any{
		"worker_type":    string(navigatorWorkerType(cfg)),
		"round_role":     "navigator",
		"driver_cli":     cfg.DriverCLI,
		"navigator_cli":  cfg.NavigatorCLI,
		"parent_task_id": cfg.ParentTaskID,
	}
}
