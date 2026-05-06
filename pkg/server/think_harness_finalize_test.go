package server

import (
	"context"
	"testing"
)

func TestThinkHarnessFinalizeBlocksPrematureAnswer(t *testing.T) {
	srv := testServer(t)
	sessionID := startThinkHarnessSession(t, srv)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":          "finalize",
		"session_id":      sessionID,
		"proposed_answer": "too early",
	}))
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if result.IsError {
		t.Fatalf("premature finalize should be a gate response, not tool error: %+v", result.Content)
	}
	payload := parseResult(t, result)
	if payload["can_finalize"] != false {
		t.Fatalf("can_finalize = %v, want false; payload=%v", payload["can_finalize"], payload)
	}
	if _, ok := payload["missing_gates"]; !ok {
		t.Fatalf("missing_gates absent: %v", payload)
	}
}

func TestThinkHarnessFinalizeAcceptsSupportedAnswer(t *testing.T) {
	srv := testServer(t)
	sessionID := startThinkHarnessSession(t, srv)
	stepThinkHarnessWithEvidence(t, srv, sessionID)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":          "finalize",
		"session_id":      sessionID,
		"proposed_answer": "supported answer",
	}))
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if result.IsError {
		t.Fatalf("supported finalize returned tool error: %+v", result.Content)
	}
	payload := parseResult(t, result)
	if payload["can_finalize"] != true {
		t.Fatalf("can_finalize = %v, want true; payload=%v", payload["can_finalize"], payload)
	}
	if _, ok := payload["trace_summary"]; !ok {
		t.Fatalf("trace_summary absent: %v", payload)
	}
}

func stepThinkHarnessWithEvidence(t *testing.T, srv *Server, sessionID string) {
	t.Helper()
	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":       "step",
		"session_id":   sessionID,
		"chosen_move":  "critical_thinking",
		"work_product": "The answer has visible evidence.",
		"confidence":   0.75,
		"evidence": []any{map[string]any{
			"kind":                "file",
			"ref":                 "spec.md",
			"summary":             "requirement verified",
			"verification_status": "verified",
		}},
	}))
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if result.IsError {
		t.Fatalf("step returned error: %+v", result.Content)
	}
}
