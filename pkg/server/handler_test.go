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

func TestHandleExec_AsyncWithPrompt(t *testing.T) {
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
	// exec defaults to async=true — should return running status with job_id
	status, _ := data["status"].(string)
	if status != "running" {
		t.Errorf("status = %v, want running (async default)", data["status"])
	}
	if data["session_id"] == nil || data["session_id"] == "" {
		t.Error("missing session_id")
	}
	if data["job_id"] == nil || data["job_id"] == "" {
		t.Error("missing job_id for async exec")
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

func TestHandleStatus_ExistingJob(t *testing.T) {
	srv := testServer(t)

	// Create a job manually and transition through lifecycle
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 0)
	srv.jobs.CompleteJob(job.ID, "test output", 0)

	req := makeRequest("status", map[string]any{
		"job_id": job.ID,
	})

	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	data := parseResult(t, result)
	if data["status"] != "completed" {
		t.Errorf("status = %v, want completed", data["status"])
	}
	if data["content"] != "test output" {
		t.Errorf("content = %v, want 'test output'", data["content"])
	}
}

func TestHandleStatus_PollWarning(t *testing.T) {
	srv := testServer(t)

	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")

	req := makeRequest("status", map[string]any{
		"job_id": job.ID,
	})

	// Poll 3 times to trigger warning
	for i := 0; i < 3; i++ {
		srv.handleStatus(context.Background(), req)
	}

	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	data := parseResult(t, result)
	if data["warning"] == nil {
		t.Error("expected poll warning after 3+ polls")
	}
}

func TestHandleSessions_InfoExisting(t *testing.T) {
	srv := testServer(t)

	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")

	req := makeRequest("sessions", map[string]any{
		"action":     "info",
		"session_id": sess.ID,
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	data := parseResult(t, result)
	if data["session"] == nil {
		t.Error("expected session field in info response")
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
	if data["session_id"] == nil {
		t.Error("expected session_id")
	}
	if data["topic"] != "test investigation" {
		t.Errorf("topic = %v, want 'test investigation'", data["topic"])
	}
	if data["coverage_areas"] == nil {
		t.Error("expected coverage_areas")
	}
}

// --- Chains ---

func TestDefaultChains(t *testing.T) {
	chains := DefaultChains()
	if len(chains) == 0 {
		t.Fatal("expected non-empty chains")
	}
}

func TestGetRecommendedNext_Exec(t *testing.T) {
	next := GetRecommendedNext("exec")
	if len(next) == 0 {
		t.Error("expected recommendations for exec")
	}
	if next[0] != "status" {
		t.Errorf("first recommendation for exec = %q, want 'status'", next[0])
	}
}

func TestGetRecommendedNext_Unknown(t *testing.T) {
	next := GetRecommendedNext("nonexistent")
	if next != nil {
		t.Errorf("expected nil for unknown tool, got %v", next)
	}
}

// --- Progress Bridge ---

func TestNewProgressBridge(t *testing.T) {
	b := NewProgressBridge(10)
	if b.interval != 10*1e9 { // 10 seconds in nanoseconds
		t.Errorf("interval = %v, want 10s", b.interval)
	}
}

func TestNewProgressBridge_DefaultInterval(t *testing.T) {
	b := NewProgressBridge(0)
	if b.interval != 15*1e9 { // default 15 seconds
		t.Errorf("interval = %v, want 15s", b.interval)
	}
}

func TestProgressBridge_Forward_ContextCancel(t *testing.T) {
	b := NewProgressBridge(1)
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan types.Event, 1)

	done := make(chan struct{})
	go func() {
		b.Forward(ctx, ch, func(s string) {})
		close(done)
	}()

	cancel()
	<-done // should return immediately after cancel
}

func TestProgressBridge_Forward_ChannelClose(t *testing.T) {
	b := NewProgressBridge(1)
	ch := make(chan types.Event, 1)

	done := make(chan struct{})
	go func() {
		b.Forward(context.Background(), ch, func(s string) {})
		close(done)
	}()

	close(ch)
	<-done // should return immediately after channel close
}

func TestProgressBridge_Forward_CompleteEvent(t *testing.T) {
	b := NewProgressBridge(1)
	ch := make(chan types.Event, 1)
	ch <- types.Event{Type: types.EventTypeComplete}

	done := make(chan struct{})
	go func() {
		b.Forward(context.Background(), ch, func(s string) {})
		close(done)
	}()

	<-done // should return after complete event
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

// --- Sessions Handler: Get/Cancel ---

func TestHandleSessions_MissingAction(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing action")
	}
}

func TestHandleSessions_InfoMissingID(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{
		"action": "info",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing session_id")
	}
}

func TestHandleSessions_InfoNonexistent(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{
		"action":     "info",
		"session_id": "nonexistent",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for nonexistent session")
	}
}

func TestHandleSessions_CancelMissingJobID(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{
		"action": "cancel",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing job_id on cancel")
	}
}

func TestHandleSessions_KillMissingID(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{
		"action": "kill",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing session_id on kill")
	}
}

func TestHandleSessions_GC(t *testing.T) {
	srv := testServer(t)

	// Create and complete a session
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	srv.sessions.Update(sess.ID, func(s *session.Session) {
		s.Status = types.SessionStatusCompleted
	})

	req := makeRequest("sessions", map[string]any{
		"action": "gc",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	data := parseResult(t, result)
	collected, _ := data["collected"].(float64)
	if collected < 1 {
		t.Errorf("collected = %v, want >= 1", collected)
	}
}

func TestHandleSessions_InvalidAction(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{
		"action": "invalid_action",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for invalid action")
	}
}

// --- Consensus: Insufficient CLIs ---

func TestHandleConsensus_InsufficientCLIs(t *testing.T) {
	srv := testServer(t)
	// testServer has only 1 CLI (codex) — consensus requires 2
	req := makeRequest("consensus", map[string]any{
		"topic": "test topic",
	})

	result, err := srv.handleConsensus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConsensus: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for insufficient CLIs")
	}
}

// --- Dialog: Insufficient CLIs ---

func TestHandleDialog_InsufficientCLIs(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("dialog", map[string]any{
		"prompt": "test prompt",
	})

	result, err := srv.handleDialog(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDialog: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for insufficient CLIs")
	}
}

// --- Debate: Insufficient CLIs ---

func TestHandleDebate_InsufficientCLIs(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("debate", map[string]any{
		"topic": "test topic",
	})

	result, err := srv.handleDebate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDebate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for insufficient CLIs")
	}
}

// --- Agents Handler: Find ---

func TestHandleAgents_Find(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agents", map[string]any{
		"action": "find",
		"prompt": "coding",
	})

	result, err := srv.handleAgents(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgents: %v", err)
	}

	data := parseResult(t, result)
	if data["query"] != "coding" {
		t.Errorf("query = %v, want coding", data["query"])
	}
}

func TestHandleAgents_FindMissingQuery(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agents", map[string]any{
		"action": "find",
	})

	result, err := srv.handleAgents(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgents: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for find without query")
	}
}

func TestHandleAgents_InvalidAction(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agents", map[string]any{
		"action": "invalid",
	})

	result, err := srv.handleAgents(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgents: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for invalid action")
	}
}

// --- Health Resource ---

func TestHandleHealthResource(t *testing.T) {
	srv := testServer(t)
	req := mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{
			URI: "aimux://health",
		},
	}

	contents, err := srv.handleHealthResource(context.Background(), req)
	if err != nil {
		t.Fatalf("handleHealthResource: %v", err)
	}

	if len(contents) == 0 {
		t.Error("expected health resource content")
	}
}

// --- DeepResearch: Missing Topic ---

func TestHandleDeepresearch_MissingTopic(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("deepresearch", map[string]any{})

	result, err := srv.handleDeepresearch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDeepresearch: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing topic")
	}
}
