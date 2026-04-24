package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestE2E_ThinkAdvisor_EnrichedResponse verifies that a per-pattern think tool
// call returns the gate_status and advisor_recommendation fields injected by
// the Phase 3 enforcement gate + pattern advisor.
//
// Uses the debugging_approach pattern with a hypothesis param so the pattern
// handler accepts the input without a validation error.
//
// Skipped in -short mode and on CI because the daemon+shim pair is required
// and is flaky under CI scheduler jitter.
func TestE2E_ThinkAdvisor_EnrichedResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("TestE2E_ThinkAdvisor_EnrichedResponse: skipped in -short mode (daemon+shim e2e)")
	}
	if os.Getenv("CI") != "" {
		t.Skip("TestE2E_ThinkAdvisor_EnrichedResponse: skipped on CI (daemon+shim e2e flaky under scheduler jitter)")
	}

	stdin, reader := initTestCLIServer(t)

	// Call the debugging_approach pattern tool with a hypothesis param.
	// A stateless invocation (no session_id) always results in gate_status="complete".
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "debugging_approach",
		"arguments": map[string]any{
			"hypothesis": "test hypothesis for advisor e2e verification",
		},
	}))

	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("debugging_approach: %v", err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", resp["error"])
	}

	// The response is wrapped in the guidance envelope; domain fields are under
	// the "result" key, matching the shape verified in TestE2E_ThinkTool.
	data := extractToolJSON(t, resp)

	inner, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested result payload under 'result' key, got %T: %v", data["result"], data)
	}

	// gate_status must be present and valid.
	gateStatus, ok := inner["gate_status"].(string)
	if !ok {
		t.Fatalf("gate_status not present or not a string in result: %v", inner)
	}
	if gateStatus != "complete" && gateStatus != "incomplete" {
		t.Errorf("gate_status = %q, want complete or incomplete", gateStatus)
	}
	// Stateless invocation (no session_id) must always return complete.
	if gateStatus != "complete" {
		t.Errorf("stateless think call: gate_status = %q, want complete", gateStatus)
	}

	// advisor_recommendation must be present with an action field.
	rec, ok := inner["advisor_recommendation"].(map[string]any)
	if !ok {
		t.Fatalf("advisor_recommendation not present or not a map in result: %v", inner)
	}
	action, ok := rec["action"].(string)
	if !ok {
		t.Fatalf("advisor_recommendation.action not a string: %v", rec)
	}
	if action != "continue" && action != "switch" {
		t.Errorf("advisor_recommendation.action = %q, want continue or switch", action)
	}
}
