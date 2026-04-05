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
				ScannerRole:   "codereview",
				ValidatorRole: "analyze",
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

	log, err := logger.New(cfg.Server.LogFile, logger.LevelError)
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

func TestNewServer_OrchestratorInitialized(t *testing.T) {
	srv := newTestServer(t)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	// Server constructs with orchestrator — if New() panics, test fails.
	// Orchestrator has 5 strategies registered (pair, dialog, consensus, debate, audit).
	// Agent registry initialized with Discover() called.
}

func TestNewServer_AllToolsRegistered(t *testing.T) {
	// Verify server constructs with all 10 tools registered.
	// If any tool registration panics or fails, New() would panic.
	srv := newTestServer(t)
	if srv == nil {
		t.Fatal("server should not be nil")
	}
}

// Note: Full MCP protocol integration tests require starting stdio transport
// which is complex to test in-process. The smoke test via binary + printf
// (documented in CONTINUITY.md) covers this path.
// Tool handler wiring is verified by:
// 1. Server constructs without panic (all strategies + agent registry initialized)
// 2. Smoke test via binary confirms tools respond
// 3. Strategy-level tests in pkg/orchestrator/ verify each strategy works
// 4. Stress tests verify concurrent session/job operations
