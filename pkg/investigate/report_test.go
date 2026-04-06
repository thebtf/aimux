package investigate

import (
	"strings"
	"testing"
)

func TestGenerateReport_Basic(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("report-basic", "basic report topic", "")

	AddFinding("report-basic", FindingInput{
		Description: "Found a bug", Severity: SeverityP1, Source: "Read pkg/server.go",
		Confidence: ConfidenceVerified, CoverageArea: "source_code",
	})
	AddFinding("report-basic", FindingInput{
		Description: "Possible issue", Severity: SeverityP2, Source: "Inferred from logs",
		Confidence: ConfidenceInferred, CoverageArea: "error_paths",
	})

	state := GetInvestigation("report-basic")
	report := GenerateReport(state)

	// Check header
	if !strings.Contains(report, "# Investigation Report: basic report topic") {
		t.Error("missing report title")
	}
	if !strings.Contains(report, "**Generated:**") {
		t.Error("missing Generated metadata")
	}
	if !strings.Contains(report, "**Coverage:**") {
		t.Error("missing Coverage metadata")
	}
	if !strings.Contains(report, "**Confidence:**") {
		t.Error("missing Confidence aggregate")
	}

	// Check findings table has confidence column
	if !strings.Contains(report, "| Confidence |") {
		t.Error("missing Confidence column in findings table")
	}

	// Check sections exist
	if !strings.Contains(report, "## What to Be Skeptical Of") {
		t.Error("missing skepticism section")
	}
	if !strings.Contains(report, "## Coverage Map") {
		t.Error("missing coverage map")
	}
	if !strings.Contains(report, "## Key Takeaways") {
		t.Error("missing key takeaways")
	}
}

func TestGenerateReport_SkepticismSection(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("report-skeptic", "skeptic topic", "")

	AddFinding("report-skeptic", FindingInput{
		Description: "Inferred finding", Severity: SeverityP2, Source: "inference",
		Confidence: ConfidenceInferred,
	})

	state := GetInvestigation("report-skeptic")
	report := GenerateReport(state)

	if !strings.Contains(report, "INFERRED evidence") {
		t.Error("skepticism section should mention INFERRED findings")
	}
	if !strings.Contains(report, "Inferred finding") {
		t.Error("skepticism section should list the inferred finding")
	}
}

func TestGenerateReport_CompletenessWarning(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("report-warn", "warn topic", "")

	// Only 1 of 10 areas covered → <80%
	AddFinding("report-warn", FindingInput{
		Description: "One finding", Severity: SeverityP2, Source: "test",
		CoverageArea: "source_code",
	})

	state := GetInvestigation("report-warn")
	report := GenerateReport(state)

	if !strings.Contains(report, "WARNING") {
		t.Error("expected completeness warning for <80% coverage")
	}
	if !strings.Contains(report, "Report may be incomplete") {
		t.Error("warning should mention incomplete report")
	}
}

func TestGenerateReport_NoWarningAtHighCoverage(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("report-ok", "ok topic", "")

	// Cover 9/10 areas → 90% → no warning
	for _, area := range GenericDomain.CoverageAreas[:9] {
		AddFinding("report-ok", FindingInput{
			Description: "Finding " + area, Severity: SeverityP2, Source: "test",
			CoverageArea: area,
		})
	}

	state := GetInvestigation("report-ok")
	report := GenerateReport(state)

	if strings.Contains(report, "WARNING") {
		t.Error("should not warn at 90% coverage")
	}
}

func TestGenerateReport_WithCorrections(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("report-corr", "correction topic", "")

	AddFinding("report-corr", FindingInput{
		Description: "Root cause is X", Severity: SeverityP1, Source: "initial",
	})
	AddFinding("report-corr", FindingInput{
		Description: "Root cause is Y", Severity: SeverityP0, Source: "deeper",
		Corrects: "F-0-1",
	})

	state := GetInvestigation("report-corr")
	report := GenerateReport(state)

	if !strings.Contains(report, "## Corrections") {
		t.Error("missing corrections section")
	}
	if !strings.Contains(report, "corrected by F-0-2") {
		t.Error("original finding should show corrected status")
	}
}

func TestGenerateReport_KeyTakeaways(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("report-take", "takeaway topic", "")

	AddFinding("report-take", FindingInput{
		Description: "Critical bug found", Severity: SeverityP0, Source: "Read code",
		Confidence: ConfidenceVerified,
	})
	AddFinding("report-take", FindingInput{
		Description: "Possible side effect", Severity: SeverityP2, Source: "Inferred",
		Confidence: ConfidenceInferred,
	})

	state := GetInvestigation("report-take")
	report := GenerateReport(state)

	if !strings.Contains(report, "**Root cause:**") {
		t.Error("missing root cause in takeaways")
	}
	if !strings.Contains(report, "**Key recommendation:**") {
		t.Error("missing recommendation in takeaways")
	}
	if !strings.Contains(report, "**Watch out:**") {
		t.Error("missing watch-out in takeaways")
	}
}

func TestGenerateReport_EmptyInvestigation(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("report-empty", "empty topic", "")

	state := GetInvestigation("report-empty")
	report := GenerateReport(state)

	// Should still generate without panic
	if !strings.Contains(report, "# Investigation Report") {
		t.Error("empty investigation should still produce report")
	}
	if !strings.Contains(report, "WARNING") {
		t.Error("0 findings = 0% coverage → should warn")
	}
}

func TestGenerateReport_MetadataFields(t *testing.T) {
	ClearAllInvestigations()
	CreateInvestigation("report-meta", "meta topic", "")

	state := GetInvestigation("report-meta")
	report := GenerateReport(state)

	if !strings.Contains(report, "**Model:**") {
		t.Error("missing Model metadata")
	}
	if !strings.Contains(report, "**Session:**") {
		t.Error("missing Session metadata")
	}
	if !strings.Contains(report, "**Iterations:**") {
		t.Error("missing Iterations metadata")
	}
}
