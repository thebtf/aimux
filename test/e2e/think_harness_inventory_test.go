package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestE2E_ThinkHarnessInventory(t *testing.T) {
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

	byName := make(map[string]map[string]any, len(tools))
	cognitiveMoveCount := 0
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		if name == "" {
			continue
		}
		byName[name] = tool
		desc, _ := tool["description"].(string)
		if strings.Contains(desc, "[cognitive move") {
			cognitiveMoveCount++
		}
	}

	const expectedTotal = 27 // 4 server tools + 1 caller-centered think harness + 22 cognitive move tools.
	if len(tools) != expectedTotal {
		t.Fatalf("tool count = %d, want %d for CR-002 surface", len(tools), expectedTotal)
	}
	if _, ok := byName["think"]; !ok {
		t.Fatal("think harness tool missing")
	}
	if _, ok := byName["think_harness"]; ok {
		t.Fatal("think_harness must not be exposed as a parallel public tool")
	}
	for _, name := range []string{"decision_framework", "metacognitive_monitoring"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("low-level cognitive move %q missing from tools/list", name)
		}
		if tool["inputSchema"] == nil {
			t.Fatalf("low-level cognitive move %q missing inputSchema", name)
		}
	}
	if cognitiveMoveCount != 22 {
		t.Fatalf("cognitive move tool count = %d, want 22", cognitiveMoveCount)
	}

	desc, _ := byName["think"]["description"].(string)
	if strings.Contains(strings.ToLower(desc), "keyword") || strings.Contains(desc, "suggestedPattern") {
		t.Fatalf("think description still describes keyword routing: %q", desc)
	}
}
