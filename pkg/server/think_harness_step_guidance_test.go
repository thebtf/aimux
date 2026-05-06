package server

import (
	"context"
	"testing"
)

func TestThinkHarnessStepGuidanceOnlyThroughPublicTool(t *testing.T) {
	srv := testServer(t)
	sessionID := startThinkHarnessSession(t, srv)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":      "step",
		"session_id":  sessionID,
		"chosen_move": "source_comparison",
		"execute":     false,
	}))
	if err != nil {
		t.Fatalf("handleThinkHarness guidance step: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected guidance error: %+v", result.Content)
	}
	payload := parseResult(t, result)
	if payload["executed"] != false {
		t.Fatalf("executed = %v, want false; payload=%v", payload["executed"], payload)
	}
	if _, ok := payload["required_report_back"]; !ok {
		t.Fatalf("guidance response missing required_report_back: %v", payload)
	}
}

func TestThinkHarnessStepMalformedEvidenceFailsClosed(t *testing.T) {
	srv := testServer(t)
	sessionID := startThinkHarnessSession(t, srv)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":       "step",
		"session_id":   sessionID,
		"chosen_move":  "critical_thinking",
		"work_product": "visible work",
		"evidence":     []any{map[string]any{"kind": "file"}},
	}))
	if err != nil {
		t.Fatalf("handleThinkHarness malformed evidence: %v", err)
	}
	if !result.IsError {
		t.Fatalf("malformed evidence should fail closed: %+v", result)
	}
	payload := parseResult(t, result)
	if payload["code"] != "invalid_input" {
		t.Fatalf("code = %v, want invalid_input; payload=%v", payload["code"], payload)
	}
}
