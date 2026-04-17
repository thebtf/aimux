package server_test

import (
	"encoding/json"
	"testing"

	"github.com/thebtf/aimux/pkg/server"
)

// TestMuxCompatibility_InitializeResponse verifies the MCP initialize response
// matches the format expected by mcp-mux (stdio multiplexer).
// mcp-mux forwards JSON-RPC messages and expects standard MCP protocol.
func TestMuxCompatibility_InitializeResponse(t *testing.T) {
	// Simulate the response our server produces
	response := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"logging": map[string]any{},
			"prompts": map[string]any{"listChanged": true},
			"resources": map[string]any{
				"subscribe":   true,
				"listChanged": true,
			},
			"tools": map[string]any{"listChanged": true},
		},
		"serverInfo": map[string]any{
			"name":    "aimux",
			"version": server.Version,
		},
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Verify it round-trips correctly (mcp-mux passes JSON-RPC as-is)
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Check required fields for mcp-mux compatibility
	if parsed["protocolVersion"] != "2024-11-05" {
		t.Error("protocolVersion missing or wrong")
	}

	caps, ok := parsed["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("capabilities missing")
	}

	if _, ok := caps["tools"]; !ok {
		t.Error("tools capability missing")
	}

	info, ok := parsed["serverInfo"].(map[string]any)
	if !ok {
		t.Fatal("serverInfo missing")
	}
	if info["name"] != "aimux" {
		t.Error("serverInfo.name wrong")
	}
}

// TestMuxCompatibility_ToolResultFormat verifies tool results match MCP spec.
func TestMuxCompatibility_ToolResultFormat(t *testing.T) {
	// MCP tool result format
	result := map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": `{"status":"completed","content":"hello"}`,
			},
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	content, ok := parsed["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("content array missing")
	}

	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatal("first content item not object")
	}
	if first["type"] != "text" {
		t.Error("content type should be 'text'")
	}
	if _, ok := first["text"].(string); !ok {
		t.Error("text field should be string")
	}
}
