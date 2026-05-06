package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThinkHarnessDocsAndDescriptionsStayCallerCentered(t *testing.T) {
	srv := testServer(t)
	tool := srv.Tool("think")
	if tool == nil {
		t.Fatal("think tool missing")
	}
	description := tool.Description
	for _, forbidden := range []string{"suggestedPattern", "keyword router", "keyword routing"} {
		if strings.Contains(description, forbidden) {
			t.Fatalf("think description contains stale router wording %q: %s", forbidden, description)
		}
	}

	for _, path := range []string{
		filepath.Join("..", "..", "README.md"),
		filepath.Join("..", "..", "docs", "PRODUCTION-TESTING-PLAYBOOK.md"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(raw)
		if !strings.Contains(text, "think(action=start|step|finalize)") {
			t.Fatalf("%s does not document think(action=start|step|finalize)", path)
		}
		if strings.Contains(strings.ToLower(text), "think_harness") {
			t.Fatalf("%s documents think_harness as public surface", path)
		}
		if strings.Contains(text, "suggestedPattern") {
			t.Fatalf("%s contains stale suggestedPattern wording", path)
		}
	}
}

func TestThinkHarnessDefaultResponsesUseTraceSummary(t *testing.T) {
	srv := testServer(t)

	startResult, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":          "start",
		"task":            "verify response shape",
		"context_summary": "trace summary must be bounded",
	}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	start := parseResult(t, startResult)
	requireBoundedTraceSummary(t, start)
	sessionID, _ := start["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("start missing session_id: %v", start)
	}

	stepResult, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":       "step",
		"session_id":   sessionID,
		"chosen_move":  "critical_thinking",
		"work_product": "The shape has visible evidence.",
		"confidence":   0.78,
		"evidence": []any{map[string]any{
			"kind":                "file",
			"ref":                 "README.md",
			"summary":             "public docs describe the action-mode harness",
			"verification_status": "verified",
		}},
	}))
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	step := parseResult(t, stepResult)
	requireBoundedTraceSummary(t, step)

	finalResult, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":          "finalize",
		"session_id":      sessionID,
		"proposed_answer": "The response shape is bounded.",
	}))
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	finalized := parseResult(t, finalResult)
	requireBoundedTraceSummary(t, finalized)
}

func requireBoundedTraceSummary(t *testing.T, payload map[string]any) {
	t.Helper()
	summary, ok := payload["trace_summary"].(map[string]any)
	if !ok {
		t.Fatalf("trace_summary missing or wrong type: %v", payload)
	}
	for _, field := range []string{"session_id", "phase", "move_count", "evidence_count", "can_finalize"} {
		if _, ok := summary[field]; !ok {
			t.Fatalf("trace_summary missing %q: %v", field, summary)
		}
	}
	for _, forbidden := range []string{"trace", "moves", "observations", "gate_reports"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("default response exposed unbounded trace field %q: %v", forbidden, payload)
		}
	}
}
