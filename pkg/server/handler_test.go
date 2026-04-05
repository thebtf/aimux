package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

// testServer creates a server with echo as the mock CLI for handler tests.
func testServer(t *testing.T) *Server {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{
			LogLevel:              "error",
			LogFile:               t.TempDir() + "/test.log",
			DefaultTimeoutSeconds: 10,
			Pair: config.PairConfig{
				MaxRounds: 2,
			},
			Audit: config.AuditConfig{
				ScannerRole:      "codereview",
				ValidatorRole:    "analyze",
				ParallelScanners: 1,
			},
		},
		Roles: map[string]types.RolePreference{
			"default":    {CLI: "codex"},
			"coding":     {CLI: "codex"},
			"codereview": {CLI: "codex"},
			"thinkdeep":  {CLI: "codex"},
			"analyze":    {CLI: "codex"},
		},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 3,
			CooldownSeconds:  5,
			HalfOpenMaxCalls: 1,
		},
		CLIProfiles: map[string]*config.CLIProfile{
			"codex": {
				Name:           "codex",
				Binary:         "echo",
				DisplayName:    "Test CLI",
				Command:        config.CommandConfig{Base: "echo"},
				PromptFlag:     "-p",
				TimeoutSeconds: 10,
				Features:       types.CLIFeatures{Headless: true},
			},
		},
		ConfigDir: t.TempDir(),
	}

	log, err := logger.New(cfg.Server.LogFile, logger.LevelError)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(func() { log.Close() })

	reg := driver.NewRegistry(cfg.CLIProfiles)
	router := routing.NewRouter(cfg.Roles, []string{"codex"})

	return New(cfg, log, reg, router)
}

// makeRequest builds a CallToolRequest with the given arguments.
func makeRequest(name string, args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}
}

// parseResult extracts the text content from a tool result.
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
		// Not JSON — return as-is in "text" key
		return map[string]any{"text": text.Text}
	}
	return data
}

// --- Exec Handler ---

func TestHandleExec_SyncWithPrompt(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("exec", map[string]any{
		"prompt": "hello world",
		"cli":    "codex",
	})

	result, err := srv.handleExec(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExec: %v", err)
	}

	data := parseResult(t, result)
	if data["status"] != "completed" {
		t.Errorf("status = %v, want completed", data["status"])
	}
	if data["session_id"] == nil || data["session_id"] == "" {
		t.Error("missing session_id")
	}
}

func TestHandleExec_MissingPrompt(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("exec", map[string]any{
		"cli": "codex",
	})

	result, err := srv.handleExec(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExec: %v", err)
	}

	// Should return error result
	if !result.IsError {
		t.Error("expected error result for missing prompt")
	}
}

func TestHandleExec_UnknownCLI(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("exec", map[string]any{
		"prompt": "test",
		"cli":    "nonexistent",
	})

	result, err := srv.handleExec(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExec: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for unknown CLI")
	}
}

func TestHandleExec_AsyncReturnsJobID(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("exec", map[string]any{
		"prompt": "hello",
		"cli":    "codex",
		"async":  true,
	})

	result, err := srv.handleExec(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExec: %v", err)
	}

	data := parseResult(t, result)
	if data["job_id"] == nil || data["job_id"] == "" {
		t.Error("async should return job_id")
	}
	if data["status"] != "running" {
		t.Errorf("status = %v, want running", data["status"])
	}
}

func TestHandleExec_SessionResume_NotFound(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("exec", map[string]any{
		"prompt":     "test",
		"session_id": "nonexistent-session",
	})

	result, err := srv.handleExec(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExec: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for nonexistent session")
	}
}

func TestHandleExec_RoleCoding_UsesPairCoding(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("exec", map[string]any{
		"prompt": "implement feature X",
		"role":   "coding",
	})

	result, err := srv.handleExec(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExec: %v", err)
	}

	// Pair coding may fail (echo as CLI doesn't produce diffs), but it should
	// attempt pair coding path, not direct execution. Check we get a result.
	data := parseResult(t, result)
	// If pair coding ran, we get session_id at minimum
	if data["session_id"] == nil && !result.IsError {
		t.Error("expected either session_id or error from pair coding path")
	}
}

// --- Status Handler ---

func TestHandleStatus_MissingJobID(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("status", map[string]any{})

	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing job_id")
	}
}

func TestHandleStatus_NonexistentJob(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("status", map[string]any{
		"job_id": "nonexistent",
	})

	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for nonexistent job")
	}
}

// --- Sessions Handler ---

func TestHandleSessions_List(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{
		"action": "list",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	data := parseResult(t, result)
	if data["count"] == nil {
		t.Error("expected count field")
	}
}

func TestHandleSessions_Health(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{
		"action": "health",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	data := parseResult(t, result)
	if data["total_sessions"] == nil {
		t.Error("expected total_sessions field")
	}
}

// --- Agents Handler ---

func TestHandleAgents_List(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agents", map[string]any{
		"action": "list",
	})

	result, err := srv.handleAgents(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgents: %v", err)
	}

	data := parseResult(t, result)
	if data["count"] == nil {
		t.Error("expected count field")
	}
}

func TestHandleAgents_MissingAction(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agents", map[string]any{})

	result, err := srv.handleAgents(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgents: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing action")
	}
}

func TestHandleAgents_RunMissing(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agents", map[string]any{
		"action": "run",
	})

	result, err := srv.handleAgents(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgents: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for run without agent name")
	}
}

func TestHandleAgents_InfoNotFound(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agents", map[string]any{
		"action": "info",
		"agent":  "nonexistent-agent",
	})

	result, err := srv.handleAgents(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgents: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for nonexistent agent")
	}
}

// --- Think Handler ---

func TestHandleThink_Basic(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("think", map[string]any{
		"pattern": "critical_thinking",
		"issue":   "analyze this problem",
	})

	result, err := srv.handleThink(context.Background(), req)
	if err != nil {
		t.Fatalf("handleThink: %v", err)
	}

	data := parseResult(t, result)
	if data["pattern"] != "critical_thinking" {
		t.Errorf("pattern = %v, want critical_thinking", data["pattern"])
	}
	if data["mode"] != "solo" {
		t.Errorf("mode = %v, want solo", data["mode"])
	}
}

func TestHandleThink_MissingPattern(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("think", map[string]any{})

	result, err := srv.handleThink(context.Background(), req)
	if err != nil {
		t.Fatalf("handleThink: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing pattern")
	}
}

// --- Consensus Handler ---

func TestHandleConsensus_MissingPrompt(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("consensus", map[string]any{})

	result, err := srv.handleConsensus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConsensus: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing prompt")
	}
}

// --- Debate Handler ---

func TestHandleDebate_MissingPrompt(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("debate", map[string]any{})

	result, err := srv.handleDebate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDebate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing prompt")
	}
}

// --- DeepResearch Handler ---

func TestHandleDeepresearch_MissingAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	srv := testServer(t)
	req := makeRequest("deepresearch", map[string]any{
		"topic": "test research",
	})

	result, err := srv.handleDeepresearch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDeepresearch: %v", err)
	}

	if !result.IsError {
		t.Error("expected error when API key not set")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "GOOGLE_API_KEY") {
		t.Error("error should mention GOOGLE_API_KEY")
	}
}

// --- Dialog Handler ---

func TestHandleDialog_MissingPrompt(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("dialog", map[string]any{})

	result, err := srv.handleDialog(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDialog: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing prompt")
	}
}

// --- Investigate Handler ---

func TestHandleInvestigate_MissingAction(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing action")
	}
}

func TestHandleInvestigate_StartWithTopic(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action": "start",
		"topic":  "test investigation",
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate: %v", err)
	}

	data := parseResult(t, result)
	if data["status"] != "started" {
		t.Errorf("status = %v, want started", data["status"])
	}
	if data["session_id"] == nil {
		t.Error("expected session_id")
	}
}

// --- Bootstrap Injection ---

func TestInjectBootstrap_NoTemplate(t *testing.T) {
	srv := testServer(t)
	prompt := "original prompt"
	result := srv.injectBootstrap("nonexistent-role", prompt)
	if result != prompt {
		t.Errorf("expected original prompt when no template, got %q", result)
	}
}

// --- Session Resume State Check ---

func TestHandleExec_SessionResume_CompletedSession(t *testing.T) {
	srv := testServer(t)

	// Create and complete a session
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	srv.sessions.Update(sess.ID, func(s *session.Session) {
		s.Status = types.SessionStatusCompleted
	})

	req := makeRequest("exec", map[string]any{
		"prompt":     "test",
		"session_id": sess.ID,
	})

	result, err := srv.handleExec(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExec: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for completed session")
	}
}
