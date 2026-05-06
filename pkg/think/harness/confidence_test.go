package harness

import "testing"

func TestWeakEvidenceCapsConfidence(t *testing.T) {
	review := EvaluateConfidence(ConfidenceInput{
		CallerConfidence: 0.95,
		GateReport:       GateReport{Status: GateWarn, MissingWork: []string{"evidence"}},
	})

	if review.Ceiling > 0.45 {
		t.Fatalf("weak evidence ceiling = %.2f, want <= 0.45", review.Ceiling)
	}
	if review.Tier == "high" {
		t.Fatalf("weak evidence must not be high confidence: %+v", review)
	}
}

func TestVerifiedEvidenceAllowsHigherConfidence(t *testing.T) {
	review := EvaluateConfidence(ConfidenceInput{
		CallerConfidence: 0.8,
		Evidence:         []EvidenceRef{{Kind: "file", Ref: "spec.md", Summary: "verified requirement", VerificationStatus: "verified"}},
		GateReport:       GateReport{Status: GatePass},
	})

	if review.Ceiling < 0.75 {
		t.Fatalf("verified evidence ceiling = %.2f, want >= 0.75", review.Ceiling)
	}
	if review.Tier != "high" {
		t.Fatalf("tier = %q, want high", review.Tier)
	}
}
