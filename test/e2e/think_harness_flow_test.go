package e2e

import (
	"bufio"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestE2E_ThinkHarnessFullFlow(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	start := callE2EToolJSON(t, stdin, reader, 2, "think", map[string]any{
		"action":          "start",
		"task":            "decide whether the supported answer can ship",
		"context_summary": "caller must provide visible evidence before finalization",
	})
	sessionID, _ := start["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("start response missing session_id: %v", start)
	}

	premature := callE2EToolJSON(t, stdin, reader, 3, "think", map[string]any{
		"action":          "finalize",
		"session_id":      sessionID,
		"proposed_answer": "ship now",
	})
	if premature["can_finalize"] != false {
		t.Fatalf("premature finalize can_finalize = %v, want false; payload=%v", premature["can_finalize"], premature)
	}
	if _, ok := premature["missing_gates"]; !ok {
		t.Fatalf("premature finalize missing missing_gates: %v", premature)
	}

	step := callE2EToolJSON(t, stdin, reader, 4, "think", map[string]any{
		"action":       "step",
		"session_id":   sessionID,
		"chosen_move":  "critical_thinking",
		"work_product": "The answer is supported by a verified requirement and has no critical objections.",
		"confidence":   0.78,
		"evidence": []any{map[string]any{
			"kind":                "file",
			"ref":                 "spec.md",
			"summary":             "finalization gate requires visible evidence",
			"verification_status": "verified",
		}},
	})
	if step["executed"] != true {
		t.Fatalf("step executed = %v, want true; payload=%v", step["executed"], step)
	}

	finalized := callE2EToolJSON(t, stdin, reader, 5, "think", map[string]any{
		"action":          "finalize",
		"session_id":      sessionID,
		"proposed_answer": "The supported answer can ship.",
	})
	if finalized["can_finalize"] != true {
		t.Fatalf("finalize can_finalize = %v, want true; payload=%v", finalized["can_finalize"], finalized)
	}
	traceSummary, ok := finalized["trace_summary"].(map[string]any)
	if !ok {
		t.Fatalf("finalize missing trace_summary: %v", finalized)
	}
	stopReason, ok := traceSummary["stop_reason"].(string)
	if !ok || stopReason == "" {
		t.Fatalf("trace_summary.stop_reason missing or not a non-empty string: %v", traceSummary["stop_reason"])
	}
}

func callE2EToolJSON(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int, toolName string, args map[string]any) map[string]any {
	t.Helper()

	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})); err != nil {
		t.Fatalf("%s request write: %v", toolName, err)
	}
	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("%s response: %v", toolName, err)
	}
	return extractToolJSON(t, resp)
}
