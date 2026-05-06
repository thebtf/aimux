package harness

import (
	"sync"
	"testing"
)

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

func TestControllerStepConcurrentEvidenceIDsAreUnique(t *testing.T) {
	controller := NewController(NewInMemoryStore(), WithIDGenerator(func() string { return "think-step-concurrent" }))
	start, err := controller.Start(t.Context(), StartRequest{
		Task:           "evaluate concurrent steps",
		ContextSummary: "parallel callers must not duplicate evidence ids",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	for _, workProduct := range []string{"first visible work", "second visible work"} {
		wg.Add(1)
		go func(workProduct string) {
			defer wg.Done()
			_, stepErr := controller.Step(t.Context(), StepRequest{
				SessionID:        start.SessionID,
				ChosenMove:       "critical_thinking",
				WorkProduct:      workProduct,
				Evidence:         []EvidenceRef{{Kind: "file", Ref: workProduct + ".md", Summary: workProduct, VerificationStatus: "verified"}},
				CallerConfidence: 0.7,
			})
			if stepErr != nil {
				t.Errorf("Step(%s): %v", workProduct, stepErr)
			}
		}(workProduct)
	}
	wg.Wait()

	session, err := controller.Session(t.Context(), start.SessionID)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	seen := make(map[string]bool)
	for _, entry := range session.Ledger.Checkable {
		if entry.Source == "first visible work.md" || entry.Source == "second visible work.md" {
			if seen[entry.ID] {
				t.Fatalf("duplicate evidence id %q in ledger: %+v", entry.ID, session.Ledger.Checkable)
			}
			seen[entry.ID] = true
		}
	}
	if len(seen) != 2 {
		t.Fatalf("evidence ids = %v, want 2 unique caller evidence entries", seen)
	}
}
