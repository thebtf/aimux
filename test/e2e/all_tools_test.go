package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
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
	stdin, reader := initTestCLIServer(t)

	// Exec async
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "async test",
			"cli":    "codex",
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
	stdin, reader := initTestCLIServer(t)

	// Create a session via exec
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{"prompt": "for kill", "cli": "codex"},
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
			// think responses are wrapped in the guidance envelope; domain fields are
			// nested under the "result" key.
			inner, ok := data["result"].(map[string]any)
			if !ok {
				t.Fatalf("expected nested result payload, got %T: %v", data["result"], data["result"])
			}
			if inner["pattern"] != tc.pattern {
				t.Errorf("pattern = %v, want %v", inner["pattern"], tc.pattern)
			}
			if inner["mode"] != "solo" {
				t.Errorf("mode = %v, want solo", inner["mode"])
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

func TestE2E_Investigate_FindingCoverageGuidance(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action": "start",
			"topic":  "server crash on startup",
		},
	}))
	startResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate start: %v", err)
	}
	startData := extractToolJSON(t, startResp)
	startResult, ok := startData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected nested start result payload")
	}
	sessionID, _ := startResult["session_id"].(string)
	if sessionID == "" {
		t.Fatal("missing result.session_id")
	}

	areas := []string{"reproduction", "isolation", "hypothesis_formation", "root_cause_analysis", "fix_verification", "regression_prevention", "environmental_factors", "error_trail"}
	previousGapCount := len(areas)
	for i, area := range areas {
		fmt.Fprint(stdin, jsonRPCRequest(3+i, "tools/call", map[string]any{
			"name": "investigate",
			"arguments": map[string]any{
				"action":        "finding",
				"session_id":    sessionID,
				"description":   "Observed issue in " + area,
				"source":        "main.go:42",
				"severity":      "P0",
				"coverage_area": area,
			},
		}))
		findingResp, err := readResponse(reader, 10*time.Second)
		if err != nil {
			t.Fatalf("investigate finding %d: %v", i, err)
		}
		findingData := extractToolJSON(t, findingResp)
		gaps, ok := findingData["gaps"].([]any)
		if i < len(areas)-1 {
			if !ok {
				t.Fatalf("expected gaps array at step %d, got %T", i, findingData["gaps"])
			}
			if len(gaps) >= previousGapCount {
				t.Fatalf("gaps did not shrink at step %d: got %d want < %d", i, len(gaps), previousGapCount)
			}
			previousGapCount = len(gaps)
		} else if ok && len(gaps) != 0 {
			t.Fatalf("expected no gaps after full coverage, got %v", gaps)
		}
		choose, ok := findingData["choose_your_path"].(map[string]any)
		if !ok {
			t.Fatal("missing choose_your_path")
		}
		self, ok := choose["self"].(map[string]any)
		if !ok {
			t.Fatal("missing self branch")
		}
		nextCall, _ := self["next_call"].(string)
		if i < len(areas)-1 {
			if nextCall == `investigate(action="assess", session_id="<session_id>")` || nextCall == `investigate(action="report", session_id="<session_id>")` {
				t.Fatalf("finding next_call switched too early at step %d: %q", i, nextCall)
			}
		} else {
			if len(gaps) != 0 {
				t.Fatalf("expected no gaps after full coverage, got %v", gaps)
			}
			if nextCall != `investigate(action="assess", session_id="<session_id>")` && nextCall != `investigate(action="report", session_id="<session_id>")` {
				t.Fatalf("expected assess/report next_call after full coverage, got %q", nextCall)
			}
		}
		previousGapCount = len(gaps)
	}
}

func TestE2E_Investigate_ReportZeroFindings_BlockedKeepsSession(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action": "start",
			"topic":  "no findings yet",
		},
	}))
	startResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate start: %v", err)
	}
	startData := extractToolJSON(t, startResp)
	startResult, ok := startData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected nested start result payload")
	}
	sessionID, _ := startResult["session_id"].(string)
	if sessionID == "" {
		t.Fatal("missing result.session_id")
	}

	fmt.Fprint(stdin, jsonRPCRequest(3, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action":     "report",
			"session_id": sessionID,
		},
	}))
	reportResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate report: %v", err)
	}
	reportData := extractToolJSON(t, reportResp)
	if reportData["state"] != "report_blocked" {
		t.Fatalf("state = %v, want report_blocked", reportData["state"])
	}
	if _, leaked := reportData["report"]; leaked {
		t.Fatal("unexpected top-level report field leak")
	}
	reportResult, ok := reportData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected report result payload")
	}
	reportText, _ := reportResult["report"].(string)
	if reportText == "" {
		t.Fatal("expected non-empty result.report")
	}
	choose, ok := reportData["choose_your_path"].(map[string]any)
	if !ok {
		t.Fatal("missing choose_your_path")
	}
	self, ok := choose["self"].(map[string]any)
	if !ok {
		t.Fatal("missing self branch")
	}
	nextCall, _ := self["next_call"].(string)
	if nextCall != `investigate(action="finding", session_id="<session_id>", description="...", source="...", severity="P2")` {
		t.Fatalf("next_call = %q, want finding corrective call", nextCall)
	}

	fmt.Fprint(stdin, jsonRPCRequest(4, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action":     "status",
			"session_id": sessionID,
		},
	}))
	statusResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate status: %v", err)
	}
	if statusResp["error"] != nil {
		t.Fatalf("status should remain usable after blocked report: %v", statusResp["error"])
	}
	statusResult, _ := statusResp["result"].(map[string]any)
	if statusResult == nil {
		t.Fatalf("missing JSON-RPC result in status response: %+v", statusResp)
	}
	if isErr, _ := statusResult["isError"].(bool); isErr {
		t.Fatalf("status returned tool error after blocked report: %+v", statusResult)
	}
}

func TestE2E_Investigate_ReportWeakEvidence_PreliminaryKeepsSession(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action": "start",
			"topic":  "startup crash",
		},
	}))
	startResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate start: %v", err)
	}
	startData := extractToolJSON(t, startResp)
	startResult, ok := startData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected nested start result payload")
	}
	sessionID, _ := startResult["session_id"].(string)
	if sessionID == "" {
		t.Fatal("missing result.session_id")
	}

	fmt.Fprint(stdin, jsonRPCRequest(3, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action":      "finding",
			"session_id":  sessionID,
			"description": "single weak finding",
			"source":      "main.go:42",
			"severity":    "P2",
		},
	}))
	if _, err := readResponse(reader, 10*time.Second); err != nil {
		t.Fatalf("investigate finding: %v", err)
	}

	fmt.Fprint(stdin, jsonRPCRequest(4, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action":     "report",
			"session_id": sessionID,
		},
	}))
	reportResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate report: %v", err)
	}
	reportData := extractToolJSON(t, reportResp)
	if reportData["state"] != "report_preliminary" {
		t.Fatalf("state = %v, want report_preliminary", reportData["state"])
	}
	if _, leaked := reportData["report"]; leaked {
		t.Fatal("unexpected top-level report field leak")
	}
	reportResult, ok := reportData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected report result payload")
	}
	reportText, _ := reportResult["report"].(string)
	if reportText == "" {
		t.Fatal("expected non-empty result.report")
	}
	choose, ok := reportData["choose_your_path"].(map[string]any)
	if !ok {
		t.Fatal("missing choose_your_path")
	}
	self, ok := choose["self"].(map[string]any)
	if !ok {
		t.Fatal("missing self branch")
	}
	nextCall, _ := self["next_call"].(string)
	if nextCall != `investigate(action="assess", session_id="<session_id>")` {
		t.Fatalf("next_call = %q, want assess corrective call", nextCall)
	}

	fmt.Fprint(stdin, jsonRPCRequest(5, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action":     "status",
			"session_id": sessionID,
		},
	}))
	statusResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate status: %v", err)
	}
	if statusResp["error"] != nil {
		t.Fatalf("status should remain usable after preliminary report: %v", statusResp["error"])
	}
	statusResult, _ := statusResp["result"].(map[string]any)
	if statusResult == nil {
		t.Fatalf("missing JSON-RPC result in status response: %+v", statusResp)
	}
	if isErr, _ := statusResult["isError"].(bool); isErr {
		t.Fatalf("status returned tool error after preliminary report: %+v", statusResult)
	}
}

func TestE2E_Investigate_ReportWeakEvidence_ForceIncompleteKeepsSession(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action": "start",
			"topic":  "startup crash",
		},
	}))
	startResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate start: %v", err)
	}
	startData := extractToolJSON(t, startResp)
	startResult, ok := startData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected nested start result payload")
	}
	sessionID, _ := startResult["session_id"].(string)
	if sessionID == "" {
		t.Fatal("missing result.session_id")
	}

	fmt.Fprint(stdin, jsonRPCRequest(3, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action":      "finding",
			"session_id":  sessionID,
			"description": "single weak finding",
			"source":      "main.go:42",
			"severity":    "P2",
		},
	}))
	if _, err := readResponse(reader, 10*time.Second); err != nil {
		t.Fatalf("investigate finding: %v", err)
	}

	fmt.Fprint(stdin, jsonRPCRequest(4, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action":     "report",
			"session_id": sessionID,
			"force":      true,
		},
	}))
	reportResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate report force=true: %v", err)
	}
	reportData := extractToolJSON(t, reportResp)
	if reportData["state"] != "report_incomplete_forced" {
		t.Fatalf("state = %v, want report_incomplete_forced", reportData["state"])
	}
	reportResult, ok := reportData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected report result payload")
	}
	reportText, _ := reportResult["report"].(string)
	if reportText == "" {
		t.Fatal("expected non-empty result.report")
	}
	metadata, ok := reportResult["metadata"].(map[string]any)
	if !ok {
		t.Fatal("expected result.metadata")
	}
	force, _ := metadata["force"].(bool)
	if !force {
		t.Fatalf("result.metadata.force = %v, want true", metadata["force"])
	}
	choose, ok := reportData["choose_your_path"].(map[string]any)
	if !ok {
		t.Fatal("missing choose_your_path")
	}
	self, ok := choose["self"].(map[string]any)
	if !ok {
		t.Fatal("missing self branch")
	}
	nextCall, _ := self["next_call"].(string)
	if nextCall != `investigate(action="assess", session_id="<session_id>")` {
		t.Fatalf("next_call = %q, want assess corrective call", nextCall)
	}

	fmt.Fprint(stdin, jsonRPCRequest(5, "tools/call", map[string]any{
		"name": "investigate",
		"arguments": map[string]any{
			"action":     "status",
			"session_id": sessionID,
		},
	}))
	statusResp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("investigate status: %v", err)
	}
	if statusResp["error"] != nil {
		t.Fatalf("status should remain usable after forced incomplete report: %v", statusResp["error"])
	}
	statusResult, _ := statusResp["result"].(map[string]any)
	if statusResult == nil {
		t.Fatalf("missing JSON-RPC result in status response: %+v", statusResp)
	}
	if isErr, _ := statusResult["isError"].(bool); isErr {
		t.Fatalf("status returned tool error after forced incomplete report: %+v", statusResult)
	}
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
	stdin, reader := initTestCLIServer(t)

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
			"cwd":    t.TempDir(),
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

// --- Stateful Tool Descriptions ---

// TestE2E_StatefulToolDescriptions_ContainExamples verifies that each stateful
// tool description is non-trivial and contains structured guidance sections with
// example call patterns (HOW/CHOOSE). This ensures that wiping the description
// body (returning null/empty) causes the test to fail per T021 AC.
func TestE2E_StatefulToolDescriptions_ContainExamples(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Fetch tool list
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/list", nil))
	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object in tools/list response: %v", resp)
	}
	toolsRaw, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array in tools/list result: %v", result)
	}

	// Build name → description index from the live registration
	descByName := make(map[string]string, len(toolsRaw))
	for _, raw := range toolsRaw {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		desc, _ := tool["description"].(string)
		if name != "" {
			descByName[name] = desc
		}
	}

	// statefulTools lists the six tools that must carry structured guidance.
	// Each entry carries:
	//   - callPatterns: substrings expected inside the HOW or CHOOSE section that
	//     represent concrete invocation examples or action names.
	//     If the description body is replaced with null/empty these will all be absent.
	type statefulToolCase struct {
		tool         string
		callPatterns []string // at least one must appear
	}

	cases := []statefulToolCase{
		{
			tool: "investigate",
			// HOW: "Call start … then iterate finding + assess … then call report."
			// CHOOSE: "Choose investigate over ad-hoc …"
			callPatterns: []string{"start", "finding", "assess", "report"},
		},
		{
			tool: "think",
			// HOW: "Provide pattern plus pattern-specific fields; pass session_id …"
			// CHOOSE: "Choose think for deep single-thread …"
			callPatterns: []string{"pattern", "session_id"},
		},
		{
			tool: "consensus",
			// HOW: "Provide topic; optionally set blinded/max_turns and synthesize …"
			// CHOOSE: "Choose consensus to measure agreement …"
			callPatterns: []string{"topic", "blinded", "synthesize"},
		},
		{
			tool: "debate",
			// HOW: "Provide topic; optionally tune max_turns and synthesize …"
			// CHOOSE: "Choose debate when challenge …"
			callPatterns: []string{"topic", "max_turns", "synthesize"},
		},
		{
			tool: "dialog",
			// HOW: "Provide prompt and optional max_turns …"
			// CHOOSE: "Choose dialog for exploratory iteration …"
			callPatterns: []string{"prompt", "max_turns"},
		},
		{
			tool: "workflow",
			// HOW: "Provide JSON steps (id/tool/params …)"
			// CHOOSE: "Choose workflow for repeatable chains …"
			callPatterns: []string{"steps", "id", "tool", "params"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.tool, func(t *testing.T) {
			desc, found := descByName[tc.tool]
			if !found {
				t.Fatalf("tool %q not found in tools/list response", tc.tool)
			}

			// AC: description must be non-empty / non-minimal (>100 chars)
			if len(desc) <= 100 {
				t.Errorf("tool %q description too short (%d chars), expected structured guidance content >100 chars", tc.tool, len(desc))
			}

			// AC: description must contain at least one example call pattern
			found = false
			for _, pattern := range tc.callPatterns {
				if strings.Contains(desc, pattern) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("tool %q description contains none of the expected example patterns %v\ndescription:\n%s", tc.tool, tc.callPatterns, desc)
			}

			// AC: HOW section must be present (structured guidance, not free-form text)
			if !strings.Contains(desc, "HOW:") {
				t.Errorf("tool %q description missing HOW: section", tc.tool)
			}

			// AC: CHOOSE section must be present (example call guidance for operators)
			if !strings.Contains(desc, "CHOOSE:") {
				t.Errorf("tool %q description missing CHOOSE: section", tc.tool)
			}
		})
	}
}

// T033 — Guided tool matrix sweep.
//
// For each guided tool (investigate, think) calls the tool via MCP and verifies:
//  1. The response contains top-level "state" and "result" guidance envelope keys.
//  2. The "state" value is tool-specific (not the generic "guidance_not_implemented" fallback).
//  3. The "result" payload contains the expected domain fields for that tool.
//
// Consensus / debate / dialog are skipped when fewer than 2 CLIs are available because
// they require multi-model dispatch which fails in the single-CLI test environment.
func TestE2E_GuidedToolMatrix(t *testing.T) {
	type guidedCase struct {
		tool        string
		args        map[string]any
		wantState   string // exact state value expected; empty = any non-generic value
		resultField string // a field that must exist inside the nested result payload
	}

	cases := []guidedCase{
		{
			tool: "investigate",
			args: map[string]any{
				"action": "start",
				"topic":  "matrix sweep test topic",
			},
			wantState:   "notebook_ready",
			resultField: "session_id",
		},
		{
			tool: "think",
			args: map[string]any{
				"pattern": "critical_thinking",
				"issue":   "matrix sweep think test",
			},
			wantState:   "complete",
			resultField: "pattern",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.tool, func(t *testing.T) {
			resp := initAndCall(t, tc.tool, tc.args)
			data := extractToolJSON(t, resp)

			// AC1: guidance envelope keys must be present.
			state, hasState := data["state"].(string)
			if !hasState || state == "" {
				t.Errorf("%s: response missing top-level state field; got %v", tc.tool, data["state"])
			}
			if _, hasResult := data["result"]; !hasResult {
				t.Errorf("%s: response missing top-level result field", tc.tool)
			}

			// AC2: state must be tool-specific, not the generic fallback.
			if state == "guidance_not_implemented" {
				t.Errorf("%s: state = %q is the generic fallback — tool policy not registered", tc.tool, state)
			}
			if tc.wantState != "" && state != tc.wantState {
				t.Errorf("%s: state = %q, want %q", tc.tool, state, tc.wantState)
			}

			// AC3: result payload must contain the expected domain field.
			inner, ok := data["result"].(map[string]any)
			if !ok {
				t.Fatalf("%s: result is %T, want map[string]any", tc.tool, data["result"])
			}
			if inner[tc.resultField] == nil {
				t.Errorf("%s: result.%s missing; full result: %v", tc.tool, tc.resultField, inner)
			}
		})
	}
}
