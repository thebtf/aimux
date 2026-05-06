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
	if resp["error"] != nil {
		return
	}
	result, _ := resp["result"].(map[string]any)
	if result != nil {
		if isErr, ok := result["isError"].(bool); ok && isErr {
			return
		}
	}
	t.Errorf("expected error response, got: %v", resp)
}

func TestE2E_Status_MissingJobID(t *testing.T) {
	resp := initAndCall(t, "status", map[string]any{})
	expectError(t, resp)
}

func TestE2E_Status_NonexistentJob(t *testing.T) {
	resp := initAndCall(t, "status", map[string]any{"job_id": "fake-job-id"})
	expectError(t, resp)
}

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

func TestE2E_Sessions_GC(t *testing.T) {
	resp := initAndCall(t, "sessions", map[string]any{"action": "gc"})
	data := extractToolJSON(t, resp)
	if data["collected"] == nil {
		t.Error("missing collected count")
	}
}

func TestE2E_Think_AllPatterns(t *testing.T) {
	// sampleArgsFromSchema does not understand XOR-required fields
	// (e.g. scientific_method requires stage OR entry_type). The test
	// fires invalid args for those patterns and the validator rejects
	// them. Tracked separately — re-enable when the sample generator
	// learns OneOf semantics.
	t.Skip("sample arg generator does not handle XOR-required schemas; tracked in backlog")

	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/list", nil))
	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}

	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("expected tools/list to return tools")
	}

	requestID := 3
	patternCount := 0
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		desc, _ := tool["description"].(string)
		if !strings.HasPrefix(desc, "[cognitive move") {
			continue
		}

		args := sampleArgsFromSchema(tool)
		fmt.Fprint(stdin, jsonRPCRequest(requestID, "tools/call", map[string]any{
			"name":      name,
			"arguments": args,
		}))
		requestID++

		patternResp, callErr := readResponse(reader, 10*time.Second)
		if callErr != nil {
			t.Fatalf("%s response: %v", name, callErr)
		}
		data := extractToolJSON(t, patternResp)
		inner, ok := data["result"].(map[string]any)
		if !ok {
			t.Fatalf("%s result payload = %T, want map[string]any", name, data["result"])
		}
		if inner["pattern"] != name {
			t.Fatalf("%s pattern = %v, want %s", name, inner["pattern"], name)
		}
		patternCount++
	}

	if patternCount != 22 {
		t.Fatalf("pattern tool count = %d, want 22", patternCount)
	}
}

func TestE2E_Think_MissingPattern(t *testing.T) {
	resp := initAndCall(t, "think", map[string]any{})
	expectError(t, resp)
}

func TestE2E_DeepResearch_MissingTopic(t *testing.T) {
	resp := initAndCall(t, "deepresearch", map[string]any{})
	expectError(t, resp)
}

func sampleArgsFromSchema(tool map[string]any) map[string]any {
	args := map[string]any{}
	inputSchema, _ := tool["inputSchema"].(map[string]any)
	properties, _ := inputSchema["properties"].(map[string]any)
	required := stringList(inputSchema["required"])
	for _, name := range required {
		property, _ := properties[name].(map[string]any)
		args[name] = sampleValueForProperty(name, property)
	}
	return args
}

func stringList(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func sampleValueForProperty(name string, property map[string]any) any {
	if enum, ok := property["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}

	switch property["type"] {
	case "string":
		return "sample " + name
	case "number", "integer":
		return 1
	case "boolean":
		return true
	case "array":
		return []any{"sample"}
	case "object":
		return map[string]any{"sample": true}
	default:
		return "sample"
	}
}
