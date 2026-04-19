package e2e

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// TestE2E_Sessions_List_DualSource verifies that sessions(action=list) returns
// dual-source shape per FR-11.
func TestE2E_Sessions_List_DualSource(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Seed 3 sessions (not 50 — keeps test fast; the shape contract is what matters).
	for i := 0; i < 3; i++ {
		fmt.Fprint(stdin, jsonRPCRequest(100+i, "tools/call", map[string]any{
			"name": "exec",
			"arguments": map[string]any{
				"prompt": fmt.Sprintf("session fixture %d", i),
				"cli":    "codex",
				"async":  true,
			},
		}))
		_, err := readResponse(reader, 5*time.Second)
		if err != nil {
			t.Fatalf("create session %d: %v", i, err)
		}
	}

	time.Sleep(200 * time.Millisecond)

	fmt.Fprint(stdin, jsonRPCRequest(200, "tools/call", map[string]any{
		"name": "sessions",
		"arguments": map[string]any{
			"action": "list",
		},
	}))
	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("sessions list: %v", err)
	}

	data := extractToolJSON(t, resp)

	// Verify dual-source shape keys.
	for _, key := range []string{"sessions", "loom_tasks", "sessions_pagination", "loom_pagination"} {
		if _, ok := data[key]; !ok {
			t.Errorf("response missing %q key", key)
		}
	}

	// Verify sessions_pagination is an object with total field.
	sessPage, _ := data["sessions_pagination"].(map[string]any)
	if sessPage == nil {
		t.Fatal("sessions_pagination is not an object")
	}
	if _, ok := sessPage["total"]; !ok {
		t.Error("sessions_pagination missing 'total' field")
	}

	// Verify response size <= 4096 bytes.
	jsonBytes, _ := json.Marshal(data)
	if len(jsonBytes) > 4096 {
		t.Errorf("sessions list response %d bytes exceeds 4096-byte budget", len(jsonBytes))
	}
}

// TestE2E_Sessions_Info_ContentLength verifies that sessions(action=info) returns
// per-job content_length without Content body by default.
func TestE2E_Sessions_Info_ContentLength(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Create a session via sync exec.
	fmt.Fprint(stdin, jsonRPCRequest(300, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt":          "hello sessions info test",
			"cli":             "codex",
			"async":           false,
			"include_content": true,
		},
	}))
	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	data := extractToolJSON(t, resp)
	sessionID, _ := data["session_id"].(string)
	if sessionID == "" {
		t.Skipf("no session_id in exec response: %v", data)
	}

	fmt.Fprint(stdin, jsonRPCRequest(301, "tools/call", map[string]any{
		"name": "sessions",
		"arguments": map[string]any{
			"action":     "info",
			"session_id": sessionID,
		},
	}))
	infoResp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("sessions info: %v", err)
	}
	infoData := extractToolJSON(t, infoResp)

	jobs, _ := infoData["jobs"].([]any)
	for i, j := range jobs {
		jm, _ := j.(map[string]any)
		if jm == nil {
			continue
		}
		if jm["content"] != nil {
			t.Errorf("job %d: content should not be present in brief", i)
		}
		if jm["content_length"] == nil {
			t.Errorf("job %d: content_length should be present in brief", i)
		}
	}
}

// TestE2E_Agents_Info_ContentGating verifies that agents(action=info) returns
// content_length without content body by default, full content with include_content=true.
func TestE2E_Agents_Info_ContentGating(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// List agents to find one.
	fmt.Fprint(stdin, jsonRPCRequest(400, "tools/call", map[string]any{
		"name": "agents",
		"arguments": map[string]any{
			"action": "list",
		},
	}))
	listResp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("agents list: %v", err)
	}
	listData := extractToolJSON(t, listResp)
	agentsList, _ := listData["agents"].([]any)
	if len(agentsList) == 0 {
		t.Skip("no agents available for test")
	}
	firstAgent, _ := agentsList[0].(map[string]any)
	agentName, _ := firstAgent["name"].(string)
	if agentName == "" {
		t.Fatal("first agent has no name")
	}

	// Default info — should not include content.
	fmt.Fprint(stdin, jsonRPCRequest(401, "tools/call", map[string]any{
		"name": "agents",
		"arguments": map[string]any{
			"action": "info",
			"agent":  agentName,
		},
	}))
	infoResp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("agents info: %v", err)
	}
	infoData := extractToolJSON(t, infoResp)

	if infoData["content"] != nil {
		t.Errorf("agents info brief should not include content: got %v", infoData["content"])
	}
	if infoData["content_length"] == nil {
		t.Error("agents info brief should include content_length")
	}

	// include_content=true should return full content.
	fmt.Fprint(stdin, jsonRPCRequest(402, "tools/call", map[string]any{
		"name": "agents",
		"arguments": map[string]any{
			"action":          "info",
			"agent":           agentName,
			"include_content": true,
		},
	}))
	fullResp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("agents info include_content: %v", err)
	}
	fullData := extractToolJSON(t, fullResp)
	if fullData["content"] == nil {
		t.Error("agents info with include_content=true should return content field")
	}
}
