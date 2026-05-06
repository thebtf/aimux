package server

import (
	"context"
	"testing"
)

func TestThinkHarnessToolSchemaReplacesBaseThink(t *testing.T) {
	srv := testServer(t)

	tool := srv.Tool("think")
	if tool == nil {
		t.Fatal("think harness tool not registered")
	}
	if srv.Tool("think_harness") != nil {
		t.Fatal("unexpected public think_harness tool registered")
	}

	properties := tool.InputSchema.Properties
	for _, field := range []string{
		"action",
		"session_id",
		"task",
		"chosen_move",
		"work_product",
		"evidence",
		"confidence",
		"execute",
		"proposed_answer",
		"force_finalize",
	} {
		if _, ok := properties[field]; !ok {
			t.Fatalf("think harness schema missing field %q; properties=%v", field, properties)
		}
	}
	if _, ok := properties["thought"]; ok {
		t.Fatal("legacy thought field must not be advertised in public think schema")
	}
}

func TestThinkHarnessLegacyThoughtFailsClosed(t *testing.T) {
	srv := testServer(t)

	result, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"thought": "debug crash error",
	}))
	if err != nil {
		t.Fatalf("handleThinkHarness returned Go error: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	if !result.IsError {
		t.Fatalf("legacy thought call must be an MCP tool error: %+v", result)
	}

	payload := parseResult(t, result)
	if payload["code"] != "legacy_thought_not_supported" {
		t.Fatalf("legacy code = %v, want legacy_thought_not_supported; payload=%v", payload["code"], payload)
	}
	for _, forbidden := range []string{
		"suggestedPattern",
		"alternativePatterns",
		"suggestedWorkflow",
		"session_id",
		"gate_status",
		"advisor_recommendation",
	} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("legacy migration response leaked %q: %v", forbidden, payload)
		}
	}
}
