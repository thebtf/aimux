package think

import (
	"strings"
	"testing"
)

func TestGenerateSummary_Success(t *testing.T) {
	r := MakeThinkResult("think", map[string]any{
		"thought":          "We should refactor the auth module",
		"suggestedPattern": "architecture_analysis",
		"keywords":         []string{"refactor", "auth", "module"},
	}, "", nil, "architecture_analysis", nil)

	summary := GenerateSummary(r, "solo")
	if !strings.Contains(summary, "[think]") {
		t.Errorf("summary should contain pattern name, got: %s", summary)
	}
	if !strings.Contains(summary, "architecture_analysis") {
		t.Errorf("summary should mention suggested pattern, got: %s", summary)
	}
	if !strings.Contains(summary, "solo") {
		t.Errorf("summary should contain mode, got: %s", summary)
	}
}

func TestGenerateSummary_Failed(t *testing.T) {
	r := MakeErrorResult("critical_thinking", "missing required field: issue")
	summary := GenerateSummary(r, "")
	if !strings.Contains(summary, "Failed") {
		t.Errorf("summary should indicate failure, got: %s", summary)
	}
	if !strings.Contains(summary, "missing required field") {
		t.Errorf("summary should include error message, got: %s", summary)
	}
}

func TestGenerateSummary_PresetSummary(t *testing.T) {
	r := MakeThinkResult("decision_framework", map[string]any{}, "", nil, "", nil)
	r.Summary = "Choose option A for lower latency."

	summary := GenerateSummary(r, "consensus")
	if summary != "Choose option A for lower latency." {
		t.Errorf("should return preset summary, got: %s", summary)
	}
}

func TestGenerateSummary_WithDecision(t *testing.T) {
	r := MakeThinkResult("decision_framework", map[string]any{
		"decision": "Use PostgreSQL over MySQL for better JSON support",
	}, "", nil, "", nil)

	summary := GenerateSummary(r, "")
	if !strings.Contains(summary, "PostgreSQL") {
		t.Errorf("summary should include decision text, got: %s", summary)
	}
}

func TestGenerateSummary_TruncatesLongText(t *testing.T) {
	longText := strings.Repeat("x", 200)
	r := MakeThinkResult("think", map[string]any{
		"thought": longText,
	}, "", nil, "", nil)

	summary := GenerateSummary(r, "")
	if len(summary) > 200 {
		t.Errorf("summary should be truncated, got length %d", len(summary))
	}
}
