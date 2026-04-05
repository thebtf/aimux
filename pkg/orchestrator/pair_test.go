package orchestrator_test

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/types"
)

// mockExecutor implements types.Executor for testing.
type mockExecutor struct {
	runResult *types.Result
	runErr    error
}

func (m *mockExecutor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	if m.runErr != nil {
		return nil, m.runErr
	}
	return m.runResult, nil
}

func (m *mockExecutor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	return nil, nil
}

func (m *mockExecutor) Name() string    { return "mock" }
func (m *mockExecutor) Available() bool { return true }

func TestPairCoding_NoDiffContent(t *testing.T) {
	driver := &mockExecutor{
		runResult: &types.Result{Content: "No changes needed", ExitCode: 0},
	}
	reviewer := &mockExecutor{
		runResult: &types.Result{Content: "LGTM", ExitCode: 0},
	}

	pair := orchestrator.NewPairCoding(driver, reviewer)

	result, err := pair.Execute(context.Background(), types.StrategyParams{
		Prompt: "add hello function",
		CWD:    "/tmp",
		CLIs:   []string{"codex", "claude"},
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Content != "No changes needed" {
		t.Errorf("Content = %q, expected non-diff passthrough", result.Content)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
}

func TestPairCoding_WithDiff(t *testing.T) {
	diffOutput := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,5 @@
 package main
+
+func hello() { fmt.Println("hello") }
`
	reviewJSON := `[{"hunk_index": 0, "verdict": "approved", "comment": "looks good"}]`

	driver := &mockExecutor{
		runResult: &types.Result{Content: diffOutput, ExitCode: 0},
	}
	reviewer := &mockExecutor{
		runResult: &types.Result{Content: reviewJSON, ExitCode: 0},
	}

	pair := orchestrator.NewPairCoding(driver, reviewer)

	result, err := pair.Execute(context.Background(), types.StrategyParams{
		Prompt: "add hello function",
		CWD:    "/tmp",
		CLIs:   []string{"codex", "claude"},
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.ReviewReport == nil {
		t.Fatal("expected ReviewReport")
	}
	if result.ReviewReport.Approved != 1 {
		t.Errorf("Approved = %d, want 1", result.ReviewReport.Approved)
	}
	if result.ReviewReport.Rounds != 1 {
		t.Errorf("Rounds = %d, want 1", result.ReviewReport.Rounds)
	}
}

func TestPairCoding_DriverFailure(t *testing.T) {
	driver := &mockExecutor{
		runErr: types.NewExecutorError("codex crashed", nil, ""),
	}
	reviewer := &mockExecutor{}

	pair := orchestrator.NewPairCoding(driver, reviewer)

	_, err := pair.Execute(context.Background(), types.StrategyParams{
		Prompt: "add hello",
		CLIs:   []string{"codex", "claude"},
	})

	if err == nil {
		t.Error("expected error when driver fails")
	}
}
