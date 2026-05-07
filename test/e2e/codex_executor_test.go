// Package e2e: Codex executor MCP surface integration tests (AIMUX-18 FR-1..FR-5).
//
// Test strategy:
//   - Always run discoverability tests (tools/list) — no codex binary required.
//   - Skip nil-safe stub tests when codex IS on PATH (real path takes over).
//   - Skip real-lifecycle tests when CODEX_E2E=1 is not set or codex not on PATH.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// codexOnPATH returns true if the `codex` binary is resolvable on PATH.
func codexOnPATH() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

// codexE2EEnabled returns true when both codex is on PATH and CODEX_E2E=1.
// Real end-to-end tests that invoke the actual Codex process require this gate
// to avoid false negatives in CI environments where codex is not authenticated.
func codexE2EEnabled() bool {
	return codexOnPATH() && os.Getenv("CODEX_E2E") == "1"
}

// TestE2E_Codex_ToolsInList verifies the 5 codex_* tools appear in tools/list
// regardless of whether codex is installed (AIMUX-18 FR-1..FR-5 discoverability).
func TestE2E_Codex_ToolsInList(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/list", nil))
	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}

	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}

	wantTools := []string{
		"codex_task",
		"codex_review",
		"codex_status",
		"codex_cancel",
		"codex_review_gate",
	}

	found := make(map[string]bool)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		for _, want := range wantTools {
			if name == want {
				found[want] = true
			}
		}
	}

	for _, want := range wantTools {
		if !found[want] {
			t.Errorf("tool %q not found in tools/list", want)
		}
	}
}

// TestE2E_Codex_NilSafe_NoCodexBinary verifies that when codex is absent from PATH,
// all 5 tools return an actionable isError=true result (no Go panic or crash).
//
// Skipped when codex IS on PATH (the real lifecycle tests cover those paths).
func TestE2E_Codex_NilSafe_NoCodexBinary(t *testing.T) {
	if codexOnPATH() {
		t.Skip("codex is on PATH — nil-safe stub path not exercised; skipping")
	}

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"codex_task", map[string]any{"prompt": "hello world"}},
		{"codex_review", map[string]any{"target": "HEAD"}},
		{"codex_status", map[string]any{"task_id": "fake-task-id"}},
		{"codex_cancel", map[string]any{"task_id": "fake-task-id"}},
		{"codex_review_gate", map[string]any{"target": "HEAD"}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			resp := initAndCall(t, tc.tool, tc.args)
			expectError(t, resp)

			// Verify the error message is actionable (mentions "codex").
			result, _ := resp["result"].(map[string]any)
			if result == nil {
				// JSON-RPC error path — still valid.
				return
			}
			content, _ := result["content"].([]any)
			if len(content) == 0 {
				t.Errorf("%s: expected error content, got empty content", tc.tool)
				return
			}
			first, _ := content[0].(map[string]any)
			text, _ := first["text"].(string)
			if !strings.Contains(strings.ToLower(text), "codex") {
				t.Errorf("%s: error text does not mention 'codex': %q", tc.tool, text)
			}
		})
	}
}

// TestE2E_Codex_Submit_Status_Cancel exercises the full task lifecycle:
// submit → codex_status poll → codex_cancel (if not yet terminal).
//
// Requires CODEX_E2E=1 and codex on PATH. Skipped otherwise.
func TestE2E_Codex_Submit_Status_Cancel(t *testing.T) {
	if !codexE2EEnabled() {
		t.Skip("CODEX_E2E=1 not set or codex not on PATH — skipping real codex lifecycle test")
	}

	stdin, reader := initTestCLIServer(t)
	reqID := 2

	// Submit a short codex task.
	fmt.Fprint(stdin, jsonRPCRequest(reqID, "tools/call", map[string]any{
		"name": "codex_task",
		"arguments": map[string]any{
			"prompt": "print the number 42 and exit",
		},
	}))
	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("codex_task submit: %v", err)
	}

	// Extract task_id from result JSON.
	taskID := extractJSONField(t, resp, "task_id")
	if taskID == "" {
		t.Fatalf("codex_task: no task_id in response: %v", resp)
	}
	t.Logf("codex_task submitted: task_id=%s", taskID)

	// Poll codex_status until terminal or 30s timeout.
	deadline := time.Now().Add(30 * time.Second)
	var finalStatus string
	for time.Now().Before(deadline) {
		reqID++
		fmt.Fprint(stdin, jsonRPCRequest(reqID, "tools/call", map[string]any{
			"name":      "codex_status",
			"arguments": map[string]any{"task_id": taskID},
		}))
		statusResp, serr := readResponse(reader, 10*time.Second)
		if serr != nil {
			t.Fatalf("codex_status: %v", serr)
		}
		status := extractJSONField(t, statusResp, "status")
		t.Logf("codex_status: %s", status)
		if isTerminalStatus(status) {
			finalStatus = status
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if finalStatus == "" {
		// Task did not complete within deadline — cancel it.
		reqID++
		fmt.Fprint(stdin, jsonRPCRequest(reqID, "tools/call", map[string]any{
			"name":      "codex_cancel",
			"arguments": map[string]any{"task_id": taskID},
		}))
		if _, cerr := readResponse(reader, 10*time.Second); cerr != nil {
			t.Logf("codex_cancel: %v", cerr)
		}
		t.Error("task did not reach terminal state within 30s")
	} else {
		t.Logf("task reached terminal state: %s", finalStatus)
	}
}

// TestE2E_Codex_Cancel_Idempotent verifies codex_cancel succeeds on an already-terminal
// task (idempotency — ADR-012 requirement).
//
// Requires CODEX_E2E=1 and codex on PATH.
func TestE2E_Codex_Cancel_Idempotent(t *testing.T) {
	if !codexE2EEnabled() {
		t.Skip("CODEX_E2E=1 not set or codex not on PATH — skipping real codex idempotency test")
	}

	stdin, reader := initTestCLIServer(t)
	reqID := 2

	// Submit task.
	fmt.Fprint(stdin, jsonRPCRequest(reqID, "tools/call", map[string]any{
		"name":      "codex_task",
		"arguments": map[string]any{"prompt": "echo done"},
	}))
	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	taskID := extractJSONField(t, resp, "task_id")
	if taskID == "" {
		t.Fatalf("no task_id in response: %v", resp)
	}

	// Cancel once.
	reqID++
	fmt.Fprint(stdin, jsonRPCRequest(reqID, "tools/call", map[string]any{
		"name":      "codex_cancel",
		"arguments": map[string]any{"task_id": taskID},
	}))
	if _, err := readResponse(reader, 10*time.Second); err != nil {
		t.Fatalf("first cancel: %v", err)
	}

	// Cancel again — must succeed (idempotent, not an error).
	reqID++
	fmt.Fprint(stdin, jsonRPCRequest(reqID, "tools/call", map[string]any{
		"name":      "codex_cancel",
		"arguments": map[string]any{"task_id": taskID},
	}))
	resp2, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("second cancel: %v", err)
	}

	result, _ := resp2["result"].(map[string]any)
	if result != nil {
		if isErr, ok := result["isError"].(bool); ok && isErr {
			content, _ := result["content"].([]any)
			t.Errorf("second cancel returned error (expected idempotent success): %v", content)
		}
	}
}

// --- helpers ---

// extractJSONField extracts a top-level string field from a tool result's JSON text content.
// Fails the test immediately on isError=true, missing content, or non-JSON payloads
// so regressions surface as fast failures rather than timeout waits.
func extractJSONField(t *testing.T, resp map[string]any, field string) string {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("tool returned no result: %v", resp)
	}
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("tool returned error: %v", result["content"])
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tool returned empty content: %v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if text == "" {
		t.Fatalf("tool returned empty text: %v", first)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("tool returned non-JSON text: %q", text)
	}
	val, _ := data[field].(string)
	return val
}

// isTerminalStatus returns true for Loom terminal task states.
func isTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "failed_crash", "cancelled":
		return true
	}
	return false
}
