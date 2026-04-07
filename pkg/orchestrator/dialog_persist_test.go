package orchestrator_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/types"
)

// countingExecutor tracks how many times Run is called and returns a fixed response.
type countingExecutor struct {
	calls    int
	response string
}

func (e *countingExecutor) Run(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
	e.calls++
	return &types.Result{Content: e.response, ExitCode: 0}, nil
}
func (e *countingExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, nil
}
func (e *countingExecutor) Name() string    { return "counting" }
func (e *countingExecutor) Available() bool { return true }

// TestDialog_TurnHistoryInResult verifies TurnHistory is populated after Execute.
func TestDialog_TurnHistoryInResult(t *testing.T) {
	exec := &countingExecutor{response: "hello from CLI"}
	dialog := orchestrator.NewSequentialDialog(exec, nil)

	result, err := dialog.Execute(context.Background(), types.StrategyParams{
		Prompt:   "discuss Go",
		CLIs:     []string{"codex", "claude"},
		MaxTurns: 1,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(result.TurnHistory) == 0 {
		t.Fatal("expected non-empty TurnHistory after Execute")
	}

	// TurnHistory must be valid JSON array.
	var turns []map[string]any
	if jsonErr := json.Unmarshal(result.TurnHistory, &turns); jsonErr != nil {
		t.Fatalf("TurnHistory is not valid JSON: %v", jsonErr)
	}
	if len(turns) != 2 {
		t.Errorf("expected 2 turns (1 max_turns * 2 CLIs), got %d", len(turns))
	}

	// Each turn must have CLI, Content, and Turn fields (capitalized — no JSON tags on turnEntry).
	for i, turn := range turns {
		if turn["CLI"] == nil {
			t.Errorf("turn %d missing 'CLI' field", i)
		}
		if turn["Content"] == nil {
			t.Errorf("turn %d missing 'Content' field", i)
		}
	}
}

// TestDialog_ResumeContinuesFromPriorTurns verifies that passing prior_turns in Extra
// causes the new dialog to continue numbering from where the previous session left off.
func TestDialog_ResumeContinuesFromPriorTurns(t *testing.T) {
	exec := &countingExecutor{response: "continuation response"}
	dialog := orchestrator.NewSequentialDialog(exec, nil)

	// First session: 1 turn.
	first, err := dialog.Execute(context.Background(), types.StrategyParams{
		Prompt:   "first topic",
		CLIs:     []string{"codex", "claude"},
		MaxTurns: 1,
	})
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if len(first.TurnHistory) == 0 {
		t.Fatal("expected TurnHistory from first session")
	}

	// Second session: resume with prior turns.
	second, err := dialog.Execute(context.Background(), types.StrategyParams{
		Prompt:   "continue topic",
		CLIs:     []string{"codex", "claude"},
		MaxTurns: 1,
		Extra:    map[string]any{"prior_turns": first.TurnHistory},
	})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	// Second result should have 4 turns total (2 prior + 2 new).
	if second.Turns != 4 {
		t.Errorf("expected 4 total turns after resume, got %d", second.Turns)
	}

	// Content should include all 4 turns.
	if !strings.Contains(second.Content, "turn 3") && !strings.Contains(second.Content, "turn 4") {
		t.Errorf("resumed content should reference turns 3 and 4, got: %q", second.Content[:dialogMin(200, len(second.Content))])
	}
}

func dialogMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
