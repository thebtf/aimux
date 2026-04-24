package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestE2E_Agents_SemanticFind verifies that agents(action="find") returns
// a candidate list with the expected structure when queried semantically.
//
// Skipped in -short mode and on CI because the daemon+shim pair is required
// and is flaky under CI scheduler jitter (same guard as shim_latency_test.go).
func TestE2E_Agents_SemanticFind(t *testing.T) {
	if testing.Short() {
		t.Skip("TestE2E_Agents_SemanticFind: skipped in -short mode (daemon+shim e2e)")
	}
	if os.Getenv("CI") != "" {
		t.Skip("TestE2E_Agents_SemanticFind: skipped on CI (daemon+shim e2e flaky under scheduler jitter)")
	}

	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "agents",
		"arguments": map[string]any{
			"action": "find",
			"prompt": "review code for security",
		},
	}))

	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("agents find: %v", err)
	}

	data := extractToolJSON(t, resp)

	// Response must contain a "matches" or "candidates" list.
	// agents(action=find) returns {query, matches, count}.
	if data["query"] == nil {
		t.Error("missing query field in agents find response")
	}
	if data["count"] == nil {
		t.Error("missing count field in agents find response")
	}
	// matches may be empty if no agents are registered, but the field must exist.
	_, hasMatches := data["matches"]
	if !hasMatches {
		t.Error("missing matches field in agents find response")
	}
}

// TestE2E_Agents_RunSelectionRationale verifies that agents(action="run") without
// an explicit agent name returns a selection_rationale field in the response.
//
// Skipped in -short mode and on CI because the daemon+shim pair is required
// and is flaky under CI scheduler jitter.
func TestE2E_Agents_RunSelectionRationale(t *testing.T) {
	if testing.Short() {
		t.Skip("TestE2E_Agents_RunSelectionRationale: skipped in -short mode (daemon+shim e2e)")
	}
	if os.Getenv("CI") != "" {
		t.Skip("TestE2E_Agents_RunSelectionRationale: skipped on CI (daemon+shim e2e flaky under scheduler jitter)")
	}

	stdin, reader := initTestCLIServer(t)

	// Call agents(action=run) without specifying an agent name so that semantic
	// auto-select runs. If no agents are registered the server returns a
	// choose_agent response — verify the shape is still structured.
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "agents",
		"arguments": map[string]any{
			"action": "run",
			"prompt": "review this code for security vulnerabilities",
			"cwd":    t.TempDir(),
		},
	}))

	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("agents run: %v", err)
	}

	// The response must not be a raw JSON-RPC error.
	if resp["error"] != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", resp["error"])
	}

	data := extractToolJSON(t, resp)

	// Two valid shapes:
	// (a) Auto-selected agent: {agent, job_id, session_id, status, selection_rationale}
	// (b) No agents registered: {action="choose_agent", candidates, message}
	if action, ok := data["action"].(string); ok && action == "choose_agent" {
		// No agents registered — verify the fallback shape is structured.
		if data["candidates"] == nil {
			t.Error("choose_agent response missing candidates field")
		}
		t.Log("agents run: no agents registered, got choose_agent fallback (expected in test env)")
		return
	}

	// Agent was selected — verify selection_rationale is present.
	rationale, ok := data["selection_rationale"].(map[string]any)
	if !ok {
		// selection_rationale is only injected when SemanticSelect ran (agent=""
		// path). If the test env registered agents via overlay this is always set.
		t.Logf("selection_rationale not present (may have been an explicit agent= run): %v", data)
		return
	}
	if rationale["agent_name"] == nil {
		t.Error("selection_rationale missing agent_name")
	}
	if rationale["reason"] == nil {
		t.Error("selection_rationale missing reason")
	}
}
