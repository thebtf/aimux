package investigate

import (
	"strings"
	"testing"
)

func TestAssess_Empty(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("assess-empty", "empty topic", "")

	result, err := Assess("assess-empty")
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}

	if result.Recommendation != "CONTINUE" {
		t.Errorf("Recommendation = %q, want CONTINUE", result.Recommendation)
	}
	if result.CoverageScore != 0 {
		t.Errorf("CoverageScore = %f, want 0", result.CoverageScore)
	}
	if len(result.UncheckedAreas) != 10 {
		t.Errorf("UncheckedAreas = %d, want 10", len(result.UncheckedAreas))
	}
}

func TestAssess_WithFindings(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("assess-findings", "findings topic", "")

	// Add findings covering 8/10 areas
	areas := []string{"source_code", "original_intent", "production_usage", "test_coverage",
		"error_paths", "caller_experience", "performance", "state_management"}
	for _, area := range areas {
		AddFinding("assess-findings", FindingInput{
			Description:  "Finding in " + area,
			Severity:     SeverityP2,
			Source:        "test",
			CoverageArea: area,
		})
	}

	result, err := Assess("assess-findings")
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}

	if result.CoverageScore != 0.8 {
		t.Errorf("CoverageScore = %f, want 0.8", result.CoverageScore)
	}
	if len(result.UncheckedAreas) != 2 {
		t.Errorf("UncheckedAreas = %d, want 2", len(result.UncheckedAreas))
	}
}

func TestAssess_WeakAreas(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("assess-weak", "weak topic", "")

	// Add 1 finding to area A (weak), 2 to area B (not weak)
	AddFinding("assess-weak", FindingInput{
		Description: "Single finding", Severity: SeverityP2, Source: "test", CoverageArea: "source_code",
	})
	AddFinding("assess-weak", FindingInput{
		Description: "First finding", Severity: SeverityP1, Source: "test", CoverageArea: "test_coverage",
	})
	AddFinding("assess-weak", FindingInput{
		Description: "Second finding", Severity: SeverityP2, Source: "test", CoverageArea: "test_coverage",
	})

	result, err := Assess("assess-weak")
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}

	found := false
	for _, area := range result.WeakAreas {
		if area == "source_code" {
			found = true
		}
	}
	if !found {
		t.Errorf("WeakAreas should contain 'source_code', got %v", result.WeakAreas)
	}
}

func TestAssess_ConflictingAreas(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("assess-conflict", "conflict topic", "")

	// Same area with P0 and P3 = conflicting
	AddFinding("assess-conflict", FindingInput{
		Description: "Critical bug", Severity: SeverityP0, Source: "test", CoverageArea: "source_code",
	})
	AddFinding("assess-conflict", FindingInput{
		Description: "Minor style issue", Severity: SeverityP3, Source: "test", CoverageArea: "source_code",
	})

	result, err := Assess("assess-conflict")
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}

	found := false
	for _, area := range result.ConflictingAreas {
		if area == "source_code" {
			found = true
		}
	}
	if !found {
		t.Errorf("ConflictingAreas should contain 'source_code', got %v", result.ConflictingAreas)
	}
}

func TestAssess_AdversarialPrompt(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("assess-adv", "adv topic", "")

	// Cover all 10 areas to get MAY_STOP, with P0 finding
	for i, area := range GenericDomain.CoverageAreas {
		sev := SeverityP2
		if i == 0 {
			sev = SeverityP0
		}
		AddFinding("assess-adv", FindingInput{
			Description:  "Finding " + area,
			Severity:     sev,
			Source:        "test",
			CoverageArea: area,
		})
	}

	// Need to advance iteration to get convergence
	NextIteration("assess-adv")

	result, err := Assess("assess-adv")
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}

	if result.Recommendation != "MAY_STOP" && result.Recommendation != "COMPLETE" {
		t.Errorf("Recommendation = %q, want MAY_STOP or COMPLETE", result.Recommendation)
	}
	if result.AdversarialPrompt == "" {
		t.Error("expected adversarial prompt for P0 findings with MAY_STOP")
	}
}

func TestAssess_AngleRotation(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("assess-angle", "angle topic", "")

	result1, _ := Assess("assess-angle")
	result2, _ := Assess("assess-angle")

	if result1.SuggestedAngle == result2.SuggestedAngle {
		t.Error("angles should rotate between iterations")
	}
}

func TestAssess_SessionNotFound(t *testing.T) {
	ClearAllInvestigations()
	_, err := Assess("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestAssess_ThinkCallSuggested(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("assess-think", "think topic", "")

	result, _ := Assess("assess-think")

	if result.SuggestedThinkCall == "" {
		t.Error("expected non-empty SuggestedThinkCall")
	}
	if !strings.Contains(result.SuggestedThinkCall, "mcp__aimux__think") {
		t.Errorf("SuggestedThinkCall should contain tool name, got %q", result.SuggestedThinkCall)
	}
}
