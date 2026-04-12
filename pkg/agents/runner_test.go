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
		MaxTurns: 0, // should default to DefaultMaxTurns (1) — single autonomous run
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

// TestRunAgent_SingleTurnDefault verifies that MaxTurns=0 defaults to 1
// and the agent exits after one turn (single autonomous run).
func TestRunAgent_SingleTurnDefault(t *testing.T) {
	exec := &mockExecutor{
		responses: []string{"Completed the work."},
	}

	result, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    makeAgent("worker", "Does the work", ""),
		CLI:      "codex",
		Prompt:   "do stuff",
		MaxTurns: 0,
		Executor: exec,
		Resolver: &mockResolver{},
	})

	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	// DefaultMaxTurns is 1, response is non-empty and has no TASK_COMPLETE,
	// so status is "max_turns" after exactly 1 turn.
	if result.Turns != 1 {
		t.Errorf("Turns = %d, want 1", result.Turns)
	}
	if exec.callCount != 1 {
		t.Errorf("executor called %d times, want 1", exec.callCount)
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

	// Agent with content: content IS the system prompt body.
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

	// When Content is set, it IS the prompt body (no "You are X." preamble).
	if !strings.Contains(capturedPrompt, "Always be helpful.") {
		t.Error("system prompt missing agent content body")
	}
	// Task section must always be present.
	if !strings.Contains(capturedPrompt, "## Task") {
		t.Error("system prompt missing ## Task section")
	}
	if !strings.Contains(capturedPrompt, "write tests") {
		t.Error("system prompt missing user task text")
	}
}

// TestRunAgent_SystemPromptFallback verifies that agents without content
// get a "You are X. Description." preamble.
func TestRunAgent_SystemPromptFallback(t *testing.T) {
	var capturedPrompt string
	captureExec := &capturingExecutor{
		capture: func(args types.SpawnArgs) {
			if len(args.Args) >= 2 {
				capturedPrompt = args.Args[len(args.Args)-1]
			}
		},
		response: "TASK_COMPLETE",
	}

	agent := makeAgent("myagent", "Does stuff", "") // no content
	_, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    agent,
		CLI:      "codex",
		Prompt:   "write tests",
		Executor: captureExec,
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}

	if !strings.Contains(capturedPrompt, "You are myagent.") {
		t.Errorf("fallback prompt missing agent name intro, got: %s", capturedPrompt[:min(200, len(capturedPrompt))])
	}
	if !strings.Contains(capturedPrompt, "Does stuff") {
		t.Error("fallback prompt missing description")
	}
	if !strings.Contains(capturedPrompt, "## Task") {
		t.Error("fallback prompt missing ## Task section")
	}
}

// TestRunAgent_SystemPromptCWD verifies that cwd is included in the Context section.
func TestRunAgent_SystemPromptCWD(t *testing.T) {
	var capturedPrompt string
	captureExec := &capturingExecutor{
		capture: func(args types.SpawnArgs) {
			if len(args.Args) >= 2 {
				capturedPrompt = args.Args[len(args.Args)-1]
			}
		},
		response: "TASK_COMPLETE",
	}

	agent := makeAgent("coder", "", "")
	_, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    agent,
		CLI:      "codex",
		Prompt:   "build it",
		CWD:      "/home/user/project",
		Executor: captureExec,
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}

	if !strings.Contains(capturedPrompt, "Working directory: /home/user/project") {
		t.Error("system prompt missing working directory in Context section")
	}
}

// TestRunAgent_AgentWithRole verifies that RunConfig accepts a role field on the agent
// and that the CLI resolution path works (role is on the Agent struct, resolution
// happens in the server layer; runner itself does not use role, just CLI).
func TestRunAgent_AgentWithRole(t *testing.T) {
	exec := &mockExecutor{
		responses: []string{"TASK_COMPLETE"},
	}

	a := &agents.Agent{
		Name:    "analyst",
		Role:    "codereview",
		Content: "Review the code carefully.",
	}

	result, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    a,
		CLI:      "codex",
		Prompt:   "review this",
		Executor: exec,
		Resolver: &mockResolver{},
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
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

func TestResolveArgs_PropagatesOnOutput(t *testing.T) {
	executed := false
	var receivedArgs types.SpawnArgs
	outputLines := []string{}

	exec := &onOutputExecutor{
		CaptureArgs: func(args types.SpawnArgs) {
			receivedArgs = args
			executed = true
		},
		CaptureLine: func(line string) {
			outputLines = append(outputLines, line)
		},
	}

	_, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    makeAgent("worker", "", ""),
		CLI:      "codex",
		Prompt:   "analyze output",
		Executor: exec,
		OnOutput: func(line string) {
			exec.CaptureLine(line)
		},
		Resolver: &mockResolver{},
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if !executed {
		t.Fatal("executor did not capture SpawnArgs")
	}
	if receivedArgs.OnOutput == nil {
		t.Fatal("expected RunConfig.OnOutput to be copied into SpawnArgs")
	}
	if len(outputLines) != 2 {
		t.Fatalf("expected 2 output lines, got %d", len(outputLines))
	}
}

func TestResolveArgs_FallbackCopiesOnOutput(t *testing.T) {
	outputLines := []string{}

	exec := &onOutputExecutor{
		CaptureLine: func(line string) {
			outputLines = append(outputLines, line)
		},
	}

	_, err := agents.RunAgent(context.Background(), agents.RunConfig{
		Agent:    makeAgent("worker", "", ""),
		CLI:      "codex",
		Prompt:   "analyze output",
		Executor: exec,
		OnOutput: func(line string) {
			outputLines = append(outputLines, line)
		},
		// no resolver; legacy fallback path
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if len(outputLines) != 2 {
		t.Fatalf("expected 2 output lines via fallback path, got %d", len(outputLines))
	}
}

type onOutputExecutor struct {
	CaptureLine func(string)
	CaptureArgs func(types.SpawnArgs)
}

func (o *onOutputExecutor) Run(_ context.Context, args types.SpawnArgs) (*types.Result, error) {
	if o.CaptureArgs != nil {
		o.CaptureArgs(args)
	}
	if args.OnOutput != nil {
		args.OnOutput("line-1")
		args.OnOutput("line-2")
	}
	return &types.Result{Content: "done"}, nil
}

func (o *onOutputExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, fmt.Errorf("not implemented")
}

func (o *onOutputExecutor) Name() string    { return "callback" }
func (o *onOutputExecutor) Available() bool { return true }

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
