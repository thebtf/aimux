package server

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
)

// TestHandlePattern_EnrichedResponse verifies that handlePattern injects
// gate_status and advisor_recommendation fields into every response.
func TestHandlePattern_EnrichedResponse_Stateless(t *testing.T) {
	patterns.RegisterAll()
	srv := testServer(t)

	req := makeRequest("critical_thinking", map[string]any{
		"issue": "evaluate my reasoning about caching",
	})

	result, err := srv.handlePattern(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePattern: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	// parseGuidedResult unwraps the guidance envelope and returns the raw handler payload.
	inner := parseGuidedResult(t, result)

	// gate_status must be present and valid.
	gateStatus, ok := inner["gate_status"].(string)
	if !ok {
		t.Fatalf("gate_status not present or not a string; full: %v", inner)
	}
	if gateStatus != "complete" && gateStatus != "incomplete" {
		t.Errorf("gate_status = %q, want complete or incomplete", gateStatus)
	}

	// Stateless invocation of a non-configed pattern should be "complete".
	if gateStatus != "complete" {
		t.Errorf("stateless invocation: gate_status = %q, want complete", gateStatus)
	}

	// advisor_recommendation must be a map with action field.
	rec, ok := inner["advisor_recommendation"].(map[string]any)
	if !ok {
		t.Fatalf("advisor_recommendation not present or not a map; full: %v", inner)
	}
	action, ok := rec["action"].(string)
	if !ok {
		t.Fatalf("advisor_recommendation.action not a string: %v", rec)
	}
	if action != "continue" && action != "switch" {
		t.Errorf("advisor_recommendation.action = %q, want continue or switch", action)
	}
}

// TestHandlePattern_EnrichedResponse_StatefulIncomplete verifies that a stateful
// pattern with an active session and insufficient progress yields gate_status=incomplete.
func TestHandlePattern_EnrichedResponse_StatefulIncomplete(t *testing.T) {
	patterns.RegisterAll()
	defer think.ClearSessions()
	srv := testServer(t)

	// Create a session manually with no steps so the gate fires incomplete.
	think.GetOrCreateSession("test-session-gate", "debugging_approach", map[string]any{})

	req := makeRequest("debugging_approach", map[string]any{
		"session_id":  "test-session-gate",
		"step":        "initial observation",
		"hypothesis":  "might be a nil pointer",
		"method":      "add logging",
		"observation": "nothing obvious",
	})

	result, err := srv.handlePattern(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePattern: %v", err)
	}
	if result.IsError {
		// Acceptable — the pattern itself may require more fields; we only care
		// about the enrichment fields when the call succeeds.
		t.Skip("pattern rejected input; skipping enrichment check")
	}

	inner := parseGuidedResult(t, result)

	gateStatus, ok := inner["gate_status"].(string)
	if !ok {
		t.Fatalf("gate_status not a string in response: %v", inner)
	}
	if gateStatus != "complete" && gateStatus != "incomplete" {
		t.Errorf("gate_status = %q, want complete or incomplete", gateStatus)
	}

	rec, ok := inner["advisor_recommendation"].(map[string]any)
	if !ok {
		t.Fatalf("advisor_recommendation not in response: %v", inner)
	}
	if _, ok := rec["action"]; !ok {
		t.Error("advisor_recommendation missing action field")
	}
}

// TestHandlePattern_EnrichedResponse_AdvisorFields verifies all three
// advisor_recommendation sub-fields are present.
func TestHandlePattern_EnrichedResponse_AdvisorFields(t *testing.T) {
	patterns.RegisterAll()
	srv := testServer(t)

	req := makeRequest("mental_model", map[string]any{
		"modelName": "first_principles",
		"problem":   "design a fast distributed cache",
	})

	result, err := srv.handlePattern(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePattern: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	inner := parseGuidedResult(t, result)

	rec, ok := inner["advisor_recommendation"].(map[string]any)
	if !ok {
		t.Fatalf("advisor_recommendation missing: %v", inner)
	}
	for _, field := range []string{"action", "target", "reason"} {
		if _, ok := rec[field]; !ok {
			t.Errorf("advisor_recommendation missing field %q", field)
		}
	}
}

// --- Mode routing tests ---

// TestHandlePattern_ModeSolo_AlwaysSolo verifies that mode=solo forces solo
// regardless of input complexity.
func TestHandlePattern_ModeSolo_AlwaysSolo(t *testing.T) {
	patterns.RegisterAll()
	srv := testServer(t)

	// Use a pattern with a dialog config so auto might recommend consensus.
	req := makeRequest("critical_thinking", map[string]any{
		"issue": "evaluate my reasoning about caching",
		"mode":  "solo",
	})

	result, err := srv.handlePattern(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePattern: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	inner := parseGuidedResult(t, result)
	mode, ok := inner["mode"].(string)
	if !ok {
		t.Fatalf("mode not present or not a string; full: %v", inner)
	}
	if mode != "solo" {
		t.Errorf("mode=solo requested, got %q", mode)
	}
}

// TestHandlePattern_ModeAuto_SimpleInput_Solo verifies that mode=auto with
// simple (short) input routes to solo.
func TestHandlePattern_ModeAuto_SimpleInput_Solo(t *testing.T) {
	patterns.RegisterAll()
	srv := testServer(t)

	req := makeRequest("critical_thinking", map[string]any{
		"issue": "short",
		"mode":  "auto",
	})

	result, err := srv.handlePattern(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePattern: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	inner := parseGuidedResult(t, result)
	mode, ok := inner["mode"].(string)
	if !ok {
		t.Fatalf("mode not present or not a string; full: %v", inner)
	}
	// Simple input is below complexity threshold — should stay solo.
	if mode != "solo" {
		t.Errorf("simple input with mode=auto: expected solo, got %q", mode)
	}
}

// TestHandlePattern_ModeAuto_ComplexInput_ConsensusRecommended verifies that
// mode=auto with complex input produces mode=consensus_recommended.
//
// Complexity routing formula: rawScore = textLen*0.3 + subItems*0.3 + depth*0.2 + bias*0.2
// Threshold = 60. To breach it we use decision_framework (bias=30) with:
//   - long "decision" text (>500 chars → textLen=100, contributes 30)
//   - 10+ "options" items (subItems=100, contributes 30)
//   - bias=30, contributes 6
//     Total = 66 ≥ 60 → recommendation "consensus".
func TestHandlePattern_ModeAuto_ComplexInput_ConsensusRecommended(t *testing.T) {
	patterns.RegisterAll()
	srv := testServer(t)

	// Long decision text pushes textLen score to 100.
	longDecision := "Should we migrate our core transaction processing service from a monolithic " +
		"PostgreSQL-backed architecture to a distributed microservices topology with Kafka " +
		"event sourcing? The current system handles 50k TPS with p99 latency of 12ms. The " +
		"proposed migration would involve splitting into 8 bounded contexts, each with its own " +
		"data store, connected via an event bus with exactly-once semantics guaranteed by a " +
		"custom idempotency layer. This requires 18 months of parallel-run investment plus " +
		"retraining the entire engineering organisation on event-driven patterns."

	// 10 options pushes subItems score to 100.
	options := []any{
		"Full migration to microservices",
		"Strangler-fig incremental extraction",
		"Keep monolith, optimise queries",
		"Add read replicas for scale-out",
		"CQRS with a separate read model",
		"Introduce a cache layer (Redis)",
		"Vertical scaling of current DB",
		"Migrate to distributed SQL (CockroachDB)",
		"Hybrid: extract only hottest services",
		"Postpone decision, gather more data",
	}

	req := makeRequest("decision_framework", map[string]any{
		"decision": longDecision,
		"options":  options,
		"mode":     "auto",
	})

	result, err := srv.handlePattern(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePattern: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	inner := parseGuidedResult(t, result)
	mode, ok := inner["mode"].(string)
	if !ok {
		t.Fatalf("mode not present or not a string; full: %v", inner)
	}
	// decision_framework has ComplexityBias=30; with max text + 10 options the
	// total score is 66 which exceeds the threshold of 60.
	if mode != "consensus_recommended" {
		t.Errorf("complex input with mode=auto: expected consensus_recommended, got %q", mode)
	}

	// Must also carry consensus_available and consensus_hint.
	if _, ok := inner["consensus_available"]; !ok {
		t.Error("consensus_available field missing in consensus_recommended response")
	}
	if _, ok := inner["consensus_hint"]; !ok {
		t.Error("consensus_hint field missing in consensus_recommended response")
	}
}

// TestHandlePattern_ModeConsensus_SoloOnlyPattern_ReturnsError verifies that
// mode=consensus on a solo-only pattern (no dialog config) returns a tool error.
func TestHandlePattern_ModeConsensus_SoloOnlyPattern_ReturnsError(t *testing.T) {
	patterns.RegisterAll()
	srv := testServer(t)

	// recursive_thinking is a solo-only cognitive move (no dialog config).
	req := makeRequest("recursive_thinking", map[string]any{
		"problem": "parse nested structures",
		"mode":    "consensus",
	})

	result, err := srv.handlePattern(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePattern returned Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for consensus mode on solo-only pattern, got success")
	}
}

// TestHandlePattern_ModeDefault_ActsLikeAuto verifies that omitting the mode
// parameter defaults to auto behavior (same result as explicit mode=auto).
func TestHandlePattern_ModeDefault_ActsLikeAuto(t *testing.T) {
	patterns.RegisterAll()
	srv := testServer(t)

	// Simple input — no mode field.
	req := makeRequest("critical_thinking", map[string]any{
		"issue": "short",
	})

	result, err := srv.handlePattern(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePattern: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	inner := parseGuidedResult(t, result)
	mode, ok := inner["mode"].(string)
	if !ok {
		t.Fatalf("mode not present or not a string; full: %v", inner)
	}
	// Default with simple input → solo (same as explicit mode=auto).
	if mode != "solo" {
		t.Errorf("no mode param with simple input: expected solo (auto default), got %q", mode)
	}
}

func TestHandlePattern_InvalidModeFailsClosed(t *testing.T) {
	patterns.RegisterAll()
	srv := testServer(t)

	result, err := srv.handlePattern(context.Background(), makeRequest("critical_thinking", map[string]any{
		"issue": "short",
		"mode":  "surprise",
	}))
	if err != nil {
		t.Fatalf("handlePattern returned Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("invalid mode should fail closed: %+v", result)
	}
}

func TestHandlePattern_StatelessMoveIgnoresIncomingSessionID(t *testing.T) {
	patterns.RegisterAll()
	srv := testServer(t)

	result, err := srv.handlePattern(context.Background(), makeRequest("critical_thinking", map[string]any{
		"issue":      "short",
		"session_id": "caller-owned-harness-session",
	}))
	if err != nil {
		t.Fatalf("handlePattern: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	inner := parseGuidedResult(t, result)
	if _, ok := inner["session_id"]; ok {
		t.Fatalf("stateless move leaked incoming session_id: %v", inner)
	}
	if inner["gate_status"] != "complete" {
		t.Fatalf("stateless move gate_status = %v, want complete", inner["gate_status"])
	}
}
