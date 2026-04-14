package workers

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/loom"
	"github.com/thebtf/aimux/pkg/types"
)

// --- mock executor ---

type mockExecutor struct {
	result *types.Result
	err    error
}

func (m *mockExecutor) Run(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func (m *mockExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, nil
}

func (m *mockExecutor) Name() string    { return "mock" }
func (m *mockExecutor) Available() bool { return true }

// --- mock resolver ---

type mockResolver struct {
	args types.SpawnArgs
	err  error
}

func (m *mockResolver) ResolveSpawnArgs(cli string, prompt string) (types.SpawnArgs, error) {
	if m.err != nil {
		return types.SpawnArgs{}, m.err
	}
	a := m.args
	a.CLI = cli
	a.Stdin = prompt
	return a, nil
}

// --- Tests ---

func TestCLIWorker_Execute(t *testing.T) {
	exec := &mockExecutor{result: &types.Result{Content: "output from codex", ExitCode: 0, DurationMS: 150}}
	resolver := &mockResolver{args: types.SpawnArgs{Command: "codex", Args: []string{"-p"}}}

	w := NewCLIWorker(exec, resolver)
	if w.Type() != loom.WorkerTypeCLI {
		t.Fatal("wrong type")
	}

	task := &loom.Task{CLI: "codex", Prompt: "hello", CWD: "/work", Env: map[string]string{"KEY": "val"}}
	result, err := w.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Content != "output from codex" {
		t.Errorf("content: got %q", result.Content)
	}
	if result.DurationMS < 0 {
		t.Error("duration should be non-negative")
	}
	if result.Metadata["exit_code"] != 0 {
		t.Error("exit_code should be 0")
	}
}

func TestCLIWorker_ResolveError(t *testing.T) {
	exec := &mockExecutor{}
	resolver := &mockResolver{err: context.DeadlineExceeded}

	w := NewCLIWorker(exec, resolver)
	task := &loom.Task{CLI: "codex", Prompt: "hello"}
	_, err := w.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected error from resolver")
	}
}

// For ThinkerWorker we cannot easily mock think.GetPattern since it uses package-level registry.
// Instead, test error paths.
func TestThinkerWorker_MissingPattern(t *testing.T) {
	w := NewThinkerWorker()
	if w.Type() != loom.WorkerTypeThinker {
		t.Fatal("wrong type")
	}

	task := &loom.Task{Prompt: "test", Metadata: map[string]any{}}
	_, err := w.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestThinkerWorker_UnknownPattern(t *testing.T) {
	w := NewThinkerWorker()
	task := &loom.Task{Prompt: "test", Metadata: map[string]any{"pattern": "nonexistent_pattern_xyz"}}
	_, err := w.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for unknown pattern")
	}
}

func TestInvestigatorWorker_Execute(t *testing.T) {
	exec := &mockExecutor{result: &types.Result{Content: "investigation result", ExitCode: 0}}
	resolver := &mockResolver{args: types.SpawnArgs{Command: "codex"}}

	w := NewInvestigatorWorker(exec, resolver)
	if w.Type() != loom.WorkerTypeInvestigator {
		t.Fatal("wrong type")
	}

	task := &loom.Task{Prompt: "investigate auth bug", CWD: "/project"}
	result, err := w.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Content != "investigation result" {
		t.Errorf("content: got %q", result.Content)
	}
}

func TestInvestigatorWorker_CLIFromMetadata(t *testing.T) {
	exec := &mockExecutor{result: &types.Result{Content: "gemini result", ExitCode: 0}}
	resolver := &mockResolver{args: types.SpawnArgs{Command: "gemini"}}

	w := NewInvestigatorWorker(exec, resolver)
	task := &loom.Task{
		Prompt:   "investigate perf bug",
		Metadata: map[string]any{"cli": "gemini"},
	}
	result, err := w.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Metadata["cli"] != "gemini" {
		t.Errorf("expected cli=gemini, got %v", result.Metadata["cli"])
	}
}

// OrchestratorWorker: test Type() and nil/missing-strategy guard.
func TestOrchestratorWorker_Type(t *testing.T) {
	w := &OrchestratorWorker{orch: nil}
	if w.Type() != loom.WorkerTypeOrchestrator {
		t.Fatal("wrong type")
	}
}

func TestOrchestratorWorker_MissingStrategy(t *testing.T) {
	w := &OrchestratorWorker{orch: nil}

	task := &loom.Task{Prompt: "test", Metadata: map[string]any{}}
	_, err := w.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing strategy")
	}
}

func TestOrchestratorWorker_NilOrchestrator(t *testing.T) {
	w := &OrchestratorWorker{orch: nil}

	task := &loom.Task{Prompt: "test", Metadata: map[string]any{"strategy": "consensus"}}
	_, err := w.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for nil orchestrator")
	}
}
