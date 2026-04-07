package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/think/patterns"
	"github.com/thebtf/aimux/pkg/types"
)

// workflowJSON serializes a WorkflowDefinition to JSON string for use in StrategyParams.
func workflowJSON(t *testing.T, def orchestrator.WorkflowDefinition) string {
	t.Helper()
	b, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal workflow: %v", err)
	}
	return string(b)
}

// makeWorkflowParams builds StrategyParams with the workflow definition embedded.
func makeWorkflowParams(t *testing.T, def orchestrator.WorkflowDefinition) types.StrategyParams {
	t.Helper()
	return types.StrategyParams{
		Extra: map[string]any{
			"workflow": workflowJSON(t, def),
		},
	}
}

// sequentialMockExecutor returns different responses for successive calls.
type sequentialMockExecutor struct {
	responses []string
	errors    []error
	callCount int
}

func (s *sequentialMockExecutor) Run(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
	idx := s.callCount
	s.callCount++
	if idx < len(s.errors) && s.errors[idx] != nil {
		return nil, s.errors[idx]
	}
	content := ""
	if idx < len(s.responses) {
		content = s.responses[idx]
	}
	return &types.Result{Content: content, ExitCode: 0}, nil
}
func (s *sequentialMockExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, nil
}
func (s *sequentialMockExecutor) Name() string    { return "sequential_mock" }
func (s *sequentialMockExecutor) Available() bool { return true }

// --- Tests ---

func TestWorkflow_SingleExecStep(t *testing.T) {
	exec := &mockExecutor{
		runResult: &types.Result{Content: "step1 output", ExitCode: 0},
	}

	strategy := orchestrator.NewWorkflowStrategy(exec, nil)

	def := orchestrator.WorkflowDefinition{
		Name:  "single-step",
		Input: "hello",
		Steps: []orchestrator.WorkflowStep{
			{ID: "step1", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "{{input}}"}},
		},
	}

	result, err := strategy.Execute(context.Background(), makeWorkflowParams(t, def))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Turns != 1 {
		t.Errorf("Turns = %d, want 1", result.Turns)
	}

	var wfResult orchestrator.WorkflowResult
	if err := json.Unmarshal([]byte(result.Content), &wfResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if wfResult.Output != "step1 output" {
		t.Errorf("Output = %q, want 'step1 output'", wfResult.Output)
	}
	if len(wfResult.Steps) != 1 {
		t.Fatalf("Steps count = %d, want 1", len(wfResult.Steps))
	}
	if wfResult.Steps[0].Status != "completed" {
		t.Errorf("step1 Status = %q, want completed", wfResult.Steps[0].Status)
	}
}

func TestWorkflow_MultiStepTemplateInterpolation(t *testing.T) {
	exec := &sequentialMockExecutor{
		responses: []string{"output from step1", "processed: output from step1"},
	}

	strategy := orchestrator.NewWorkflowStrategy(exec, nil)

	def := orchestrator.WorkflowDefinition{
		Name:  "two-step",
		Input: "initial input",
		Steps: []orchestrator.WorkflowStep{
			{ID: "step1", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "{{input}}"}},
			{ID: "step2", Tool: "exec", Params: map[string]any{"cli": "claude", "prompt": "process: {{step1.content}}"}},
		},
	}

	result, err := strategy.Execute(context.Background(), makeWorkflowParams(t, def))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if exec.callCount != 2 {
		t.Errorf("executor called %d times, want 2", exec.callCount)
	}

	var wfResult orchestrator.WorkflowResult
	if err := json.Unmarshal([]byte(result.Content), &wfResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if wfResult.Output != "processed: output from step1" {
		t.Errorf("Output = %q, want 'processed: output from step1'", wfResult.Output)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
}

func TestWorkflow_ConditionalStep_Skipped(t *testing.T) {
	exec := &sequentialMockExecutor{
		responses: []string{"no findings here"},
	}

	strategy := orchestrator.NewWorkflowStrategy(exec, nil)

	def := orchestrator.WorkflowDefinition{
		Name:  "conditional-skip",
		Input: "scan target",
		Steps: []orchestrator.WorkflowStep{
			{ID: "scan", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "scan {{input}}"}},
			{
				ID:        "report",
				Tool:      "exec",
				Params:    map[string]any{"cli": "codex", "prompt": "report findings"},
				Condition: "{{scan.content}} contains 'FINDING'",
			},
		},
	}

	result, err := strategy.Execute(context.Background(), makeWorkflowParams(t, def))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Only "scan" step ran — "report" was skipped
	if exec.callCount != 1 {
		t.Errorf("executor called %d times, want 1 (report should be skipped)", exec.callCount)
	}

	var wfResult orchestrator.WorkflowResult
	if err := json.Unmarshal([]byte(result.Content), &wfResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(wfResult.Steps) != 2 {
		t.Fatalf("Steps count = %d, want 2", len(wfResult.Steps))
	}
	if wfResult.Steps[1].Status != "skipped" {
		t.Errorf("report step Status = %q, want skipped", wfResult.Steps[1].Status)
	}
}

func TestWorkflow_ConditionalStep_Executed(t *testing.T) {
	exec := &sequentialMockExecutor{
		responses: []string{"FINDING: critical bug at line 42", "report generated"},
	}

	strategy := orchestrator.NewWorkflowStrategy(exec, nil)

	def := orchestrator.WorkflowDefinition{
		Name:  "conditional-execute",
		Input: "scan target",
		Steps: []orchestrator.WorkflowStep{
			{ID: "scan", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "scan {{input}}"}},
			{
				ID:        "report",
				Tool:      "exec",
				Params:    map[string]any{"cli": "codex", "prompt": "report: {{scan.content}}"},
				Condition: "{{scan.content}} contains 'FINDING'",
			},
		},
	}

	result, err := strategy.Execute(context.Background(), makeWorkflowParams(t, def))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if exec.callCount != 2 {
		t.Errorf("executor called %d times, want 2", exec.callCount)
	}

	var wfResult orchestrator.WorkflowResult
	if err := json.Unmarshal([]byte(result.Content), &wfResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if wfResult.Steps[1].Status != "completed" {
		t.Errorf("report step Status = %q, want completed", wfResult.Steps[1].Status)
	}
	if wfResult.Output != "report generated" {
		t.Errorf("Output = %q, want 'report generated'", wfResult.Output)
	}
}

func TestWorkflow_OnErrorSkip_Continues(t *testing.T) {
	exec := &sequentialMockExecutor{
		responses: []string{"", "step3 output"},
		errors:    []error{errors.New("step2 failed"), nil},
	}
	// First call (step1) succeeds, second (step2) fails, third (step3) succeeds.
	// But step1 also needs a response: prepend one.
	exec2 := &sequentialMockExecutor{
		responses: []string{"step1 output", "", "step3 output"},
		errors:    []error{nil, errors.New("step2 failed"), nil},
	}

	strategy := orchestrator.NewWorkflowStrategy(exec2, nil)

	def := orchestrator.WorkflowDefinition{
		Name:  "skip-on-error",
		Input: "input",
		Steps: []orchestrator.WorkflowStep{
			{ID: "step1", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "step1"}},
			{ID: "step2", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "step2"}, OnError: "skip"},
			{ID: "step3", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "step3"}},
		},
	}

	result, err := strategy.Execute(context.Background(), makeWorkflowParams(t, def))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var wfResult orchestrator.WorkflowResult
	if err := json.Unmarshal([]byte(result.Content), &wfResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(wfResult.Steps) != 3 {
		t.Fatalf("Steps count = %d, want 3", len(wfResult.Steps))
	}
	if wfResult.Steps[1].Status != "skipped" {
		t.Errorf("step2 Status = %q, want skipped", wfResult.Steps[1].Status)
	}
	if wfResult.Steps[2].Status != "completed" {
		t.Errorf("step3 Status = %q, want completed", wfResult.Steps[2].Status)
	}
	// Overall status should be partial since a step was skipped due to error
	if wfResult.Status != "partial" {
		t.Errorf("Status = %q, want partial", wfResult.Status)
	}
	if wfResult.Output != "step3 output" {
		t.Errorf("Output = %q, want 'step3 output'", wfResult.Output)
	}
	_ = exec // suppress unused warning
}

func TestWorkflow_OnErrorStop_Stops(t *testing.T) {
	exec := &sequentialMockExecutor{
		responses: []string{"step1 output", ""},
		errors:    []error{nil, errors.New("step2 exploded")},
	}

	strategy := orchestrator.NewWorkflowStrategy(exec, nil)

	def := orchestrator.WorkflowDefinition{
		Name:  "stop-on-error",
		Input: "input",
		Steps: []orchestrator.WorkflowStep{
			{ID: "step1", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "step1"}},
			{ID: "step2", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "step2"}}, // on_error default = stop
			{ID: "step3", Tool: "exec", Params: map[string]any{"cli": "codex", "prompt": "step3"}},
		},
	}

	result, err := strategy.Execute(context.Background(), makeWorkflowParams(t, def))
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	var wfResult orchestrator.WorkflowResult
	if err := json.Unmarshal([]byte(result.Content), &wfResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if wfResult.Status != "failed" {
		t.Errorf("Status = %q, want failed", wfResult.Status)
	}
	// step3 should not appear — pipeline stopped at step2
	if len(wfResult.Steps) != 2 {
		t.Errorf("Steps count = %d, want 2 (pipeline should stop after step2 failure)", len(wfResult.Steps))
	}
	if wfResult.Steps[1].Status != "failed" {
		t.Errorf("step2 Status = %q, want failed", wfResult.Steps[1].Status)
	}
	if !strings.Contains(wfResult.Steps[1].Error, "step2 exploded") {
		t.Errorf("step2 Error = %q, want it to contain 'step2 exploded'", wfResult.Steps[1].Error)
	}
}

func TestWorkflow_ThinkStep(t *testing.T) {
	patterns.RegisterAll() // idempotent via sync.Once

	strategy := orchestrator.NewWorkflowStrategy(nil, nil)

	def := orchestrator.WorkflowDefinition{
		Name:  "think-workflow",
		Input: "Should we use microservices or monolith?",
		Steps: []orchestrator.WorkflowStep{
			{
				ID:   "analysis",
				Tool: "think",
				Params: map[string]any{
					"pattern": "think",
					"thought": "{{input}}",
				},
			},
		},
	}

	result, err := strategy.Execute(context.Background(), makeWorkflowParams(t, def))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var wfResult orchestrator.WorkflowResult
	if err := json.Unmarshal([]byte(result.Content), &wfResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(wfResult.Steps) != 1 {
		t.Fatalf("Steps count = %d, want 1", len(wfResult.Steps))
	}
	if wfResult.Steps[0].Status != "completed" {
		t.Errorf("think step Status = %q, want completed; error: %s", wfResult.Steps[0].Status, wfResult.Steps[0].Error)
	}
	if wfResult.Steps[0].Content == "" {
		t.Error("think step Content is empty")
	}
}

func TestWorkflow_EmptySteps_ReturnsError(t *testing.T) {
	strategy := orchestrator.NewWorkflowStrategy(nil, nil)

	def := orchestrator.WorkflowDefinition{
		Name:  "empty",
		Input: "anything",
		Steps: []orchestrator.WorkflowStep{},
	}

	_, err := strategy.Execute(context.Background(), makeWorkflowParams(t, def))
	if err == nil {
		t.Error("expected error for empty steps, got nil")
	}
	if !strings.Contains(err.Error(), "no steps defined") {
		t.Errorf("error = %q, want it to contain 'no steps defined'", err.Error())
	}
}
