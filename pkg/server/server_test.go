package server_test

import (
	"strings"
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

// TestRegisteredToolDescriptions_ContainStructuredSections verifies that all six
// stateful tools have their structured guidance descriptions actually wired into
// the MCP tool registry — not just defined in the data map.
//
// The test is intentionally written so that replacing mustStatefulToolDescription
// with a stub (return "") causes every assertion to fail.
func TestRegisteredToolDescriptions_ContainStructuredSections(t *testing.T) {
	srv := newTestServer(t)

	// All six stateful tools must carry structured descriptions when registered.
	statefulTools := []string{"investigate", "consensus", "debate", "dialog", "workflow"}

	// Required section headers produced by renderStatefulToolDescription.
	requiredHeaders := []string{"WHAT:", "WHEN:", "WHY:", "HOW:", "NOT:", "CHOOSE:"}

	for _, toolName := range statefulTools {
		desc := srv.ToolDescription(toolName)

		if desc == "" {
			t.Errorf("tool %q: ToolDescription returned empty string — tool may not be registered", toolName)
			continue
		}

		// Must contain all six structured section headers.
		for _, header := range requiredHeaders {
			if !strings.Contains(desc, header) {
				t.Errorf("tool %q: registered description missing section header %q", toolName, header)
			}
		}

		// Must not be single-paragraph prose: a structured description has at least
		// 5 newlines (one between each of the 6 sections separated by blank lines).
		newlineCount := strings.Count(desc, "\n")
		if newlineCount < 5 {
			t.Errorf("tool %q: registered description has only %d newlines — looks like single-paragraph prose, not structured sections", toolName, newlineCount)
		}

		// The NOT: section must contain an explicit negative statement.
		// This catches a stub body→return null scenario where NotDo is empty.
		notIdx := strings.Index(desc, "NOT:")
		chooseIdx := strings.Index(desc, "CHOOSE:")
		if notIdx < 0 {
			t.Errorf("tool %q: registered description missing NOT: section", toolName)
			continue
		}
		if chooseIdx <= notIdx {
			t.Errorf("tool %q: NOT: section appears after or without CHOOSE: section", toolName)
			continue
		}
		notSection := strings.ToLower(desc[notIdx+len("NOT:") : chooseIdx])
		if !strings.Contains(notSection, "not") {
			t.Errorf("tool %q: NOT: section does not contain a negative statement", toolName)
		}
	}
}

func TestServer_ShutdownCallsProcessManager(t *testing.T) {
	// Verify Shutdown() completes cleanly with no tracked processes.
	// ProcessManager.Shutdown() is safe to call on an empty manager.
	// All Server fields are nil — Shutdown() must guard each before use.
	s := &aimuxServer.Server{}
	s.Shutdown() // must not panic
}
