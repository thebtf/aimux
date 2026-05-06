package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTraceSerializationIncludesVisibleArtifacts(t *testing.T) {
	session := NewThinkingSession("trace-1", validFrame(t))
	updated, err := session.ApplyPatch(KnowledgePatch{
		Move: &MovePlan{
			Name:                  "observe_current_code",
			Group:                 MoveGroupExplore,
			Reason:                "need evidence",
			ExpectedArtifactDelta: "observation added",
			Execute:               true,
		},
		Observation: &Observation{
			MoveName:    "observe_current_code",
			WorkProduct: "visible summary of the current think router",
			Evidence: []EvidenceRef{{
				Kind:               "file",
				Ref:                "pkg/think/patterns/think.go",
				Summary:            "suggestedPattern exists",
				VerificationStatus: "verified",
			}},
			CallerConfidence: 0.7,
		},
		GateReport: &GateReport{
			Status:   GateWarn,
			Warnings: []string{"needs migration contract"},
		},
		ConfidenceFactors: []ConfidenceFactor{{
			Name:   "source check",
			Impact: 0.15,
			Reason: "direct file evidence",
		}},
		StopDecision: &StopDecision{
			Action:      StopContinue,
			Reason:      "more work needed",
			CanFinalize: false,
		},
	})
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}

	trace := NewThinkingRunTrace(updated)
	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	text := string(raw)

	for _, want := range []string{
		"visible summary of the current think router",
		"pkg/think/patterns/think.go",
		"needs migration contract",
		"source check",
		"more work needed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace JSON missing %q: %s", want, text)
		}
	}
}

func TestTraceSerializationExcludesHiddenReasoning(t *testing.T) {
	session := NewThinkingSession("trace-privacy", validFrame(t))
	trace := NewThinkingRunTrace(session)

	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{
		"hidden_reasoning",
		"private_reasoning",
		"chain_of_thought",
		"raw_cot",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("trace JSON contains forbidden field %q: %s", forbidden, lower)
		}
	}
}
