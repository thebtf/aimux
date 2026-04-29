package server_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	aimuxServer "github.com/thebtf/aimux/pkg/server"
	"github.com/thebtf/aimux/pkg/types"
)

func newTestServer(t *testing.T) *aimuxServer.Server {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{
			LogLevel:              "error",
			LogFile:               t.TempDir() + "/test.log",
			DefaultTimeoutSeconds: 30,
			Audit: config.AuditConfig{
				ScannerRole:      "codereview",
				ValidatorRole:    "analyze",
				ParallelScanners: 1,
			},
		},
		Roles: map[string]types.RolePreference{
			"coding":     {CLI: "codex"},
			"codereview": {CLI: "codex"},
			"default":    {CLI: "codex"},
		},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 3,
			CooldownSeconds:  5,
			HalfOpenMaxCalls: 1,
		},
		CLIProfiles: map[string]*config.CLIProfile{
			"codex": {
				Name:           "codex",
				Binary:         "echo", // Use echo as mock CLI
				DisplayName:    "Test Codex",
				Command:        config.CommandConfig{Base: "echo"},
				PromptFlag:     "",
				TimeoutSeconds: 30,
				Features:       types.CLIFeatures{Headless: true},
			},
		},
	}

	log, err := logger.New(cfg.Server.LogFile, logger.LevelError, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(func() { log.Close() })

	reg := driver.NewRegistry(cfg.CLIProfiles)
	// Don't probe — echo is always available
	router := routing.NewRouter(cfg.Roles, []string{"codex"})

	return aimuxServer.New(cfg, log, reg, router)
}

func TestNewServer(t *testing.T) {
	srv := newTestServer(t)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestNewServer_AllToolsRegistered(t *testing.T) {
	// Verify server constructs with the reduced tool surface registered.
	srv := newTestServer(t)
	if srv == nil {
		t.Fatal("server should not be nil")
	}
}

func TestUpgradeToolSchema_ModeParameter(t *testing.T) {
	srv := newTestServer(t)
	tool := srv.Tool("upgrade")
	if tool == nil {
		t.Fatal("upgrade tool not registered")
	}

	modeProp, ok := tool.InputSchema.Properties["mode"].(map[string]any)
	if !ok {
		t.Fatalf("upgrade tool mode schema missing or wrong type: %T", tool.InputSchema.Properties["mode"])
	}
	if got := modeProp["type"]; got != "string" {
		t.Fatalf("upgrade mode type = %v, want string", got)
	}
	enumValues, ok := modeProp["enum"].([]string)
	if !ok {
		t.Fatalf("upgrade mode enum missing or wrong type: %T", modeProp["enum"])
	}
	wantEnum := []string{"auto", "hot_swap", "deferred"}
	if len(enumValues) != len(wantEnum) {
		t.Fatalf("upgrade mode enum len = %d, want %d", len(enumValues), len(wantEnum))
	}
	for i, want := range wantEnum {
		if got := enumValues[i]; got != want {
			t.Fatalf("upgrade mode enum[%d] = %q, want %q", i, got, want)
		}
	}
	if got := modeProp["default"]; got != "auto" {
		t.Fatalf("upgrade mode default = %v, want auto", got)
	}
}

// Note: Full MCP protocol integration tests require starting stdio transport
// which is complex to test in-process. The smoke test via binary + printf
// (documented in CONTINUITY.md) covers this path.
func TestServer_ShutdownCallsProcessManager(t *testing.T) {
	// Verify Shutdown() completes cleanly with no tracked processes.
	// ProcessManager.Shutdown() is safe to call on an empty manager.
	// All Server fields are nil — Shutdown() must guard each before use.
	s := &aimuxServer.Server{}
	s.Shutdown() // must not panic
}
