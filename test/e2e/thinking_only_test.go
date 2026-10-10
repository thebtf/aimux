package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
)

func TestE2E_MCPStartupMode(t *testing.T) {
	aimuxBin := buildBinary(t)
	testcliBin := buildTestCLI(t)
	patterns.RegisterAll()
	patternNames := think.GetAllPatterns()
	if len(patternNames) != 22 {
		t.Fatalf("pattern registry count = %d, want 22", len(patternNames))
	}

	for _, tc := range []struct {
		name         string
		value        string
		thinkingOnly bool
	}{
		{name: "thinking_only", value: "1", thinkingOnly: true},
		{name: "default", value: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stdin, reader := startDaemonAndShimWithEnv(t, aimuxBin, filepath.Dir(testcliBin), filepath.Join(testdataDir(), "config"), []string{
				"AIMUX_MCP_THINKING_ONLY=" + tc.value,
			})
			response := initializeMCPWithResponse(t, stdin, reader)
			initialized, ok := response["result"].(map[string]any)
			if !ok {
				t.Fatalf("initialize result missing: %#v", response)
			}
			caps, ok := initialized["capabilities"].(map[string]any)
			if !ok {
				t.Fatalf("initialize capabilities missing: %#v", initialized)
			}
			for _, name := range []string{"tools", "sampling"} {
				if _, ok := caps[name]; !ok {
					t.Errorf("required capability %q missing: %#v", name, caps)
				}
			}
			for _, name := range []string{"tasks", "resources", "prompts"} {
				_, present := caps[name]
				if want := !tc.thinkingOnly; present != want {
					t.Errorf("%s capability present = %v, want %v", name, present, want)
				}
			}
			instructions, _ := initialized["instructions"].(string)
			if tc.thinkingOnly {
				if !strings.Contains(instructions, "thinking-only") || !strings.Contains(instructions, "22 low-level cognitive move tools") {
					t.Errorf("thinking-only instructions missing: %q", instructions)
				}
			} else if !strings.Contains(instructions, "29 tools") || strings.Contains(instructions, "thinking-only") {
				t.Errorf("default instructions changed: %q", instructions)
			}
			if _, err := fmt.Fprint(stdin, jsonRPCNotification("notifications/initialized")); err != nil {
				t.Fatalf("initialized notification: %v", err)
			}

			if _, err := fmt.Fprint(stdin, jsonRPCRequest(2, "tools/list", nil)); err != nil {
				t.Fatalf("tools/list request: %v", err)
			}
			response, err := readResponse(reader, 10*time.Second)
			if err != nil {
				t.Fatalf("tools/list: %v", err)
			}
			result, ok := response["result"].(map[string]any)
			if !ok {
				t.Fatalf("tools/list result missing: %#v", response)
			}
			tools, ok := result["tools"].([]any)
			if !ok {
				t.Fatalf("tools/list tools missing: %#v", result)
			}
			wantNames := append([]string(nil), patternNames...)
			nonPatternTools := []string{"think", "task", "review", "status", "sessions", "deepresearch", "upgrade"}
			if !tc.thinkingOnly {
				wantNames = append(wantNames, nonPatternTools...)
			}
			if len(tools) != len(wantNames) {
				t.Fatalf("tool count = %d, want %d", len(tools), len(wantNames))
			}
			remaining := make(map[string]bool, len(wantNames))
			for _, name := range wantNames {
				remaining[name] = true
			}
			for _, raw := range tools {
				tool, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("tool = %T, want object", raw)
				}
				name, _ := tool["name"].(string)
				if !remaining[name] {
					t.Errorf("unexpected or duplicate tool %q", name)
				}
				delete(remaining, name)
				if tc.thinkingOnly {
					for _, forbidden := range nonPatternTools {
						if name == forbidden {
							t.Errorf("forbidden tool %q exposed", name)
						}
					}
					if tool["inputSchema"] == nil {
						t.Errorf("cognitive move %q missing inputSchema", name)
					}
				}
			}
			for name := range remaining {
				t.Errorf("missing tool %q", name)
			}
		})
	}
}
