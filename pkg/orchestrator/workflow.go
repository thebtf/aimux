package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/types"
)

// WorkflowStep defines one step in a workflow pipeline.
type WorkflowStep struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`      // "exec", "think", "investigate"
	Params    map[string]any `json:"params"`
	Condition string         `json:"condition,omitempty"` // simple condition: "{{prev.content}} contains 'FINDING'"
	OnError   string         `json:"on_error,omitempty"` // "stop" (default), "skip", "retry"
}

// WorkflowDefinition defines a complete workflow.
type WorkflowDefinition struct {
	Name  string         `json:"name"`
	Steps []WorkflowStep `json:"steps"`
	Input string         `json:"input"` // initial input text
}

// WorkflowResult holds the outcome of a workflow execution.
type WorkflowResult struct {
	Name   string       `json:"name"`
	Status string       `json:"status"` // "completed", "failed", "partial"
	Steps  []StepResult `json:"steps"`
	Output string       `json:"output"` // final step's content
}

// StepResult holds the outcome of one workflow step.
type StepResult struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Status  string `json:"status"` // "completed", "skipped", "failed"
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

// WorkflowStrategy implements types.Strategy for declarative multi-step pipelines.
type WorkflowStrategy struct {
	executor types.Executor
	resolver types.CLIResolver
}

// NewWorkflowStrategy creates a workflow strategy.
func NewWorkflowStrategy(executor types.Executor, resolver types.CLIResolver) *WorkflowStrategy {
	return &WorkflowStrategy{executor: executor, resolver: resolver}
}

// Name returns the strategy identifier.
func (w *WorkflowStrategy) Name() string { return "workflow" }

// Execute runs a declarative pipeline defined in params.Extra["workflow"].
func (w *WorkflowStrategy) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	// Parse workflow definition
	workflowJSON, ok := params.Extra["workflow"].(string)
	if !ok || workflowJSON == "" {
		return nil, fmt.Errorf("workflow: missing or empty 'workflow' in Extra")
	}

	var def WorkflowDefinition
	if err := json.Unmarshal([]byte(workflowJSON), &def); err != nil {
		return nil, fmt.Errorf("workflow: invalid definition JSON: %w", err)
	}

	if len(def.Steps) == 0 {
		return nil, fmt.Errorf("workflow %q: no steps defined", def.Name)
	}

	results := make(map[string]*StepResult, len(def.Steps))
	stepResults := make([]StepResult, 0, len(def.Steps))
	overallStatus := "completed"

	for i := range def.Steps {
		step := &def.Steps[i]

		// Evaluate condition
		if step.Condition != "" {
			condMet, err := w.evaluateCondition(step.Condition, def.Input, results)
			if err != nil {
				sr := StepResult{
					ID:     step.ID,
					Tool:   step.Tool,
					Status: "failed",
					Error:  fmt.Sprintf("condition evaluation error: %v", err),
				}
				results[step.ID] = &sr
				stepResults = append(stepResults, sr)
				overallStatus = "partial"
				break
			}
			if !condMet {
				sr := StepResult{
					ID:     step.ID,
					Tool:   step.Tool,
					Status: "skipped",
				}
				results[step.ID] = &sr
				stepResults = append(stepResults, sr)
				continue
			}
		}

		// Execute step with optional retry
		sr := w.executeStep(ctx, step, def.Input, results, params.CWD)

		if sr.Status == "failed" {
			onError := step.OnError
			if onError == "" {
				onError = "stop"
			}

			switch onError {
			case "retry":
				// One retry
				sr = w.executeStep(ctx, step, def.Input, results, params.CWD)
				if sr.Status == "failed" {
					results[step.ID] = &sr
					stepResults = append(stepResults, sr)
					overallStatus = "failed"
					return w.buildResult(def.Name, overallStatus, stepResults), nil
				}
			case "skip":
				sr.Status = "skipped"
				results[step.ID] = &sr
				stepResults = append(stepResults, sr)
				overallStatus = "partial"
				continue
			default: // "stop"
				results[step.ID] = &sr
				stepResults = append(stepResults, sr)
				overallStatus = "failed"
				return w.buildResult(def.Name, overallStatus, stepResults), nil
			}
		}

		results[step.ID] = &sr
		stepResults = append(stepResults, sr)
	}

	return w.buildResult(def.Name, overallStatus, stepResults), nil
}

// executeStep runs a single workflow step and returns its result.
func (w *WorkflowStrategy) executeStep(
	ctx context.Context,
	step *WorkflowStep,
	input string,
	results map[string]*StepResult,
	cwd string,
) StepResult {
	// Interpolate params
	interpolatedParams := w.interpolateParams(step.Params, input, results)

	switch step.Tool {
	case "exec":
		return w.executeExecStep(ctx, step, interpolatedParams, cwd)
	case "think":
		return w.executeThinkStep(step, interpolatedParams)
	default:
		return StepResult{
			ID:     step.ID,
			Tool:   step.Tool,
			Status: "failed",
			Error:  fmt.Sprintf("unknown tool %q; supported: exec, think", step.Tool),
		}
	}
}

// executeExecStep runs an "exec" step via the CLI executor.
func (w *WorkflowStrategy) executeExecStep(
	ctx context.Context,
	step *WorkflowStep,
	params map[string]any,
	cwd string,
) StepResult {
	cli := stringParam(params, "cli", "")
	prompt := stringParam(params, "prompt", "")
	stepCWD := stringParam(params, "cwd", cwd)
	timeout := intParam(params, "timeout_seconds", 0)

	if cli == "" {
		return StepResult{
			ID:     step.ID,
			Tool:   step.Tool,
			Status: "failed",
			Error:  "exec step requires 'cli' param",
		}
	}

	// Workflow steps specify their own CLI explicitly; top-level model/effort
	// from StrategyParams does not currently propagate to per-step resolution.
	// Per-step model override is a follow-up (each step can carry its own
	// "model"/"effort" params if needed).
	spawnArgs := resolveOrFallback(w.resolver, cli, prompt, stepCWD, timeout)
	result, err := w.executor.Run(ctx, spawnArgs)
	if err != nil {
		return StepResult{
			ID:     step.ID,
			Tool:   step.Tool,
			Status: "failed",
			Error:  err.Error(),
		}
	}

	return StepResult{
		ID:      step.ID,
		Tool:    step.Tool,
		Status:  "completed",
		Content: result.Content,
	}
}

// executeThinkStep runs a "think" step in-process using the pattern registry.
func (w *WorkflowStrategy) executeThinkStep(step *WorkflowStep, params map[string]any) StepResult {
	patternName := stringParam(params, "pattern", "think")

	handler := think.GetPattern(patternName)
	if handler == nil {
		return StepResult{
			ID:     step.ID,
			Tool:   step.Tool,
			Status: "failed",
			Error:  fmt.Sprintf("unknown think pattern %q", patternName),
		}
	}

	// Build input from params, excluding "pattern" which is the routing key
	input := make(map[string]any, len(params))
	for k, v := range params {
		if k != "pattern" {
			input[k] = v
		}
	}

	sessionID := stringParam(params, "session_id", "")

	validInput, err := handler.Validate(input)
	if err != nil {
		return StepResult{
			ID:     step.ID,
			Tool:   step.Tool,
			Status: "failed",
			Error:  fmt.Sprintf("validation error: %v", err),
		}
	}

	thinkResult, err := handler.Handle(validInput, sessionID)
	if err != nil {
		return StepResult{
			ID:     step.ID,
			Tool:   step.Tool,
			Status: "failed",
			Error:  fmt.Sprintf("pattern error: %v", err),
		}
	}

	// Serialize the think result data as content
	contentBytes, _ := json.Marshal(thinkResult.Data)
	return StepResult{
		ID:      step.ID,
		Tool:    step.Tool,
		Status:  "completed",
		Content: string(contentBytes),
	}
}

// buildResult constructs the final StrategyResult from completed step results.
func (w *WorkflowStrategy) buildResult(name, status string, stepResults []StepResult) *types.StrategyResult {
	wfResult := WorkflowResult{
		Name:   name,
		Status: status,
		Steps:  stepResults,
	}

	// Final output is the last completed step's content
	for i := len(stepResults) - 1; i >= 0; i-- {
		if stepResults[i].Status == "completed" {
			wfResult.Output = stepResults[i].Content
			break
		}
	}

	data, _ := json.Marshal(wfResult)
	return &types.StrategyResult{
		Content: string(data),
		Status:  status,
		Turns:   len(stepResults),
		Extra:   map[string]any{"workflow": wfResult},
	}
}

// interpolateParams replaces template placeholders in all string param values.
func (w *WorkflowStrategy) interpolateParams(params map[string]any, input string, results map[string]*StepResult) map[string]any {
	out := make(map[string]any, len(params))
	for k, v := range params {
		if s, ok := v.(string); ok {
			out[k] = w.interpolate(s, input, results)
		} else {
			out[k] = v
		}
	}
	return out
}

// interpolate replaces {{input}}, {{step_id.content}}, {{step_id.status}} in a string.
func (w *WorkflowStrategy) interpolate(s, input string, results map[string]*StepResult) string {
	s = strings.ReplaceAll(s, "{{input}}", input)

	for id, sr := range results {
		s = strings.ReplaceAll(s, "{{"+id+".content}}", sr.Content)
		s = strings.ReplaceAll(s, "{{"+id+".status}}", sr.Status)
	}
	return s
}

// evaluateCondition evaluates a simple condition string after template interpolation.
// Supported forms:
//   - "{{step_id.content}} contains 'FINDING'"
//   - "{{step_id.status}} == 'completed'"
func (w *WorkflowStrategy) evaluateCondition(condition, input string, results map[string]*StepResult) (bool, error) {
	resolved := w.interpolate(condition, input, results)

	// "X contains 'Y'"
	if idx := strings.Index(resolved, " contains '"); idx >= 0 {
		lhs := strings.TrimSpace(resolved[:idx])
		rest := resolved[idx+len(" contains '"):]
		closeQuote := strings.Index(rest, "'")
		if closeQuote < 0 {
			return false, fmt.Errorf("condition syntax: unclosed quote in %q", condition)
		}
		rhs := rest[:closeQuote]
		return strings.Contains(lhs, rhs), nil
	}

	// "X == 'Y'"
	if idx := strings.Index(resolved, " == '"); idx >= 0 {
		lhs := strings.TrimSpace(resolved[:idx])
		rest := resolved[idx+len(" == '"):]
		closeQuote := strings.Index(rest, "'")
		if closeQuote < 0 {
			return false, fmt.Errorf("condition syntax: unclosed quote in %q", condition)
		}
		rhs := rest[:closeQuote]
		return lhs == rhs, nil
	}

	return false, fmt.Errorf("condition syntax: unrecognized form %q (supported: 'X contains Y', 'X == Y')", condition)
}

// stringParam extracts a string value from a params map with a fallback default.
func stringParam(params map[string]any, key, def string) string {
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// intParam extracts an int value from a params map with a fallback default.
func intParam(params map[string]any, key string, def int) int {
	if v, ok := params[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case float64:
			return int(n)
		case json.Number:
			i, _ := n.Int64()
			return int(i)
		}
	}
	return def
}
