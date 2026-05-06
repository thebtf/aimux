package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTraceFixtureBasic(t *testing.T) {
	trace := basicTraceFixture(t)
	raw, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}

	expectedPath := filepath.Join("testdata", "trace_basic.json")
	expected, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read %s: %v", expectedPath, err)
	}
	if normalizeFixtureJSON(string(raw)) != normalizeFixtureJSON(string(expected)) {
		t.Fatalf("trace fixture drifted.\nGot:\n%s\nWant:\n%s", raw, expected)
	}
}

func normalizeFixtureJSON(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
}

func TestTraceFixtureBasicContainsAuditFields(t *testing.T) {
	trace := basicTraceFixture(t)
	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`"phase":"finalize"`,
		`"evidence_count":1`,
		`"gate_reports"`,
		`"confidence_factors"`,
		`"stop_decision"`,
		"Visible supported answer.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace JSON missing %q: %s", want, text)
		}
	}
	for _, forbidden := range []string{
		"hidden_reasoning",
		"private_reasoning",
		"chain_of_thought",
		"raw_cot",
	} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("trace JSON contains forbidden private reasoning field %q: %s", forbidden, text)
		}
	}
}

func basicTraceFixture(t *testing.T) ThinkingRunTrace {
	t.Helper()

	session := NewThinkingSession("trace-basic", validFrame(t))
	updated, err := session.ApplyPatch(KnowledgePatch{
		Phase: PhaseFinalize,
		LedgerAdds: KnowledgeLedger{
			Known: []LedgerEntry{{
				ID:     "answer",
				Text:   "Visible supported answer.",
				Source: "caller",
				Status: "observed",
			}},
		},
		Move: &MovePlan{
			Name:                  "critical_thinking",
			Group:                 MoveGroupEvaluate,
			Reason:                "test the answer before accepting it",
			ExpectedArtifactDelta: "gate report and confidence factors update",
			Execute:               true,
		},
		Observation: &Observation{
			MoveName:    "critical_thinking",
			WorkProduct: "Visible supported answer.",
			Evidence: []EvidenceRef{{
				Kind:               "file",
				Ref:                "spec.md",
				Summary:            "finalization requires visible support",
				VerificationStatus: "verified",
			}},
			CallerConfidence: 0.82,
		},
		GateReport: &GateReport{Status: GatePass},
		ConfidenceFactors: []ConfidenceFactor{{
			Name:   "visible_evidence",
			Impact: 0.2,
			Reason: "verified evidence supports finalization",
		}},
		StopDecision: &StopDecision{
			Action:      StopFinalize,
			Reason:      "finalization accepted with visible evidence",
			CanFinalize: true,
		},
	})
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	return NewThinkingRunTrace(updated)
}
