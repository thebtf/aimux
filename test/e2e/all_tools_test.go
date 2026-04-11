package e2e

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// initAndCall initializes MCP server and calls a tool, returning parsed response JSON.
func initAndCall(t *testing.T, toolName string, args map[string]any) map[string]any {
	t.Helper()
	stdin, reader := initTestCLIServer(t)

	// Call tool
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	}))

	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("%s response: %v", toolName, err)
	}

	return resp
}

// extractToolText extracts the text content from a tool response.
func extractToolText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no result in response: %v", resp)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("empty content in response: %v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

// extractToolJSON extracts and parses the JSON text content from a tool response.
func extractToolJSON(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	text := extractToolText(t, resp)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("tool response not JSON: %v (text: %s)", err, text)
	}
	return data
}

// expectError verifies the response is an MCP error (isError=true or JSON-RPC error).
func expectError(t *testing.T, resp map[string]any) {
	t.Helper()
	// Check JSON-RPC level error
	if resp["error"] != nil {
		return
	}
	// Check MCP tool-level error (isError flag on result)
	result, _ := resp["result"].(map[string]any)
	if result != nil {
		if isErr, ok := result["isError"].(bool); ok && isErr {
			return
		}
	}
	t.Errorf("expected error response, got: %v", resp)
}

// --- Exec Tool ---

func TestE2E_Exec_Async(t *testing.T) {
	bin := buildBinary(t)
	_, stdin, reader := startServer(t, bin)

	// Initialize
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	}))
	if _, err := readResponse(reader, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Exec async
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "async test",
			"cli":    "echo-cli",
			"async":  true,
		},
	}))
	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("exec async: %v", err)
	}

	data := extractToolJSON(t, resp)
	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Fatal("missing job_id in async response")
	}
	if data["status"] != "running" {
		t.Errorf("status = %v, want running", data["status"])
	}

	// Poll status until completed (with timeout)
	for i := 0; i < 20; i++ {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(stdin, jsonRPCRequest(3+i, "tools/call", map[string]any{
			"name":      "status",
			"arguments": map[string]any{"job_id": jobID},
		}))
		pollResp, pollErr := readResponse(reader, 5*time.Second)
		if pollErr != nil {
			t.Fatalf("status poll: %v", pollErr)
		}
		pollData := extractToolJSON(t, pollResp)
		status, _ := pollData["status"].(string)
		if status == "completed" || status == "failed" {
			if status == "completed" {
				return // success
			}
			t.Fatalf("job failed: %v", pollData)
		}
	}
	t.Fatal("async job did not complete in time")
}

func TestE2E_Exec_MissingPrompt(t *testing.T) {
	resp := initAndCall(t, "exec", map[string]any{"cli": "echo-cli"})
	expectError(t, resp)
}

func TestE2E_Exec_InvalidCWD(t *testing.T) {
	resp := initAndCall(t, "exec", map[string]any{
		"prompt": "test",
		"cli":    "echo-cli",
		"cwd":    "/nonexistent/path/xyz",
	})
	expectError(t, resp)
}

// --- Status Tool ---

func TestE2E_Status_MissingJobID(t *testing.T) {
	resp := initAndCall(t, "status", map[string]any{})
	expectError(t, resp)
}

func TestE2E_Status_NonexistentJob(t *testing.T) {
	resp := initAndCall(t, "status", map[string]any{"job_id": "fake-job-id"})
	expectError(t, resp)
}

// --- Sessions Tool ---

func TestE2E_Sessions_Health(t *testing.T) {
	resp := initAndCall(t, "sessions", map[string]any{"action": "health"})
	data := extractToolJSON(t, resp)
	if data["total_sessions"] == nil {
		t.Error("missing total_sessions")
	}
	if data["running_jobs"] == nil {
		t.Error("missing running_jobs")
	}
}

func TestE2E_Sessions_Info_NotFound(t *testing.T) {
	resp := initAndCall(t, "sessions", map[string]any{
		"action":     "info",
		"session_id": "nonexistent",
	})
	expectError(t, resp)
}

func TestE2E_Sessions_Kill(t *testing.T) {
	bin := buildBinary(t)
	_, stdin, reader := startServer(t, bin)

	// Initialize
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	}))
	if _, err := readResponse(reader, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Create a session via exec
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{"prompt": "for kill", "cli": "echo-cli"},
	}))
	execResp, _ := readResponse(reader, 10*time.Second)
	execData := extractToolJSON(t, execResp)
	sessionID, _ := execData["session_id"].(string)

	// Kill session
	fmt.Fprint(stdin, jsonRPCRequest(3, "tools/call", map[string]any{
		"name":      "sessions",
		"arguments": map[string]any{"action": "kill", "session_id": sessionID},
	}))
	killResp, _ := readResponse(reader, 10*time.Second)
	killData := extractToolJSON(t, killResp)
	if killData["status"] != "killed" {
		t.Errorf("kill status = %v, want killed", killData["status"])
	}
}

func TestE2E_Sessions_GC(t *testing.T) {
	resp := initAndCall(t, "sessions", map[string]any{"action": "gc"})
	data := extractToolJSON(t, resp)
	if data["collected"] == nil {
		t.Error("missing collected count")
	}
}

// --- Agents Tool ---

func TestE2E_Agents_List(t *testing.T) {
	resp := initAndCall(t, "agents", map[string]any{"action": "list"})
	data := extractToolJSON(t, resp)
	if data["agents"] == nil {
		t.Error("missing agents list")
	}
}

func TestE2E_Agents_MissingAction(t *testing.T) {
	resp := initAndCall(t, "agents", map[string]any{})
	expectError(t, resp)
}

// --- Think Tool ---

func TestE2E_Think_BasicPatterns(t *testing.T) {
	patterns := []struct {
		pattern string
		params  map[string]any
	}{
		{
			pattern: "think",
			params: map[string]any{
				"pattern": "think",
				"thought": "test thought",
			},
		},
		{
			pattern: "critical_thinking",
			params: map[string]any{
				"pattern": "critical_thinking",
				"issue":   "test issue",
			},
		},
		{
			pattern: "decision_framework",
			params: map[string]any{
				"pattern":  "decision_framework",
				"decision": "choose architecture",
			},
		},
	}

	for _, tc := range patterns {
		t.Run(tc.pattern, func(t *testing.T) {
			resp := initAndCall(t, "think", tc.params)
			data := extractToolJSON(t, resp)
			if data["pattern"] != tc.pattern {
				t.Errorf("pattern = %v, want %v", data["pattern"], tc.pattern)
			}
			if data["mode"] != "solo" {
				t.Errorf("mode = %v, want solo", data["mode"])
			}
		})
	}
}

func TestE2E_Think_MissingPattern(t *testing.T) {
	resp := initAndCall(t, "think", map[string]any{})
	expectError(t, resp)
}

// --- Investigate Tool ---

func TestE2E_Investigate_Start(t *testing.T) {
	resp := initAndCall(t, "investigate", map[string]any{
		"action": "start",
		"topic":  "test investigation",
	})
	data := extractToolJSON(t, resp)
	resultPayload, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatal("expected nested result payload")
	}
	if resultPayload["session_id"] == nil {
		t.Error("missing result.session_id")
	}
	if data["state"] == nil {
		t.Error("missing state guidance field")
	}
	if data["how_this_tool_works"] == nil {
		t.Error("missing how_this_tool_works guidance field")
	}
	if data["choose_your_path"] == nil {
		t.Error("missing choose_your_path guidance field")
	}
	if data["do_not"] == nil {
		t.Error("missing do_not guidance field")
	}
}

func TestE2E_Investigate_MissingAction(t *testing.T) {
	resp := initAndCall(t, "investigate", map[string]any{})
	expectError(t, resp)
}

func TestE2E_Investigate_StartMissingTopic(t *testing.T) {
	resp := initAndCall(t, "investigate", map[string]any{
		"action": "start",
	})
	expectError(t, resp)
}

// --- Consensus Tool ---

func TestE2E_Consensus_Basic(t *testing.T) {
	resp := initAndCall(t, "consensus", map[string]any{
		"topic": "test consensus topic",
	})
	// Consensus needs 2 CLIs — with only echo-cli, may return error
	result, _ := resp["result"].(map[string]any)
	if result == nil && resp["error"] == nil {
		t.Fatal("expected either result or error from consensus")
	}
}

func TestE2E_Consensus_MissingTopic(t *testing.T) {
	resp := initAndCall(t, "consensus", map[string]any{})
	expectError(t, resp)
}

// --- Debate Tool ---

func TestE2E_Debate_Basic(t *testing.T) {
	resp := initAndCall(t, "debate", map[string]any{
		"topic": "test debate topic",
	})
	// Debate needs 2 CLIs — with only echo-cli, may return error
	result, _ := resp["result"].(map[string]any)
	if result == nil && resp["error"] == nil {
		t.Fatal("expected either result or error from debate")
	}
}

func TestE2E_Debate_MissingTopic(t *testing.T) {
	resp := initAndCall(t, "debate", map[string]any{})
	expectError(t, resp)
}

// --- Dialog Tool ---

func TestE2E_Dialog_Basic(t *testing.T) {
	resp := initAndCall(t, "dialog", map[string]any{
		"prompt": "test dialog prompt",
	})
	// Dialog needs 2 CLIs — with only echo-cli, may return error
	result, _ := resp["result"].(map[string]any)
	if result == nil && resp["error"] == nil {
		t.Fatal("expected either result or error from dialog")
	}
}

func TestE2E_Dialog_MissingPrompt(t *testing.T) {
	resp := initAndCall(t, "dialog", map[string]any{})
	expectError(t, resp)
}

// --- Agents Tool ---

func TestE2E_Agent_Builtin(t *testing.T) {
	// First list agents to get a real builtin name. Use same server process so the
	// discovered registry from startup is shared with the later agent call.
	bin := buildBinary(t)
	_, stdin, reader := startServer(t, bin)

	// Initialize
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	}))
	if _, err := readResponse(reader, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// List agents
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name":      "agents",
		"arguments": map[string]any{"action": "list"},
	}))
	listResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("agents list: %v", err)
	}
	listData := extractToolJSON(t, listResp)
	agents, _ := listData["agents"].([]any)
	if len(agents) == 0 {
		t.Skip("no agents available in registry")
	}
	first, _ := agents[0].(map[string]any)
	agentName, _ := first["name"].(string)
	if agentName == "" {
		t.Skip("first agent missing name")
	}

	// Call agent tool using the builtin agent name
	fmt.Fprint(stdin, jsonRPCRequest(3, "tools/call", map[string]any{
		"name": "agent",
		"arguments": map[string]any{
			"agent":  agentName,
			"prompt": "test prompt",
		},
	}))
	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("agent response: %v", err)
	}
	data := extractToolJSON(t, resp)
	if data["agent"] != agentName {
		t.Errorf("agent = %v, want %s", data["agent"], agentName)
	}
	if data["content"] == nil {
		t.Error("missing content")
	}
}
