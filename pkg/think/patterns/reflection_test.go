package patterns

import "testing"

func TestValidateEvidenceGate_Below(t *testing.T) {
	d := ValidateEvidenceGate(2, 3)
	if d == nil {
		t.Fatal("expected STOP directive when findings < required, got nil")
	}
	if d.Directive != "STOP" {
		t.Fatalf("expected directive=STOP, got %q", d.Directive)
	}
	if d.Reason == "" {
		t.Fatal("expected non-empty reason")
	}
	if len(d.Checklist) == 0 {
		t.Fatal("expected non-empty checklist")
	}
}

func TestValidateEvidenceGate_Sufficient(t *testing.T) {
	d := ValidateEvidenceGate(4, 3)
	if d != nil {
		t.Fatalf("expected nil when findings >= required, got %+v", d)
	}
}

func TestValidateEvidenceGate_Equal(t *testing.T) {
	d := ValidateEvidenceGate(3, 3)
	if d != nil {
		t.Fatalf("expected nil when findings == required, got %+v", d)
	}
}

func TestValidateConfidence_Overconfident(t *testing.T) {
	d := ValidateConfidence(0.9, 2)
	if d == nil {
		t.Fatal("expected VERIFY directive for overconfident scenario, got nil")
	}
	if d.Directive != "VERIFY" {
		t.Fatalf("expected directive=VERIFY, got %q", d.Directive)
	}
	if d.Reason == "" {
		t.Fatal("expected non-empty reason")
	}
	if len(d.Checklist) == 0 {
		t.Fatal("expected non-empty checklist")
	}
}

func TestValidateConfidence_Justified(t *testing.T) {
	d := ValidateConfidence(0.9, 6)
	if d != nil {
		t.Fatalf("expected nil when evidence is sufficient, got %+v", d)
	}
}

func TestValidateConfidence_LowConfidence(t *testing.T) {
	// Low confidence with few evidence — no warning needed
	d := ValidateConfidence(0.7, 2)
	if d != nil {
		t.Fatalf("expected nil for low confidence, got %+v", d)
	}
}

func TestValidateConfidence_ExactBoundary(t *testing.T) {
	// Exactly 0.8 confidence is NOT > 0.8, so no directive
	d := ValidateConfidence(0.8, 2)
	if d != nil {
		t.Fatalf("expected nil at exactly 0.8 confidence, got %+v", d)
	}
}
