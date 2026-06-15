package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskInspectability_DiagnosticSmoke(t *testing.T) {
	if os.Getenv("AIMUX23_E2E") != "1" {
		t.Skip("AIMUX23_E2E=1 not set - skipping task inspectability diagnostic smoke")
	}

	evidence := runTaskInspectabilitySmoke(t)
	for _, field := range []string{
		"daemon_health",
		"task_acceptance",
		"terminal_state_or_timeout",
		"resource_read",
		"caller_cleanup",
	} {
		if _, ok := evidence[field]; !ok {
			t.Fatalf("diagnostic smoke evidence missing %q: %#v", field, evidence)
		}
	}
	requireEvidenceStatus(t, evidence, "daemon_health", "ok")
	daemonHealth := requireEvidenceMap(t, evidence, "daemon_health")
	if daemonHealth["loom_status"] != "ok" {
		t.Fatalf("daemon_health.loom_status = %#v, want ok; evidence=%#v", daemonHealth["loom_status"], daemonHealth)
	}
	taskAcceptance := requireEvidenceMap(t, evidence, "task_acceptance")
	if taskAcceptance["status"] != "accepted" || taskAcceptance["task_id"] == "" {
		t.Fatalf("task_acceptance missing accepted task_id: %#v", taskAcceptance)
	}
	terminal := requireEvidenceMap(t, evidence, "terminal_state_or_timeout")
	if terminal["status"] == "" || terminal["task_id"] == "" || terminal["elapsed_ms"] == nil {
		t.Fatalf("terminal_state_or_timeout missing status/task_id/elapsed_ms: %#v", terminal)
	}
	resourceRead := requireEvidenceMap(t, evidence, "resource_read")
	if resourceRead["status"] != "ok" || resourceRead["uri"] == "" || resourceRead["projection_status"] == nil {
		t.Fatalf("resource_read missing status/uri/projection_status: %#v", resourceRead)
	}
	cleanup := requireEvidenceMap(t, evidence, "caller_cleanup")
	if cleanup["status"] == "" || cleanup["elapsed_ms"] == nil {
		t.Fatalf("caller_cleanup missing status/elapsed_ms: %#v", cleanup)
	}

	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal diagnostic smoke evidence: %v", err)
	}
	for _, forbidden := range []string{"env", "transcript", "content", "result"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Fatalf("diagnostic smoke evidence exposed forbidden key %q: %s", forbidden, raw)
		}
	}
	t.Logf("AIMUX23 diagnostic smoke evidence: %s", raw)
}

func requireEvidenceMap(t *testing.T, evidence map[string]any, field string) map[string]any {
	t.Helper()
	value, ok := evidence[field].(map[string]any)
	if !ok {
		t.Fatalf("diagnostic smoke evidence %q = %T, want map: %#v", field, evidence[field], evidence[field])
	}
	return value
}

func requireEvidenceStatus(t *testing.T, evidence map[string]any, field string, want string) {
	t.Helper()
	value := requireEvidenceMap(t, evidence, field)
	if value["status"] != want {
		t.Fatalf("%s.status = %#v, want %q; evidence=%#v", field, value["status"], want, value)
	}
}

func runTaskInspectabilitySmoke(t *testing.T) map[string]any {
	t.Helper()

	aimuxBin := buildBinary(t)
	testcliBin := buildTestCLI(t)
	configDir := taskRouterConfigDir(t)
	shimCmd, stdin, reader := startDaemonAndShimWithEnv(t, aimuxBin, filepath.Dir(testcliBin), configDir, []string{
		"AIMUX_SESSION_STORE=sqlite",
	})
	initializeMCP(t, stdin, reader)

	evidence := map[string]any{}

	health := callTaskInspectabilityToolJSON(t, stdin, reader, 2, "sessions", map[string]any{
		"action": "health",
	}, 10*time.Second)
	evidence["daemon_health"] = map[string]any{
		"status":        "ok",
		"init_phase":    health["init_phase"],
		"running_jobs":  health["running_jobs"],
		"loom_status":   health["loom_status"],
		"loom_tasks":    health["loom_tasks"],
		"tool_surface":  "sessions.health",
		"process_probe": "daemon+shim JSON-RPC",
	}
	if loomErr, ok := health["loom_error"].(string); ok && loomErr != "" {
		evidence["daemon_health"].(map[string]any)["loom_error"] = loomErr
	}

	taskPayload := callTaskInspectabilityToolJSON(t, stdin, reader, 3, "task", map[string]any{
		"task_class":      "review",
		"target":          "HEAD",
		"prompt":          "Review HEAD for AIMUX-23 task inspectability smoke and return PASS.",
		"timeout_seconds": 60,
	}, 60*time.Second)
	taskID, _ := taskPayload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("task acceptance missing task_id: %#v", taskPayload)
	}
	evidence["task_acceptance"] = map[string]any{
		"status":     "accepted",
		"task_id":    taskID,
		"task_class": taskPayload["task_class"],
	}
	if taskStatus, ok := taskPayload["status"].(string); ok && taskStatus != "" {
		evidence["task_acceptance"].(map[string]any)["task_status"] = taskStatus
	}

	terminal := waitTaskInspectabilityTerminal(t, stdin, reader, 4, taskID, 30*time.Second)
	evidence["terminal_state_or_timeout"] = terminal
	if terminal["status"] == "timeout" {
		t.Fatalf("task %s did not reach terminal status: %#v", taskID, terminal)
	}

	resourceURI := "aimux://tasks/" + taskID
	resourcePayload := readTaskInspectabilityResourceJSON(t, stdin, reader, 20, resourceURI)
	resourceRead := map[string]any{
		"status": "ok",
		"uri":    resourceURI,
	}
	if gotTaskID, _ := resourcePayload["task_id"].(string); gotTaskID != "" {
		resourceRead["task_id"] = gotTaskID
	}
	if artifacts, ok := resourcePayload["artifacts"].(map[string]any); ok {
		resourceRead["projection_status"] = artifacts["projection_status"]
	}
	evidence["resource_read"] = resourceRead

	evidence["caller_cleanup"] = cleanupTaskInspectabilityCaller(shimCmd, stdin)
	return evidence
}

func callTaskInspectabilityToolJSON(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int, toolName string, args map[string]any, timeout time.Duration) map[string]any {
	t.Helper()

	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})); err != nil {
		t.Fatalf("%s request write: %v", toolName, err)
	}
	resp, err := readResponse(reader, timeout)
	if err != nil {
		t.Fatalf("%s response: %v", toolName, err)
	}
	return extractToolJSON(t, resp)
}

func waitTaskInspectabilityTerminal(t *testing.T, stdin io.Writer, reader *bufio.Reader, firstID int, taskID string, timeout time.Duration) map[string]any {
	t.Helper()

	start := time.Now()
	for attempt := 0; time.Since(start) < timeout; attempt++ {
		statusPayload := callTaskInspectabilityToolJSON(t, stdin, reader, firstID+attempt, "status", map[string]any{
			"job_id": taskID,
		}, 10*time.Second)
		status, _ := statusPayload["status"].(string)
		if isTaskInspectabilityTerminalStatus(status) {
			return map[string]any{
				"status":          status,
				"task_id":         taskID,
				"elapsed_ms":      time.Since(start).Milliseconds(),
				"evidence_source": "status tool",
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return map[string]any{
		"status":          "timeout",
		"task_id":         taskID,
		"elapsed_ms":      time.Since(start).Milliseconds(),
		"evidence_source": "status tool",
	}
}

func isTaskInspectabilityTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "failed_crash", "cancelled":
		return true
	default:
		return false
	}
}

func readTaskInspectabilityResourceJSON(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int, uri string) map[string]any {
	t.Helper()

	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "resources/read", map[string]any{
		"uri": uri,
	})); err != nil {
		t.Fatalf("resource read request write: %v", err)
	}
	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("resource read response: %v", err)
	}
	if resp["error"] != nil {
		t.Fatalf("resource read JSON-RPC error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("resource read missing result: %#v", resp)
	}
	contents, _ := result["contents"].([]any)
	if len(contents) == 0 {
		t.Fatalf("resource read empty contents: %#v", result)
	}
	first, _ := contents[0].(map[string]any)
	text, _ := first["text"].(string)
	if text == "" {
		t.Fatalf("resource read missing text: %#v", first)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("resource read text is not JSON: %v text=%s", err, text)
	}
	return data
}

func cleanupTaskInspectabilityCaller(shimCmd *exec.Cmd, stdin io.Closer) map[string]any {
	start := time.Now()
	cleanup := map[string]any{
		"status": "started",
	}
	if err := stdin.Close(); err != nil {
		cleanup["status"] = "stdin_close_error"
		cleanup["error"] = err.Error()
		cleanup["elapsed_ms"] = time.Since(start).Milliseconds()
		return cleanup
	}
	if shimCmd == nil || shimCmd.Process == nil {
		cleanup["status"] = "no_process"
		cleanup["elapsed_ms"] = time.Since(start).Milliseconds()
		return cleanup
	}

	done := make(chan error, 1)
	go func() {
		done <- shimCmd.Wait()
	}()
	select {
	case err := <-done:
		cleanup["status"] = "exited"
		if err != nil {
			cleanup["wait_error"] = err.Error()
		}
	case <-time.After(2 * time.Second):
		_ = shimCmd.Process.Kill()
		cleanup["status"] = "timeout_killed"
		select {
		case err := <-done:
			if err != nil {
				cleanup["wait_error"] = err.Error()
			}
		case <-time.After(1 * time.Second):
			cleanup["wait_error"] = "wait timeout after kill"
		}
	}
	cleanup["elapsed_ms"] = time.Since(start).Milliseconds()
	return cleanup
}
