package harness

import "testing"

func TestTaskFrameValidation(t *testing.T) {
	if _, err := NewTaskFrame(TaskFrame{}); err == nil {
		t.Fatal("empty task frame accepted")
	}

	frame, err := NewTaskFrame(TaskFrame{
		Task:           "choose the right implementation path",
		Goal:           "ship a caller-centered thinking harness",
		ContextSummary: "CR-002 replaces the base think router",
		SuccessSignal:  "gates block unsupported finalization",
	})
	if err != nil {
		t.Fatalf("valid task frame rejected: %v", err)
	}
	if frame.Task == "" || frame.SuccessSignal == "" {
		t.Fatalf("required fields not preserved: %+v", frame)
	}
}

func TestArtifactPatchUpdatesEveryLoopStage(t *testing.T) {
	frame, err := NewTaskFrame(TaskFrame{
		Task:           "reason about a risky change",
		Goal:           "avoid premature closure",
		ContextSummary: "caller owns the answer",
		SuccessSignal:  "final gates pass",
	})
	if err != nil {
		t.Fatalf("task frame: %v", err)
	}

	session := NewThinkingSession("s1", frame)
	patch := KnowledgePatch{
		Phase: PhaseIntegrate,
		LedgerAdds: KnowledgeLedger{
			Known: []LedgerEntry{{ID: "known-1", Text: "current router is keyword based"}},
		},
		Move: &MovePlan{
			Name:                  "inventory_existing_behavior",
			Group:                 MoveGroupExplore,
			Reason:                "understand what must change",
			ExpectedArtifactDelta: "known ledger gains current behavior",
			Execute:               true,
		},
		Observation: &Observation{
			MoveName:    "inventory_existing_behavior",
			WorkProduct: "found suggestedPattern routing path",
			Evidence: []EvidenceRef{{
				Kind:               "file",
				Ref:                "pkg/think/patterns/think.go",
				Summary:            "base think returns suggestedPattern",
				VerificationStatus: "verified",
			}},
			CallerConfidence: 0.82,
		},
		GateReport: &GateReport{
			Status:      GateBlocked,
			Blockers:    []string{"legacy migration behavior not specified"},
			MissingWork: []string{"choose exact migration response"},
		},
		Objections: []Objection{{
			ID:       "obj-1",
			Severity: ObjectionCritical,
			Text:     "legacy calls could still auto-route",
		}},
		ConfidenceFactors: []ConfidenceFactor{{
			Name:   "file evidence",
			Impact: 0.2,
			Reason: "source path observed",
		}},
		StopDecision: &StopDecision{
			Action:      StopContinue,
			Reason:      "critical objection remains",
			CanFinalize: false,
		},
	}

	updated, err := session.ApplyPatch(patch)
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}

	if updated.Phase != PhaseIntegrate {
		t.Fatalf("phase = %q, want %q", updated.Phase, PhaseIntegrate)
	}
	if len(updated.Ledger.Known) != 1 {
		t.Fatalf("known ledger entries = %d, want 1", len(updated.Ledger.Known))
	}
	if len(updated.MoveHistory) != 1 {
		t.Fatalf("move history entries = %d, want 1", len(updated.MoveHistory))
	}
	if len(updated.Observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(updated.Observations))
	}
	if len(updated.GateReports) != 1 {
		t.Fatalf("gate reports = %d, want 1", len(updated.GateReports))
	}
	if len(updated.Objections) != 1 {
		t.Fatalf("objections = %d, want 1", len(updated.Objections))
	}
	if len(updated.ConfidenceFactors) != 1 {
		t.Fatalf("confidence factors = %d, want 1", len(updated.ConfidenceFactors))
	}
	if updated.StopDecision == nil || updated.StopDecision.CanFinalize {
		t.Fatalf("stop decision not preserved: %+v", updated.StopDecision)
	}

	if len(session.Ledger.Known) != 0 || len(session.MoveHistory) != 0 {
		t.Fatalf("original session mutated: %+v", session)
	}
}

func TestInvalidArtifactsFailClosed(t *testing.T) {
	frame := validFrame(t)
	session := NewThinkingSession("s1", frame)

	_, err := session.ApplyPatch(KnowledgePatch{
		Move: &MovePlan{
			Name:                  "",
			Group:                 MoveGroupExplore,
			Reason:                "missing name should fail",
			ExpectedArtifactDelta: "none",
		},
	})
	if err == nil {
		t.Fatal("invalid move plan accepted")
	}

	_, err = session.ApplyPatch(KnowledgePatch{
		Observation: &Observation{
			MoveName:         "observe",
			WorkProduct:      "",
			CallerConfidence: 0.5,
		},
	})
	if err == nil {
		t.Fatal("empty work product accepted")
	}
}

func validFrame(t *testing.T) TaskFrame {
	t.Helper()

	frame, err := NewTaskFrame(TaskFrame{
		Task:           "frame a task",
		Goal:           "produce a useful result",
		ContextSummary: "test context",
		SuccessSignal:  "typed artifacts change",
	})
	if err != nil {
		t.Fatalf("valid frame: %v", err)
	}
	return frame
}
