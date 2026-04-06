package investigate

import (
	"testing"
)

func TestCreateInvestigation_Generic(t *testing.T) {
	ClearAllInvestigations()
	state := CreateInvestigation("test-1", "test topic", "")

	if state.Topic != "test topic" {
		t.Errorf("Topic = %q, want 'test topic'", state.Topic)
	}
	if state.Domain != "generic" {
		t.Errorf("Domain = %q, want 'generic'", state.Domain)
	}
	if len(state.CoverageAreas) != 10 {
		t.Errorf("CoverageAreas = %d, want 10", len(state.CoverageAreas))
	}
	if state.Iteration != 0 {
		t.Errorf("Iteration = %d, want 0", state.Iteration)
	}
}

func TestCreateInvestigation_Debugging(t *testing.T) {
	ClearAllInvestigations()
	state := CreateInvestigation("test-2", "debug topic", "debugging")

	if state.Domain != "debugging" {
		t.Errorf("Domain = %q, want 'debugging'", state.Domain)
	}
	if len(state.CoverageAreas) != 8 {
		t.Errorf("CoverageAreas = %d, want 8", len(state.CoverageAreas))
	}
}

func TestGetInvestigation(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("test-get", "get topic", "")

	state := GetInvestigation("test-get")
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if state.Topic != "get topic" {
		t.Errorf("Topic = %q, want 'get topic'", state.Topic)
	}

	if GetInvestigation("nonexistent") != nil {
		t.Error("expected nil for nonexistent session")
	}
}

func TestAddFinding_Basic(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("test-find", "finding topic", "")

	finding, correction, err := AddFinding("test-find", FindingInput{
		Description: "Found a bug",
		Severity:    SeverityP1,
		Source:      "Read pkg/server.go:42",
	})
	if err != nil {
		t.Fatalf("AddFinding: %v", err)
	}
	if finding.ID != "F-0-1" {
		t.Errorf("ID = %q, want 'F-0-1'", finding.ID)
	}
	if finding.Confidence != ConfidenceVerified {
		t.Errorf("Confidence = %q, want VERIFIED (default)", finding.Confidence)
	}
	if correction != nil {
		t.Error("expected nil correction for new finding")
	}

	state := GetInvestigation("test-find")
	if len(state.Findings) != 1 {
		t.Errorf("Findings count = %d, want 1", len(state.Findings))
	}
}

func TestAddFinding_WithConfidence(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("test-conf", "conf topic", "")

	finding, _, err := AddFinding("test-conf", FindingInput{
		Description: "Inferred from logs",
		Severity:    SeverityP2,
		Source:      "Log analysis",
		Confidence:  ConfidenceInferred,
	})
	if err != nil {
		t.Fatalf("AddFinding: %v", err)
	}
	if finding.Confidence != ConfidenceInferred {
		t.Errorf("Confidence = %q, want INFERRED", finding.Confidence)
	}
}

func TestAddFinding_Dedup(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("test-dedup", "dedup topic", "")

	AddFinding("test-dedup", FindingInput{
		Description: "Same finding",
		Severity:    SeverityP1,
		Source:      "source1",
	})

	finding, _, err := AddFinding("test-dedup", FindingInput{
		Description: "Same finding",
		Severity:    SeverityP0,
		Source:      "source2",
	})
	if err != nil {
		t.Fatalf("AddFinding: %v", err)
	}

	// Should return existing finding, not create new
	if finding.ID != "F-0-1" {
		t.Errorf("Dedup should return original finding, got ID %q", finding.ID)
	}

	state := GetInvestigation("test-dedup")
	if len(state.Findings) != 1 {
		t.Errorf("Findings count = %d, want 1 (dedup)", len(state.Findings))
	}
}

func TestAddFinding_Correction(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("test-correct", "correction topic", "")

	AddFinding("test-correct", FindingInput{
		Description: "Root cause is X",
		Severity:    SeverityP1,
		Source:      "initial analysis",
	})

	finding, correction, err := AddFinding("test-correct", FindingInput{
		Description:  "Root cause is actually Y",
		Severity:     SeverityP0,
		Source:        "deeper analysis",
		Corrects:     "F-0-1",
		CoverageArea: "source_code",
	})
	if err != nil {
		t.Fatalf("AddFinding: %v", err)
	}
	if finding.ID != "F-0-2" {
		t.Errorf("ID = %q, want 'F-0-2'", finding.ID)
	}
	if correction == nil {
		t.Fatal("expected non-nil correction")
	}
	if correction.OriginalID != "F-0-1" {
		t.Errorf("OriginalID = %q, want 'F-0-1'", correction.OriginalID)
	}

	state := GetInvestigation("test-correct")
	// Original should be marked as corrected
	if state.Findings[0].CorrectedBy != "F-0-2" {
		t.Errorf("Original CorrectedBy = %q, want 'F-0-2'", state.Findings[0].CorrectedBy)
	}
	if len(state.Corrections) != 1 {
		t.Errorf("Corrections count = %d, want 1", len(state.Corrections))
	}
}

func TestAddFinding_SessionNotFound(t *testing.T) {
	ClearAllInvestigations()
	_, _, err := AddFinding("nonexistent", FindingInput{
		Description: "test",
		Severity:    SeverityP1,
		Source:      "test",
	})
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestAddFinding_CorrectNotFound(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("test-bad-correct", "topic", "")

	_, _, err := AddFinding("test-bad-correct", FindingInput{
		Description: "test",
		Severity:    SeverityP1,
		Source:      "test",
		Corrects:    "F-99-99",
	})
	if err == nil {
		t.Error("expected error for correcting nonexistent finding")
	}
}

func TestAddFinding_CoverageUpdate(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("test-coverage", "coverage topic", "")

	AddFinding("test-coverage", FindingInput{
		Description:  "found in source_code",
		Severity:     SeverityP2,
		Source:        "Read",
		CoverageArea: "source_code",
	})

	state := GetInvestigation("test-coverage")
	if !state.CoverageChecked["source_code"] {
		t.Error("source_code should be marked as checked")
	}
}

func TestNextIteration(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("test-iter", "iter topic", "")

	err := NextIteration("test-iter")
	if err != nil {
		t.Fatalf("NextIteration: %v", err)
	}

	state := GetInvestigation("test-iter")
	if state.Iteration != 1 {
		t.Errorf("Iteration = %d, want 1", state.Iteration)
	}
	if len(state.ConvergenceHistory) != 1 {
		t.Errorf("ConvergenceHistory len = %d, want 1", len(state.ConvergenceHistory))
	}
}

func TestListInvestigations(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("list-1", "topic 1", "")
	CreateInvestigation("list-2", "topic 2", "debugging")

	list := ListInvestigations()
	if len(list) != 2 {
		t.Errorf("ListInvestigations = %d, want 2", len(list))
	}
}

func TestDeleteInvestigation(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("test-delete", "delete topic", "")

	DeleteInvestigation("test-delete")

	if GetInvestigation("test-delete") != nil {
		t.Error("expected nil after delete")
	}
}

func TestComputeConvergence(t *testing.T) {
	// Iteration 0: uncertain (0.5)
	state := &InvestigationState{Iteration: 0}
	if c := ComputeConvergence(state); c != 0.5 {
		t.Errorf("Convergence at iter 0 = %f, want 0.5", c)
	}

	// No findings this iteration: converged (1.0)
	state = &InvestigationState{Iteration: 1}
	if c := ComputeConvergence(state); c != 1.0 {
		t.Errorf("Convergence with 0 findings = %f, want 1.0", c)
	}

	// 2 findings, 1 correction: convergence = 0.5
	state = &InvestigationState{
		Iteration: 1,
		Findings: []Finding{
			{Iteration: 1}, {Iteration: 1},
		},
		Corrections: []Correction{
			{Iteration: 1},
		},
	}
	if c := ComputeConvergence(state); c != 0.5 {
		t.Errorf("Convergence with 1/2 corrections = %f, want 0.5", c)
	}
}

func TestComputeCoverage(t *testing.T) {
	// No areas: 100%
	state := &InvestigationState{CoverageAreas: []string{}, CoverageChecked: map[string]bool{}}
	if c := ComputeCoverage(state); c != 1.0 {
		t.Errorf("Coverage with 0 areas = %f, want 1.0", c)
	}

	// 2/4 areas checked: 50%
	state = &InvestigationState{
		CoverageAreas:   []string{"a", "b", "c", "d"},
		CoverageChecked: map[string]bool{"a": true, "c": true},
	}
	if c := ComputeCoverage(state); c != 0.5 {
		t.Errorf("Coverage with 2/4 = %f, want 0.5", c)
	}
}

func TestGetDomain(t *testing.T) {
	if d := GetDomain(""); d == nil || d.Name != "generic" {
		t.Error("empty name should return generic domain")
	}
	if d := GetDomain("debugging"); d == nil || d.Name != "debugging" {
		t.Error("debugging should return debugging domain")
	}
	if d := GetDomain("nonexistent"); d != nil {
		t.Error("nonexistent should return nil")
	}
}

func TestDomainNames(t *testing.T) {
	names := DomainNames()
	if len(names) < 2 {
		t.Errorf("DomainNames = %d, want >= 2", len(names))
	}
}
