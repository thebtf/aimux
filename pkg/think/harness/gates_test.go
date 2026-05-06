package harness

import "testing"

func TestFinalizationGateRequiresFullLoop(t *testing.T) {
	controller := NewController(NewInMemoryStore(), WithIDGenerator(func() string { return "think-finalize-missing-loop" }))
	start, err := controller.Start(t.Context(), StartRequest{
		Task:           "finalize too early",
		ContextSummary: "no move has executed",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := controller.Finalize(t.Context(), FinalizeRequest{
		SessionID:      start.SessionID,
		ProposedAnswer: "ship it",
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if resp.CanFinalize {
		t.Fatalf("premature finalize accepted: %+v", resp)
	}
	if !containsString(resp.MissingGates, "full_loop") {
		t.Fatalf("missing gates = %v, want full_loop", resp.MissingGates)
	}
}

func TestFinalizationGateBlocksCriticalObjection(t *testing.T) {
	controller := NewController(NewInMemoryStore(), WithIDGenerator(func() string { return "think-finalize-critical" }))
	start := mustStartForFinalize(t, controller)
	mustStepWithEvidence(t, controller, start.SessionID)

	_, err := controller.store.Update(t.Context(), start.SessionID, func(current ThinkingSession) (ThinkingSession, error) {
		return current.ApplyPatch(KnowledgePatch{
			Objections: []Objection{{ID: "critical-1", Severity: ObjectionCritical, Text: "unresolved critical blocker"}},
		})
	})
	if err != nil {
		t.Fatalf("inject objection: %v", err)
	}

	resp, err := controller.Finalize(t.Context(), FinalizeRequest{
		SessionID:      start.SessionID,
		ProposedAnswer: "ship it",
		ForceFinalize:  true,
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if resp.CanFinalize {
		t.Fatalf("critical objection should block even forced finalization: %+v", resp)
	}
	if !containsString(resp.MissingGates, "critical_objections") {
		t.Fatalf("missing gates = %v, want critical_objections", resp.MissingGates)
	}
}
