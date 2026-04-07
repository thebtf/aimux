package agents_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/agents"
	"github.com/thebtf/aimux/pkg/types"
)

// mockExecutor returns predefined responses in sequence.
// When the sequence is exhausted it repeats the last response.
type mockExecutor struct {
	responses []string
	callCount int
}

func (m *mockExecutor) Run(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
	idx := m.callCount
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	m.callCount++
	return &types.Result{Content: m.responses[idx]}, nil
}

func (m *mockExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockExecutor) Name() string      { return "mock" }
func (m *mockExecutor) Available() bool   { return true }

// mockResolver always succeeds with minimal SpawnArgs.
type mockResolver struct{}

func (r *mockResolver) ResolveSpawnArgs(cli string, prompt string) (types.SpawnArgs, error) {
	return types.SpawnArgs{
		CLI:     cli,
		Command: cli,
		Args:    []string{"-p", prompt},
	}, nil
}

func makeAgent(name, desc, content string) *agents.Agent {
	return &agents.Agent{
		Name:        name,
		Description: desc,
		Content:     content,
	}
}

// --- Tests ---

func TestRunAgent_SingleTurnWithCompletion(t *testing.T) {
	exec := &mockExecutor{
		responses: []string{"Here is the result. TASK_COMPLETE"},
	}

	result, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    makeAgent("coder", "Writes code", "Always use idiomatic Go."),
		CLI:      "codex",
		Prompt:   "Write a hello world program",
		Executor: exec,
		Resolver: &mockResolver{},
	})

	if err != nil {
		t.Fatalf("RunAgent: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Turns != 1 {
		t.Errorf("Turns = %d, want 1", result.Turns)
	}
	if !strings.Contains(result.Content, "TASK_COMPLETE") {
		t.Error("Content should contain TASK_COMPLETE signal")
	}
	if exec.callCount != 1 {
		t.Errorf("executor called %d times, want 1", exec.callCount)
	}
}

func TestRunAgent_MultiTurnContinues(t *testing.T) {
	// First two responses have no completion signal; third does.
	exec := &mockExecutor{
		responses: []string{
			"Thinking about the problem...",
			"Making progress on the solution...",
			"Done! TASK_COMPLETE",
		},
	}

	result, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    makeAgent("analyst", "Analyzes problems", ""),
		CLI:      "codex",
		Prompt:   "Analyze this codebase",
		MaxTurns: 5,
		Executor: exec,
		Resolver: &mockResolver{},
	})

	if err != nil {
		t.Fatalf("RunAgent: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Turns != 3 {
		t.Errorf("Turns = %d, want 3", result.Turns)
	}
	if exec.callCount != 3 {
		t.Errorf("executor called %d times, want 3", exec.callCount)
	}
	// Aggregated content should include all turns
	if !strings.Contains(result.Content, "Thinking about") {
		t.Error("Content missing turn 1 output")
	}
	if !strings.Contains(result.Content, "Making progress") {
		t.Error("Content missing turn 2 output")
	}
}

func TestRunAgent_MaxTurnsReached(t *testing.T) {
	// Never emits completion signal.
	exec := &mockExecutor{
		responses: []string{"Still working..."},
	}

	result, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    makeAgent("worker", "", ""),
		CLI:      "codex",
		Prompt:   "Do something",
		MaxTurns: 3,
		Executor: exec,
		Resolver: &mockResolver{},
	})

	if err != nil {
		t.Fatalf("RunAgent: unexpected error: %v", err)
	}
	if result.Status != "max_turns" {
		t.Errorf("Status = %q, want max_turns", result.Status)
	}
	if result.Turns != 3 {
		t.Errorf("Turns = %d, want 3", result.Turns)
	}
	if exec.callCount != 3 {
		t.Errorf("executor called %d times, want 3", exec.callCount)
	}
}

func TestRunAgent_DefaultMaxTurns(t *testing.T) {
	exec := &mockExecutor{
		responses: []string{"no completion"},
	}

	result, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    makeAgent("x", "", ""),
		CLI:      "codex",
		Prompt:   "task",
		MaxTurns: 0, // should default to DefaultMaxTurns (10)
		Executor: exec,
		Resolver: &mockResolver{},
	})

	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if result.Turns != agents.DefaultMaxTurns {
		t.Errorf("Turns = %d, want %d (DefaultMaxTurns)", result.Turns, agents.DefaultMaxTurns)
	}
}

func TestRunAgent_SystemPromptFormat(t *testing.T) {
	var capturedPrompt string
	captureExec := &capturingExecutor{
		capture: func(args types.SpawnArgs) {
			// The prompt is the last element in Args (after "-p").
			if len(args.Args) >= 2 {
				capturedPrompt = args.Args[len(args.Args)-1]
			}
		},
		response: "TASK_COMPLETE",
	}

	agent := makeAgent("myagent", "Does stuff", "Always be helpful.")
	_, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    agent,
		CLI:      "codex",
		Prompt:   "write tests",
		Executor: captureExec,
		// No resolver — tests legacy fallback path
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}

	if !strings.Contains(capturedPrompt, "You are myagent.") {
		t.Errorf("system prompt missing agent name intro, got: %s", capturedPrompt[:min(200, len(capturedPrompt))])
	}
	if !strings.Contains(capturedPrompt, "Does stuff") {
		t.Error("system prompt missing description")
	}
	if !strings.Contains(capturedPrompt, "Always be helpful.") {
		t.Error("system prompt missing agent content")
	}
	if !strings.Contains(capturedPrompt, agents.CompletionSignal) {
		t.Error("system prompt missing TASK_COMPLETE instruction")
	}
	if !strings.Contains(capturedPrompt, "Task: write tests") {
		t.Error("system prompt missing user task")
	}
}

func TestRunAgent_CompletionSignalCaseInsensitive(t *testing.T) {
	exec := &mockExecutor{
		responses: []string{"task_complete"},
	}

	result, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    makeAgent("a", "", ""),
		CLI:      "codex",
		Prompt:   "go",
		Executor: exec,
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed (case-insensitive match)", result.Status)
	}
}

func TestRunAgent_NilAgentError(t *testing.T) {
	_, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    nil,
		CLI:      "codex",
		Prompt:   "task",
		Executor: &mockExecutor{responses: []string{"x"}},
	})
	if err == nil {
		t.Error("expected error for nil Agent")
	}
}

func TestRunAgent_NilExecutorError(t *testing.T) {
	_, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    makeAgent("a", "", ""),
		CLI:      "codex",
		Prompt:   "task",
		Executor: nil,
	})
	if err == nil {
		t.Error("expected error for nil Executor")
	}
}

// capturingExecutor records the SpawnArgs it receives.
type capturingExecutor struct {
	capture  func(types.SpawnArgs)
	response string
}

func (c *capturingExecutor) Run(_ context.Context, args types.SpawnArgs) (*types.Result, error) {
	if c.capture != nil {
		c.capture(args)
	}
	return &types.Result{Content: c.response}, nil
}

func (c *capturingExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *capturingExecutor) Name() string    { return "capture" }
func (c *capturingExecutor) Available() bool { return true }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
