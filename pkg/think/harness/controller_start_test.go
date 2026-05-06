package harness

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestControllerStartCreatesGuidedSession(t *testing.T) {
	controller := NewController(NewInMemoryStore(), WithIDGenerator(func() string { return "think-test-1" }))

	resp, err := controller.Start(t.Context(), StartRequest{
		Task:           "decide whether to ship the harness",
		ContextSummary: "CR-002 requires caller-owned reasoning",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if resp.SessionID != "think-test-1" {
		t.Fatalf("session_id = %q, want think-test-1", resp.SessionID)
	}
	if resp.Phase != PhaseFrame {
		t.Fatalf("phase = %q, want %q", resp.Phase, PhaseFrame)
	}
	if len(resp.AllowedMoveGroups) == 0 {
		t.Fatal("allowed move groups empty")
	}
	if len(resp.RecommendedMoves) == 0 {
		t.Fatal("recommended moves empty")
	}
	if len(resp.MissingInputs) == 0 {
		t.Fatal("missing inputs empty")
	}
	if resp.NextPrompt == "" {
		t.Fatal("next prompt empty")
	}
	if len(resp.KnowledgeState.Unknown) == 0 {
		t.Fatalf("knowledge state should classify unknowns: %+v", resp.KnowledgeState)
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(raw), "final_answer") {
		t.Fatalf("start response must not include final answer: %s", raw)
	}
}

func TestControllerStartRejectsMissingTask(t *testing.T) {
	controller := NewController(NewInMemoryStore())

	_, err := controller.Start(t.Context(), StartRequest{
		ContextSummary: "context without task",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Start error = %v, want ErrInvalidInput", err)
	}
}
