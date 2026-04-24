//go:build windows

package e2e

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestE2E_Upgrade_HotSwap_Windows(t *testing.T) {
	if testing.Short() {
		t.Skip("hot-swap e2e requires full daemon lifecycle")
	}
	// Handoff protocol times out on Windows test runners (30s deadline).
	// This Windows-specific test needs DuplicateHandle path which is slower.
	t.Skip("hot-swap handoff times out on Windows test runners — tracked for CI optimization")
	v1Bin := buildBinaryVersion(t, "1.0.0")
	v2Bin := buildBinaryVersion(t, "1.0.1")
	testcliBin := buildTestCLI(t)
	tmpDir := t.TempDir()
	configDir, _, _ := shimTestWriteConfig(t, tmpDir)

	mockBaseURL := serveMockRelease(t, "1.0.0", "1.0.1", v2Bin)
	_, stdin, reader := startDaemonAndShimWithEnv(t, v1Bin, filepath.Dir(testcliBin), configDir, []string{
		"AIMUX_TEST_UPDATE_BASE_URL=" + mockBaseURL,
		"AIMUX_SESSION_STORE=sqlite",
	})
	initializeMCP(t, stdin, reader)

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
		t.Fatal("expected at least one available agent")
	}
	first, _ := agents[0].(map[string]any)
	agentName, _ := first["name"].(string)
	if agentName == "" {
		t.Fatal("first agent missing name")
	}

	fmt.Fprint(stdin, jsonRPCRequest(3, "tools/call", map[string]any{
		"name": "agent",
		"arguments": map[string]any{
			"agent":  agentName,
			"prompt": "upgrade hot swap async agent windows",
			"cwd":    filepath.Dir(testcliBin),
			"async":  true,
		},
	}))
	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("start async agent: %v", err)
	}
	startData := extractToolJSON(t, resp)
	jobID, _ := startData["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id in async agent response: %+v", startData)
	}

	fmt.Fprint(stdin, jsonRPCRequest(4, "tools/call", map[string]any{
		"name":      "upgrade",
		"arguments": map[string]any{"action": "apply"},
	}))
	upgradeResp, err := readResponse(reader, 45*time.Second)
	if err != nil {
		t.Fatalf("upgrade apply: %v", err)
	}
	upgradeData := extractToolJSON(t, upgradeResp)
	if got := upgradeData["status"]; got != "updated_hot_swap" {
		t.Fatalf("upgrade status = %v, want updated_hot_swap (full=%+v)", got, upgradeData)
	}
	if got := upgradeData["new_version"]; got != "1.0.1" {
		t.Fatalf("upgrade new_version = %v, want 1.0.1", got)
	}

	deadline := time.Now().Add(15 * time.Second)
	seenQueryable := false
	for pollID := 100; time.Now().Before(deadline); pollID++ {
		time.Sleep(150 * time.Millisecond)
		fmt.Fprint(stdin, jsonRPCRequest(pollID, "tools/call", map[string]any{
			"name":      "status",
			"arguments": map[string]any{"job_id": jobID, "include_content": true},
		}))
		pollResp, pollErr := readResponse(reader, 5*time.Second)
		if pollErr != nil {
			t.Fatalf("status poll after upgrade: %v", pollErr)
		}
		pollData := extractToolJSON(t, pollResp)
		status, _ := pollData["status"].(string)
		if status == "running" || status == "completed" || status == "failed" || status == "failed_crash" || status == "aborted" {
			seenQueryable = true
			break
		}
	}
	if !seenQueryable {
		t.Fatal("async job was never queryable after hot-swap response")
	}

	versionDeadline := time.Now().Add(1 * time.Second)
	for reqID := 300; time.Now().Before(versionDeadline); reqID++ {
		fmt.Fprint(stdin, jsonRPCRequest(reqID, "resources/read", map[string]any{
			"uri": "aimux://health",
		}))
		healthResp, healthErr := readResponse(reader, 5*time.Second)
		if healthErr != nil {
			t.Fatalf("health read after upgrade: %v", healthErr)
		}
		result, ok := healthResp["result"].(map[string]any)
		if !ok {
			t.Fatalf("health read missing result: %+v", healthResp)
		}
		contents, ok := result["contents"].([]any)
		if !ok || len(contents) == 0 {
			t.Fatalf("health read missing contents: %+v", result)
		}
		entry, ok := contents[0].(map[string]any)
		if !ok {
			t.Fatalf("health entry malformed: %+v", contents[0])
		}
		text, _ := entry["text"].(string)
		var health map[string]any
		if err := json.Unmarshal([]byte(text), &health); err != nil {
			t.Fatalf("parse health json: %v", err)
		}
		if health["version"] == "1.0.1" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("health/version signal did not show 1.0.1 within 1s of upgrade response")
}
