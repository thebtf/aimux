package orchestrator_test

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/types"
)

func TestOrchestrator_ExecuteUnknownStrategy(t *testing.T) {
	log := newTestLogger(t)
	orch := orchestrator.New(log)

	_, err := orch.Execute(context.Background(), "nonexistent", types.StrategyParams{})
	if err == nil {
		t.Error("expected error for unknown strategy")
	}
}

func TestOrchestrator_Register(t *testing.T) {
	log := newTestLogger(t)
	orch := orchestrator.New(log)

	mock := &mockStrategy{name: "test_strategy"}
	orch.Register(mock)

	result, err := orch.Execute(context.Background(), "test_strategy", types.StrategyParams{
		Prompt: "test",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "mock_completed" {
		t.Errorf("Status = %q, want mock_completed", result.Status)
	}
}

type mockStrategy struct {
	name string
}

func (m *mockStrategy) Name() string { return m.name }
func (m *mockStrategy) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	return &types.StrategyResult{
		Content: "mock result",
		Status:  "mock_completed",
		Turns:   1,
	}, nil
}
