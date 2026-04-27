package server

import (
	"encoding/json"
	"runtime"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/types"
)

// testServer constructs a minimal Server suitable for unit tests of the
// reduced post-purge tool surface (status, sessions, deepresearch, upgrade,
// 23 think patterns). Heavyweight wiring used by the removed Layer 5 tools
// (executor, swarm, orchestrator, agent registry) is intentionally omitted.
func testServer(t *testing.T) *Server {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{
			LogLevel:              "error",
			LogFile:               t.TempDir() + "/test.log",
			DefaultTimeoutSeconds: 10,
		},
		Roles: map[string]types.RolePreference{
			"default": {CLI: "codex"},
			"coding":  {CLI: "codex"},
		},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 3,
			CooldownSeconds:  5,
			HalfOpenMaxCalls: 1,
		},
		CLIProfiles: map[string]*config.CLIProfile{
			"codex": {
				Name:           "codex",
				Binary:         testBinary(),
				TimeoutSeconds: 10,
				Capabilities:   []string{"coding", "default"},
			},
		},
	}

	log, err := logger.New(cfg.Server.LogFile, logger.LevelError)
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}

	registry := driver.NewRegistry(cfg.CLIProfiles)
	router := routing.NewRouterWithProfiles(cfg.Roles, registry.EnabledCLIs(), cfg.CLIProfiles)

	srv := New(cfg, log, registry, router)
	if srv == nil {
		t.Fatal("server.New returned nil")
	}
	t.Cleanup(func() {
		srv.Shutdown()
		// Close the logger so its file handle is released before
		// t.TempDir's RemoveAll runs (Windows file-locking).
		_ = log.Close()
	})
	return srv
}

// testBinary returns a binary that exists on the test host across platforms.
// Used by tests that need a profile.Binary to point to something runnable.
func testBinary() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "echo"
}

// makeRequest builds an mcp.CallToolRequest with the given tool name and args.
func makeRequest(name string, args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}
}

// parseResult decodes the JSON text payload of an MCP tool result into a map.
// Returns the parsed map, or {"text": <raw>} when the payload is not JSON.
func parseResult(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content is not TextContent: %T", result.Content[0])
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text.Text), &data); err != nil {
		return map[string]any{"text": text.Text}
	}
	return data
}

// parseGuidedResult unwraps the guidance envelope (data.result map) returned
// by guided handlers like the think patterns. Fails the test when the result
// payload is not a map under the "result" key.
func parseGuidedResult(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	data := parseResult(t, result)
	inner, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatalf("parseGuidedResult: expected result.result map, got %T; full: %v", data["result"], data)
	}
	return inner
}

// mockNotifier is a test double for muxcore.Notifier. Captures Broadcast
// payloads (defensively copying) so tests can inspect what would have been
// broadcast.
type mockNotifier struct {
	broadcasts [][]byte
	mu         sync.Mutex
}

func (m *mockNotifier) Notify(projectID string, notification []byte) error { return nil }

func (m *mockNotifier) Broadcast(notification []byte) {
	cp := make([]byte, len(notification))
	copy(cp, notification)
	m.mu.Lock()
	m.broadcasts = append(m.broadcasts, cp)
	m.mu.Unlock()
}

func (m *mockNotifier) broadcastCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.broadcasts)
}
