package harness

import "testing"

func TestControllerStepExecutesMoveAndUpdatesSession(t *testing.T) {
	controller := NewController(NewInMemoryStore(), WithIDGenerator(func() string { return "think-step-1" }))
	start, err := controller.Start(t.Context(), StartRequest{
		Task:           "evaluate a risky claim",
		ContextSummary: "caller must supply evidence",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := controller.Step(t.Context(), StepRequest{
		SessionID:        start.SessionID,
		ChosenMove:       "critical_thinking",
		WorkProduct:      "The current claim has one assumption and one missing counterexample.",
		Evidence:         []EvidenceRef{{Kind: "file", Ref: "spec.md", Summary: "claim is visible", VerificationStatus: "verified"}},
		CallerConfidence: 0.78,
	})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}

	if !resp.Executed {
		t.Fatal("step should execute by default")
	}
	if resp.GateReport.Status == "" {
		t.Fatalf("gate report missing: %+v", resp.GateReport)
	}
	if resp.ConfidenceCeiling <= 0 {
		t.Fatalf("confidence ceiling not computed: %+v", resp)
	}
	if len(resp.RecommendedMoves) == 0 {
		t.Fatal("recommended moves empty after step")
	}

	session, err := controller.Session(t.Context(), start.SessionID)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if len(session.MoveHistory) != 1 {
		t.Fatalf("move history = %d, want 1", len(session.MoveHistory))
	}
	if len(session.Observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(session.Observations))
	}
	if len(session.Ledger.Known) < 2 {
		t.Fatalf("ledger known entries not updated: %+v", session.Ledger.Known)
	}
}
