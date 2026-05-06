package harness

import "testing"

func TestControllerFinalizeAcceptsSupportedRun(t *testing.T) {
	controller := NewController(NewInMemoryStore(), WithIDGenerator(func() string { return "think-finalize-ok" }))
	start := mustStartForFinalize(t, controller)
	mustStepWithEvidence(t, controller, start.SessionID)

	resp, err := controller.Finalize(t.Context(), FinalizeRequest{
		SessionID:      start.SessionID,
		ProposedAnswer: "The supported answer is ready.",
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !resp.CanFinalize || !resp.Accepted {
		t.Fatalf("supported run not accepted: %+v", resp)
	}
	if resp.StopDecision.Action != StopFinalize {
		t.Fatalf("stop action = %q, want finalize", resp.StopDecision.Action)
	}
	if resp.TraceSummary.StopReason == "" {
		t.Fatalf("trace summary missing stop reason: %+v", resp.TraceSummary)
	}
}

func TestControllerFinalizeForceDisclosesNonCriticalObjections(t *testing.T) {
	controller := NewController(NewInMemoryStore(), WithIDGenerator(func() string { return "think-finalize-force" }))
	start := mustStartForFinalize(t, controller)
	mustStepWithEvidence(t, controller, start.SessionID)

	_, err := controller.store.Update(t.Context(), start.SessionID, func(current ThinkingSession) (ThinkingSession, error) {
		return current.ApplyPatch(KnowledgePatch{
			Objections: []Objection{{ID: "major-1", Severity: ObjectionMajor, Text: "non-critical unresolved issue"}},
		})
	})
	if err != nil {
		t.Fatalf("inject objection: %v", err)
	}

	resp, err := controller.Finalize(t.Context(), FinalizeRequest{
		SessionID:      start.SessionID,
		ProposedAnswer: "Forced answer with disclosed objection.",
		ForceFinalize:  true,
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !resp.CanFinalize {
		t.Fatalf("forced non-critical finalization rejected: %+v", resp)
	}
	if len(resp.UnresolvedObjections) == 0 {
		t.Fatalf("forced finalization must disclose unresolved objections: %+v", resp)
	}
}

func mustStartForFinalize(t *testing.T, controller *Controller) StartResponse {
	t.Helper()
	resp, err := controller.Start(t.Context(), StartRequest{
		Task:           "finalize a supported answer",
		ContextSummary: "finalization requires evidence",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return resp
}

func mustStepWithEvidence(t *testing.T, controller *Controller, sessionID string) StepResponse {
	t.Helper()
	resp, err := controller.Step(t.Context(), StepRequest{
		SessionID:        sessionID,
		ChosenMove:       "critical_thinking",
		WorkProduct:      "The answer has visible evidence and a reviewed assumption.",
		Evidence:         []EvidenceRef{{Kind: "file", Ref: "spec.md", Summary: "requirement verified", VerificationStatus: "verified"}},
		CallerConfidence: 0.78,
	})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	return resp
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
