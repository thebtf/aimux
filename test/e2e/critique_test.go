package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestE2E_Critique_StructuredOutput verifies that the critique MCP tool dispatches
// to a CLI and returns a structured response with findings and cli_used fields.
//
// The testcli emulators do not produce real security findings; this test only
// verifies that the tool is wired into the MCP server, dispatches correctly, and
// returns the expected response envelope shape.
//
// Skipped in -short mode and on CI because the daemon+shim pair is required
// and is flaky under CI scheduler jitter.
func TestE2E_Critique_StructuredOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("TestE2E_Critique_StructuredOutput: skipped in -short mode (daemon+shim e2e)")
	}
	if os.Getenv("CI") != "" {
		t.Skip("TestE2E_Critique_StructuredOutput: skipped on CI (daemon+shim e2e flaky under scheduler jitter)")
	}

	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "critique",
		"arguments": map[string]any{
			"artifact": "function foo() { eval(userInput) }",
			"lens":     "security",
			"cli":      "codex",
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("critique: %v", err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", resp["error"])
	}

	data := extractToolJSON(t, resp)

	// The critique handler always includes lens and cli_used in the response.
	if data["lens"] == nil {
		t.Error("missing lens field in critique response")
	}
	if data["cli_used"] == nil {
		t.Error("missing cli_used field in critique response")
	}

	// findings must be present as an array (may be empty if testcli returns
	// unstructured plain text that the handler cannot parse as JSON).
	_, hasFindings := data["findings"]
	if !hasFindings {
		// Check whether the response has a raw_output fallback instead — the
		// handler falls back to raw_output when the CLI does not return valid JSON.
		if data["raw_output"] == nil && data["summary"] == nil {
			t.Error("critique response missing both findings and raw_output/summary — tool may not have dispatched")
		}
		t.Logf("testcli returned non-JSON output; critique response shape: %v", data)
		return
	}

	findings, _ := data["findings"].([]any)
	// testcli won't produce real findings, so nil or empty is acceptable.
	t.Logf("critique: lens=%v cli_used=%v findings=%d", data["lens"], data["cli_used"], len(findings))
}

// TestE2E_Critique_InvalidLens verifies that an unknown lens name returns an error.
func TestE2E_Critique_InvalidLens(t *testing.T) {
	if testing.Short() {
		t.Skip("TestE2E_Critique_InvalidLens: skipped in -short mode (daemon+shim e2e)")
	}
	if os.Getenv("CI") != "" {
		t.Skip("TestE2E_Critique_InvalidLens: skipped on CI (daemon+shim e2e flaky under scheduler jitter)")
	}

	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "critique",
		"arguments": map[string]any{
			"artifact": "func foo() {}",
			"lens":     "nonexistent-lens",
		},
	}))

	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("critique invalid lens: %v", err)
	}

	expectError(t, resp)
}
