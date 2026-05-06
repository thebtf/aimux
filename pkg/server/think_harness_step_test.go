package server

import (
	"context"
	"testing"
)

func TestThinkHarnessStepThroughPublicTool(t *testing.T) {
	srv := testServer(t)
	sessionID := startThinkHarnessSession(t, srv)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":       "step",
		"session_id":   sessionID,
		"chosen_move":  "critical_thinking",
		"work_product": "The answer needs one more verified source.",
		"confidence":   0.72,
		"evidence": []any{
			map[string]any{
				"kind":                "file",
				"ref":                 "spec.md",
				"summary":             "requirement is visible",
				"verification_status": "verified",
			},
		},
	}))
	if err != nil {
		t.Fatalf("handleThinkHarness step: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected step error: %+v", result.Content)
	}
	payload := parseResult(t, result)
	for _, field := range []string{
		"gate_report",
		"confidence_ceiling",
		"unresolved_objections",
		"allowed_move_groups",
		"recommended_moves",
	} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("step response missing %q: %v", field, payload)
		}
	}
}

func TestThinkHarnessStepInvalidSessionFailsClosed(t *testing.T) {
	srv := testServer(t)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":       "step",
		"session_id":   "missing",
		"chosen_move":  "critical_thinking",
		"work_product": "visible work",
	}))
	if err != nil {
		t.Fatalf("handleThinkHarness step: %v", err)
	}
	if !result.IsError {
		t.Fatalf("invalid session should fail closed: %+v", result)
	}
	payload := parseResult(t, result)
	if payload["code"] != "unknown_session" {
		t.Fatalf("code = %v, want unknown_session; payload=%v", payload["code"], payload)
	}
}

func startThinkHarnessSession(t *testing.T, srv *Server) string {
	t.Helper()

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":          "start",
		"task":            "step test task",
		"context_summary": "step test context",
	}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.IsError {
		t.Fatalf("start returned error: %+v", result.Content)
	}
	payload := parseResult(t, result)
	sessionID, ok := payload["session_id"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("missing session_id in start payload: %v", payload)
	}
	return sessionID
}
