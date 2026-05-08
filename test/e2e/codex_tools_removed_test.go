package e2e

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

// @critical - release blocker per Constitution rule #10.
func TestE2E_CodexToolsRemovedFromToolList(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/list", nil))
	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %v", result["tools"])
	}

	toolNames := make(map[string]bool, len(tools))
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		if name != "" {
			toolNames[name] = true
		}
	}

	for _, removed := range []string{
		"codex_task",
		"codex_review",
		"codex_status",
		"codex_cancel",
		"codex_review_gate",
	} {
		if toolNames[removed] {
			t.Fatalf("removed tool %q is still present in tools/list: %v", removed, sortedToolNames(toolNames))
		}
	}

	if !toolNames["task"] {
		t.Fatalf("replacement task tool missing from tools/list: %v", sortedToolNames(toolNames))
	}
}

func sortedToolNames(toolNames map[string]bool) []string {
	names := make([]string, 0, len(toolNames))
	for name := range toolNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
