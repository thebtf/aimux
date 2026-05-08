// Package review implements the multi-pass review executor.
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

const (
	WorkerTypeReviewStructural  loom.WorkerType = "review_structural"
	WorkerTypeReviewBehavioural loom.WorkerType = "review_behavioural"
	WorkerTypeReviewAdversarial loom.WorkerType = "review_adversarial"

	DefaultPassTaskTimeout  = 5 * time.Minute
	DefaultPassPollInterval = 25 * time.Millisecond
)

// PassName identifies one review pass in the fixed multi-pass pipeline.
type PassName string

const (
	PassStructural  PassName = "structural"
	PassBehavioural PassName = "behavioural"
	PassAdversarial PassName = "adversarial"
)

// Severity classifies a review finding.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding is one structured review finding.
type Finding struct {
	Severity Severity `json:"severity"`
	File     string   `json:"file,omitempty"`
	Line     *int     `json:"line,omitempty"`
	Body     string   `json:"body"`
}

// PassResult is the structured output of one review pass.
type PassResult struct {
	Name       PassName  `json:"name"`
	Findings   []Finding `json:"findings"`
	Summary    string    `json:"summary"`
	LatencyMS  int64     `json:"latency_ms"`
	TaskID     string    `json:"task_id"`
	WorkerType string    `json:"worker_type"`
}

// Criteria carries dispatch context and execution bounds for review passes.
type Criteria struct {
	ParentTaskID string
	ProjectID    string
	RequestID    string
	TenantID     string
	CWD          string
	CLI          types.CLIName
	Model        string
	Effort       string
	WorkerTypes  map[PassName]loom.WorkerType
	TaskTimeout  time.Duration
	PollInterval time.Duration
}

// LoomClient is the subset of Loom used by review pass orchestration.
type LoomClient interface {
	Submit(ctx context.Context, req loom.TaskRequest) (string, error)
	Get(taskID string) (*loom.Task, error)
}

type taskCanceler interface {
	Cancel(taskID string) error
}

// Passes runs the fixed structural -> behavioural -> adversarial review pipeline.
type Passes struct {
	loom LoomClient
}

type passResponse struct {
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
}

// NewPasses constructs a multi-pass review runner.
func NewPasses(client LoomClient) (*Passes, error) {
	if client == nil {
		return nil, types.NewCapabilityMismatch("review passes Loom client is required", nil)
	}
	return &Passes{loom: client}, nil
}

// Run executes structural, behavioural, and adversarial passes sequentially.
func (p *Passes) Run(ctx context.Context, target string, criteria Criteria) ([]PassResult, error) {
	if p == nil || p.loom == nil {
		return nil, types.NewCapabilityMismatch("review passes Loom client is required", nil)
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, types.NewUserInputError("review target is required", nil)
	}
	if strings.TrimSpace(criteria.ParentTaskID) == "" {
		return nil, types.NewUserInputError("review passes ParentTaskID is required", nil)
	}

	passes := orderedPasses()
	results := make([]PassResult, 0, len(passes))
	for _, pass := range passes {
		result, err := p.runOne(ctx, pass, target, criteria)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (p *Passes) runOne(ctx context.Context, pass PassName, target string, criteria Criteria) (PassResult, error) {
	start := time.Now()
	workerType := workerTypeForPass(criteria, pass)
	taskID, err := p.loom.Submit(ctx, loom.TaskRequest{
		WorkerType:   workerType,
		ProjectID:    criteria.ProjectID,
		RequestID:    criteria.RequestID,
		ParentTaskID: criteria.ParentTaskID,
		TenantID:     criteria.TenantID,
		Prompt:       buildPassPrompt(pass, target),
		CWD:          criteria.CWD,
		CLI:          defaultReviewCLI(criteria),
		Role:         string(pass),
		Model:        criteria.Model,
		Effort:       criteria.Effort,
		Metadata:     passMetadata(pass, workerType, criteria),
	})
	if err != nil {
		return PassResult{}, fmt.Errorf("submit %s review pass: %w", pass, err)
	}

	task, err := waitForTask(ctx, p.loom, taskID, criteria)
	if err != nil {
		return PassResult{}, fmt.Errorf("%s review pass: %w", pass, err)
	}
	result, err := parsePassResult(pass, workerType, taskID, task.Result, time.Since(start))
	if err != nil {
		return PassResult{}, fmt.Errorf("%s review pass: %w", pass, err)
	}
	return result, nil
}

func waitForTask(ctx context.Context, client LoomClient, taskID string, criteria Criteria) (*loom.Task, error) {
	timeout := criteria.TaskTimeout
	if timeout <= 0 {
		timeout = DefaultPassTaskTimeout
	}
	pollInterval := criteria.PollInterval
	if pollInterval <= 0 {
		pollInterval = DefaultPassPollInterval
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-waitCtx.Done():
			cancelTaskIfSupported(client, taskID)
			return nil, waitCtx.Err()
		case <-timer.C:
			task, err := client.Get(taskID)
			if err != nil {
				return nil, err
			}
			if task == nil {
				return nil, types.NewUnknown("review pass task is nil", nil)
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

func parsePassResult(pass PassName, workerType loom.WorkerType, taskID string, output string, latency time.Duration) (PassResult, error) {
	var response passResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return PassResult{}, types.NewUserInputError("review pass output must be JSON", err)
	}
	response.Summary = strings.TrimSpace(response.Summary)
	if response.Summary == "" {
		return PassResult{}, types.NewUserInputError("review pass summary is required", nil)
	}
	for i := range response.Findings {
		if err := validateFinding(response.Findings[i]); err != nil {
			return PassResult{}, err
		}
	}
	return PassResult{
		Name:       pass,
		Findings:   response.Findings,
		Summary:    response.Summary,
		LatencyMS:  latency.Milliseconds(),
		TaskID:     taskID,
		WorkerType: string(workerType),
	}, nil
}

func validateFinding(finding Finding) error {
	switch finding.Severity {
	case SeverityError, SeverityWarning, SeverityInfo:
	default:
		return types.NewUserInputError(fmt.Sprintf("unknown review finding severity %q", finding.Severity), nil)
	}
	if strings.TrimSpace(finding.Body) == "" {
		return types.NewUserInputError("review finding body is required", nil)
	}
	return nil
}

func workerTypeForPass(criteria Criteria, pass PassName) loom.WorkerType {
	if criteria.WorkerTypes != nil {
		if workerType := criteria.WorkerTypes[pass]; workerType != "" {
			return workerType
		}
	}
	switch pass {
	case PassStructural:
		return WorkerTypeReviewStructural
	case PassBehavioural:
		return WorkerTypeReviewBehavioural
	case PassAdversarial:
		return WorkerTypeReviewAdversarial
	default:
		return loom.WorkerType("review_" + string(pass))
	}
}

func orderedPasses() []PassName {
	return []PassName{PassStructural, PassBehavioural, PassAdversarial}
}

func defaultReviewCLI(criteria Criteria) string {
	if criteria.CLI != "" {
		return string(criteria.CLI)
	}
	return "codex"
}

func passMetadata(pass PassName, workerType loom.WorkerType, criteria Criteria) map[string]any {
	return map[string]any{
		"worker_type":    string(workerType),
		"review_pass":    string(pass),
		"parent_task_id": criteria.ParentTaskID,
	}
}

func buildPassPrompt(pass PassName, target string) string {
	return fmt.Sprintf(`Run the %s review pass for this target.

Focus:
%s

Target:
%s

Return raw JSON only:
{"findings":[{"severity":"error|warning|info","file":"path","line":null,"body":"description"}],"summary":"one-line summary"}`,
		pass, passFocus(pass), target)
}

func passFocus(pass PassName) string {
	switch pass {
	case PassStructural:
		return "structure, naming, cohesion, boundaries, abstractions, and maintainability"
	case PassBehavioural:
		return "observable behaviour, happy paths, edge cases, side effects, and regressions"
	case PassAdversarial:
		return "security, races, hostile inputs, path traversal, secrets, and abuse cases"
	default:
		return "general code review"
	}
}
