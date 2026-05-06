package server

import (
	"context"
	"testing"
)

func TestThinkHarnessStartThroughPublicTool(t *testing.T) {
	srv := testServer(t)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":          "start",
		"task":            "plan a risky change",
		"context_summary": "caller owns the final answer",
	}))
	if err != nil {
		t.Fatalf("handleThinkHarness: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected start error: %+v", result.Content)
	}

	payload := parseResult(t, result)
	for _, field := range []string{
		"session_id",
		"phase",
		"allowed_move_groups",
		"recommended_moves",
		"missing_inputs",
		"next_prompt",
	} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("start response missing %q: %v", field, payload)
		}
	}
	if _, ok := payload["suggestedPattern"]; ok {
		t.Fatalf("start response must not include suggestedPattern: %v", payload)
	}
}

func TestThinkHarnessStartRejectsMissingTask(t *testing.T) {
	srv := testServer(t)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":          "start",
		"context_summary": "missing task",
	}))
	if err != nil {
		t.Fatalf("handleThinkHarness: %v", err)
	}
	if !result.IsError {
		t.Fatalf("missing task should fail closed: %+v", result)
	}
	payload := parseResult(t, result)
	if payload["code"] != "invalid_input" {
		t.Fatalf("code = %v, want invalid_input; payload=%v", payload["code"], payload)
	}
}

func TestThinkHarnessStartAppliesResponseFieldsBudget(t *testing.T) {
	srv := testServer(t)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":          "start",
		"task":            "budget response",
		"context_summary": "fields should filter response",
		"fields":          "session_id,trace_summary",
	}))
	if err != nil {
		t.Fatalf("handleThinkHarness: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected start error: %+v", result.Content)
	}
	payload := parseResult(t, result)
	if _, ok := payload["session_id"]; !ok {
		t.Fatalf("budgeted response missing session_id: %v", payload)
	}
	if _, ok := payload["trace_summary"]; !ok {
		t.Fatalf("budgeted response missing trace_summary: %v", payload)
	}
	if _, ok := payload["recommended_moves"]; ok {
		t.Fatalf("budgeted response kept omitted field: %v", payload)
	}
	if payload["truncated"] != true {
		t.Fatalf("budgeted response missing truncation marker: %v", payload)
	}
}
