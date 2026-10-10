package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/think"
)

func TestMCPStartupMode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		value        string
		thinkingOnly bool
	}{
		{name: "unset"},
		{name: "empty", value: ""},
		{name: "zero", value: "0"},
		{name: "true", value: "true"},
		{name: "leading_zero", value: "01"},
		{name: "leading_space", value: " 1"},
		{name: "trailing_space", value: "1 "},
		{name: "newline", value: "1\n"},
		{name: "enabled", value: "1", thinkingOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AIMUX_MCP_THINKING_ONLY", tc.value)
			if tc.name == "unset" {
				if err := os.Unsetenv("AIMUX_MCP_THINKING_ONLY"); err != nil {
					t.Fatalf("unset startup mode: %v", err)
				}
			}
			srv := testServer(t)

			// A later environment change must not alter the constructed surface.
			if tc.thinkingOnly {
				t.Setenv("AIMUX_MCP_THINKING_ONLY", "")
			} else {
				t.Setenv("AIMUX_MCP_THINKING_ONLY", "1")
			}

			response := srv.mcp.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"mode-test","version":"1.0"}}}`))
			rpc, ok := response.(mcp.JSONRPCResponse)
			if !ok {
				t.Fatalf("initialize response = %T, want JSONRPCResponse", response)
			}
			initialized, ok := rpc.Result.(mcp.InitializeResult)
			if !ok {
				t.Fatalf("initialize result = %T, want InitializeResult", rpc.Result)
			}
			caps := initialized.Capabilities
			if caps.Tools == nil || caps.Sampling == nil {
				t.Fatalf("tools and sampling must remain enabled: %#v", caps)
			}
			wantExtraCapabilities := !tc.thinkingOnly
			for name, present := range map[string]bool{
				"tasks":     caps.Tasks != nil,
				"resources": caps.Resources != nil,
				"prompts":   caps.Prompts != nil,
			} {
				if present != wantExtraCapabilities {
					t.Errorf("%s capability present = %v, want %v", name, present, wantExtraCapabilities)
				}
			}

			wantInstructions := thinkingOnlyInstructions
			if !tc.thinkingOnly {
				wantInstructions = buildInstructions(srv.registry.EnabledCLIs(), false, srv.registry.AllCLIs(), 0, buildRoleMap(srv.router))
			}
			if initialized.Instructions != wantInstructions {
				t.Errorf("initialize instructions = %q, want %q", initialized.Instructions, wantInstructions)
			}

			response = srv.mcp.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
			rpc, ok = response.(mcp.JSONRPCResponse)
			if !ok {
				t.Fatalf("tools/list response = %T, want JSONRPCResponse", response)
			}
			listed, ok := rpc.Result.(mcp.ListToolsResult)
			if !ok {
				t.Fatalf("tools/list result = %T, want ListToolsResult", rpc.Result)
			}
			wantNames := think.GetAllPatterns()
			if len(wantNames) != 22 {
				t.Fatalf("pattern registry count = %d, want 22", len(wantNames))
			}
			nonPatternTools := []string{"think", "task", "review", "status", "sessions", "deepresearch", "upgrade"}
			if !tc.thinkingOnly {
				wantNames = append(wantNames, nonPatternTools...)
			}
			if len(listed.Tools) != len(wantNames) {
				t.Fatalf("tool count = %d, want %d", len(listed.Tools), len(wantNames))
			}
			remaining := make(map[string]bool, len(wantNames))
			for _, name := range wantNames {
				remaining[name] = true
			}
			for _, tool := range listed.Tools {
				if !remaining[tool.Name] {
					t.Errorf("unexpected or duplicate tool %q", tool.Name)
				}
				delete(remaining, tool.Name)
			}
			for name := range remaining {
				t.Errorf("missing tool %q", name)
			}
			if !tc.thinkingOnly {
				return
			}
			for _, name := range nonPatternTools {
				if srv.mcp.GetTool(name) != nil {
					t.Errorf("forbidden tool %q registered", name)
				}
			}
			for _, method := range []string{"resources/list", "resources/templates/list", "prompts/list", "tasks/list"} {
				request := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":%q,"params":{}}`, method))
				response := srv.mcp.HandleMessage(context.Background(), request)
				rpcError, ok := response.(mcp.JSONRPCError)
				if !ok || rpcError.Error.Code != mcp.METHOD_NOT_FOUND {
					t.Errorf("%s response = %#v, want method not found", method, response)
				}
			}
		})
	}
}

func TestThinkingOnlyInstructions(t *testing.T) {
	if !strings.Contains(thinkingOnlyInstructions, "thinking-only") || !strings.Contains(thinkingOnlyInstructions, "22 low-level cognitive move tools") {
		t.Fatalf("instructions must identify thinking-only mode: %s", thinkingOnlyInstructions)
	}
	for _, disabled := range []string{"`think`", "`task`", "`review`", "status(", "sessions(", "deepresearch", "upgrade", "aimux://", "resources/", "prompts/", "Sampling remains enabled"} {
		if strings.Contains(thinkingOnlyInstructions, disabled) {
			t.Errorf("instructions reference disabled surface %q", disabled)
		}
	}
}
