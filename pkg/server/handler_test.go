package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/agents"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/guidance"
	inv "github.com/thebtf/aimux/pkg/investigate"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

type stubExecutor struct {
	run func(ctx context.Context, args types.SpawnArgs) (*types.Result, error)
}

func (s *stubExecutor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	if s.run != nil {
		return s.run(ctx, args)
	}
	return &types.Result{Content: "stub executor output", ExitCode: 0}, nil
}

func (s *stubExecutor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	return nil, errors.New("stub executor does not support Start")
}

func (s *stubExecutor) Name() string { return "stub" }

func (s *stubExecutor) Available() bool { return true }

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
			"coding":     {CLI: "codex", Model: "gpt-5.3-codex", ReasoningEffort: "medium"},
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
				Name:        "codex",
				Binary:      "echo",
				DisplayName: "Test CLI",
				Command:     config.CommandConfig{Base: "echo"},
				PromptFlag:  "-p",
				ModelFlag:   "-m",
				Reasoning: &config.ReasoningConfig{
					Flag:              "-c",
					FlagValueTemplate: "model_reasoning_effort=%s",
					Levels:            []string{"low", "medium", "high", "xhigh"},
				},
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
	resultData, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatal("expected nested result payload")
	}
	if resultData["session_id"] == nil {
		t.Error("expected result.session_id")
	}
	if resultData["topic"] != "test investigation" {
		t.Errorf("result.topic = %v, want 'test investigation'", resultData["topic"])
	}
	if resultData["coverage_areas"] == nil {
		t.Error("expected result.coverage_areas")
	}
	if data["state"] == nil {
		t.Error("expected state guidance field")
	}
	if data["how_this_tool_works"] == nil {
		t.Error("expected how_this_tool_works guidance field")
	}
	if data["choose_your_path"] == nil {
		t.Error("expected choose_your_path guidance field")
	}
	if data["do_not"] == nil {
		t.Error("expected do_not guidance field")
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

func TestNormalizeProgressLine_JSONLAgentMessage(t *testing.T) {
	line := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"hello"}}`
	if got := normalizeProgressLine("jsonl", line); got != "hello" {
		t.Fatalf("normalizeProgressLine(jsonl) = %q, want hello", got)
	}
}

func TestNormalizeProgressLine_JSONLSuppressesControlEvents(t *testing.T) {
	line := `{"type":"turn.started"}`
	if got := normalizeProgressLine("jsonl", line); got != "" {
		t.Fatalf("normalizeProgressLine(jsonl control) = %q, want empty", got)
	}
}

func TestNormalizeProgressLine_JSONExtractsContent(t *testing.T) {
	line := `{"type":"message","role":"assistant","content":"chunk","delta":true}`
	if got := normalizeProgressLine("json", line); got != "chunk" {
		t.Fatalf("normalizeProgressLine(json) = %q, want chunk", got)
	}
}

func TestNormalizeProgressLine_JSONSuppressesInit(t *testing.T) {
	line := `{"type":"init","session_id":"abc"}`
	if got := normalizeProgressLine("json", line); got != "" {
		t.Fatalf("normalizeProgressLine(json init) = %q, want empty", got)
	}
}

func TestAgentBusyEstimateMs_DefaultTurns(t *testing.T) {
	if got := agentBusyEstimateMs(30, 0); got != 30000 {
		t.Fatalf("agentBusyEstimateMs(30,0) = %d, want 30000", got)
	}
}

func TestAgentBusyEstimateMs_MultiTurn(t *testing.T) {
	if got := agentBusyEstimateMs(30, 3); got != 90000 {
		t.Fatalf("agentBusyEstimateMs(30,3) = %d, want 90000", got)
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

func TestHandleSessions_KillExistingSession(t *testing.T) {
	srv := testServer(t)
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 123)
	srv.jobs.SetPheromone(job.ID, "k", "v")

	req := makeRequest("sessions", map[string]any{
		"action":     "kill",
		"session_id": sess.ID,
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions kill: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected successful kill")
	}

	live := srv.jobs.Get(job.ID)
	if live == nil {
		t.Fatal("expected job to still exist after kill")
	}
	if live.Status != types.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", live.Status)
	}
	if live.Error == nil || live.Error.Message != "session killed" {
		t.Fatalf("job error = %#v, want message session killed", live.Error)
	}
	if live.Pheromones["k"] != "v" {
		t.Fatalf("job pheromone = %q, want v", live.Pheromones["k"])
	}

	if got := srv.sessions.Get(sess.ID); got != nil {
		t.Fatal("expected session to be deleted after kill")
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

// --- Pure helpers ---

func TestExtractPromptFromArgs_LastPositional(t *testing.T) {
	args := []string{"-p", "my prompt"}
	got := extractPromptFromArgs(args)
	if got != "my prompt" {
		t.Errorf("got %q, want %q", got, "my prompt")
	}
}

func TestExtractPromptFromArgs_AllFlags(t *testing.T) {
	args := []string{"--flag1", "--flag2"}
	got := extractPromptFromArgs(args)
	if got != "" {
		t.Errorf("expected empty for all-flag args, got %q", got)
	}
}

func TestExtractPromptFromArgs_Empty(t *testing.T) {
	got := extractPromptFromArgs(nil)
	if got != "" {
		t.Errorf("expected empty for nil args, got %q", got)
	}
}

func TestIsRetriableError_RateLimit(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"rate limit exceeded", true},
		{"quota exceeded for API", true},
		{"429 Too Many Requests", true},
		{"authentication failed", true},
		{"connection refused", true},
		{"ETIMEDOUT", true},
		{"ECONNREFUSED", true},
		{"ENOTFOUND", true},
		{"dns resolution failed", true},
		{"some random error", false},
		{"file not found", false},
		{"", false},
	}
	for _, c := range cases {
		got := isRetriableError(c.msg)
		if got != c.want {
			t.Errorf("isRetriableError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestIsRetriableValidationError_Propagates(t *testing.T) {
	if isRetriableValidationError([]string{"some error", "rate limit hit"}) != true {
		t.Error("expected true when one error is retriable")
	}
	if isRetriableValidationError([]string{"error1", "error2"}) != false {
		t.Error("expected false when no error is retriable")
	}
	if isRetriableValidationError(nil) != false {
		t.Error("expected false for nil errors")
	}
}

func TestEnsureLocalhostBinding(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{":8080", "127.0.0.1:8080"},
		{"0.0.0.0:9090", "127.0.0.1:9090"},
		{"127.0.0.1:8080", "127.0.0.1:8080"},
		{"example.com:80", "example.com:80"},
	}
	for _, c := range cases {
		got := ensureLocalhostBinding(c.in)
		if got != c.want {
			t.Errorf("ensureLocalhostBinding(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsLocalhostAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"localhost:3000", true},
		{"[::1]:8080", true},
		{"0.0.0.0:8080", false},
		{"192.168.1.1:8080", false},
	}
	for _, c := range cases {
		got := isLocalhostAddr(c.addr)
		if got != c.want {
			t.Errorf("isLocalhostAddr(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// --- Audit Handler ---

func TestHandleAudit_Async(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("audit", map[string]any{
		"mode":  "standard",
		"async": true,
	})

	result, err := srv.handleAudit(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAudit async: %v", err)
	}

	data := parseResult(t, result)
	if data["job_id"] == nil || data["job_id"] == "" {
		t.Error("expected job_id for async audit")
	}
	if data["status"] != "running" {
		t.Errorf("status = %v, want running", data["status"])
	}
}

// --- AgentRun Handler ---

func TestHandleAgentRun_MissingAgent(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agent", map[string]any{
		"prompt": "do something",
	})

	result, err := srv.handleAgentRun(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgentRun: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing agent")
	}
}

func TestHandleAgentRun_MissingPrompt(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agent", map[string]any{
		"agent": "nonexistent",
	})

	result, err := srv.handleAgentRun(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgentRun: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing prompt")
	}
}

func TestHandleAgentRun_AgentNotFound(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agent", map[string]any{
		"agent":  "nonexistent-agent-xyz",
		"prompt": "do something",
	})

	result, err := srv.handleAgentRun(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgentRun: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for nonexistent agent")
	}
	text := parseResult(t, result)
	textStr, _ := text["text"].(string)
	if !strings.Contains(textStr, "nonexistent-agent-xyz") {
		t.Errorf("error should mention agent name, got: %s", textStr)
	}
}

// --- Workflow Handler ---

func TestHandleWorkflow_MissingSteps(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("workflow", map[string]any{})

	result, err := srv.handleWorkflow(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWorkflow: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for missing steps")
	}
}

func TestHandleWorkflow_InvalidJSON(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("workflow", map[string]any{
		"steps": "not-valid-json",
	})

	result, err := srv.handleWorkflow(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWorkflow: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for invalid steps JSON")
	}
}

func TestHandleWorkflow_Async(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("workflow", map[string]any{
		"steps": `[{"role":"default","prompt":"hello"}]`,
		"async": true,
	})

	result, err := srv.handleWorkflow(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWorkflow async: %v", err)
	}

	data := parseResult(t, result)
	if data["job_id"] == nil || data["job_id"] == "" {
		t.Error("expected job_id for async workflow")
	}
	if data["status"] != "running" {
		t.Errorf("status = %v, want running", data["status"])
	}
}

// --- Metrics Resource ---

func TestHandleMetricsResource(t *testing.T) {
	srv := testServer(t)
	req := mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{
			URI: "aimux://metrics",
		},
	}

	contents, err := srv.handleMetricsResource(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMetricsResource: %v", err)
	}

	if len(contents) == 0 {
		t.Error("expected metrics resource content")
	}
}

// --- buildSkillData / handleSkillPrompt ---

func TestBuildSkillData(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Arguments: map[string]string{"key": "value"},
		},
	}

	data := srv.buildSkillData(req)
	if data == nil {
		t.Fatal("buildSkillData returned nil")
	}
	// EnabledCLIs may be nil or empty — both valid when no CLIs are probed available yet.
	if data.RoleRouting == nil {
		t.Error("RoleRouting should not be nil")
	}
	if data.Args["key"] != "value" {
		t.Errorf("args[key] = %q, want %q", data.Args["key"], "value")
	}
}

func TestBuildSkillData_NoArgs(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{}

	data := srv.buildSkillData(req)
	if data == nil {
		t.Fatal("buildSkillData returned nil")
	}
	if data.Args == nil {
		t.Error("Args should be non-nil map even with no request args")
	}
}

// --- Investigate: additional actions ---

func TestHandleInvestigate_StartMissingTopic(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action": "start",
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for start without topic")
	}
}

func TestHandleInvestigate_FindingMissingSessionID(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action":      "finding",
		"description": "a bug",
		"source":      "file.go:42",
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for finding without session_id")
	}
}

func TestHandleInvestigate_AssessMissingSessionID(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action": "assess",
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for assess without session_id")
	}
}

func TestHandleInvestigate_ReportMissingSessionID(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action": "report",
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for report without session_id")
	}
}

func TestHandleInvestigate_StatusMissingSessionID(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action": "status",
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for status without session_id")
	}
}

func TestHandleInvestigate_List(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action": "list",
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate list: %v", err)
	}

	data := parseResult(t, result)
	resultData, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatal("expected result payload")
	}
	if resultData["active_count"] == nil {
		t.Error("expected result.active_count field")
	}
}

func TestMarshalGuidedToolResult_NestsRawPayloadUnderResult(t *testing.T) {
	state := map[string]any{"step": "start"}
	raw := map[string]any{"session_id": "sess-123", "topic": "test investigation"}

	toolResult, err := marshalGuidedToolResult("investigate", "start", state, raw)
	if err != nil {
		t.Fatalf("marshalGuidedToolResult: %v", err)
	}

	data := parseResult(t, toolResult)
	if _, exists := data["session_id"]; exists {
		t.Fatal("unexpected top-level session_id; guided payload must be nested under result")
	}

	resultData, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested result object, got %T", data["result"])
	}
	if resultData["session_id"] != "sess-123" {
		t.Fatalf("result.session_id = %v, want sess-123", resultData["session_id"])
	}
	if resultData["topic"] != "test investigation" {
		t.Fatalf("result.topic = %v, want test investigation", resultData["topic"])
	}
}

func TestGuidanceFallbackProductionMode_MissingPolicyReturnsNestedResultWithoutPanic(t *testing.T) {
	t.Setenv("AIMUX_ENV", "")

	rawGuided := map[string]any{
		"result": map[string]any{
			"job_id":  "job-1",
			"session": "sess-1",
		},
	}

	registry := guidance.NewRegistry()
	policy, fallback, err := registry.Resolve("workflow", rawGuided)
	if err != nil {
		t.Fatalf("Resolve missing policy in production mode: %v", err)
	}
	if policy != nil {
		t.Fatal("expected nil policy for missing workflow policy")
	}
	if fallback == nil {
		t.Fatal("expected fallback envelope for missing policy")
	}
	if fallback.State != guidance.StateGuidanceNotImplemented {
		t.Fatalf("fallback.State = %q, want %q", fallback.State, guidance.StateGuidanceNotImplemented)
	}

	marshaled, err := marshalToolResult(fallback)
	if err != nil {
		t.Fatalf("marshalToolResult(fallback): %v", err)
	}

	data := parseResult(t, marshaled)
	if data["state"] != guidance.StateGuidanceNotImplemented {
		t.Fatalf("state = %v, want %s", data["state"], guidance.StateGuidanceNotImplemented)
	}

	resultData, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested result object, got %T", data["result"])
	}
	if resultData["job_id"] != "job-1" {
		t.Fatalf("result.job_id = %v, want job-1", resultData["job_id"])
	}
	if resultData["session"] != "sess-1" {
		t.Fatalf("result.session = %v, want sess-1", resultData["session"])
	}
}

func TestHandleInvestigate_RecallMissingTopic(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action": "recall",
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for recall without topic")
	}
}

func TestHandleInvestigate_RecallNotFound(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action": "recall",
		"topic":  "nonexistent-topic-xyz-12345",
		"cwd":    t.TempDir(),
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate recall: %v", err)
	}

	// Verify the recall code path was exercised and returned a proper response.
	if result.IsError {
		// An error result here would mean the handler failed internally, not a
		// cache miss — that would be a real bug, not the expected not-found path.
		t.Errorf("recall returned an error result: %v", result)
	}
	data := parseResult(t, result)
	resultPayload, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatal("expected result payload")
	}
	found, _ := resultPayload["found"].(bool)
	if found {
		t.Error("expected found=false for nonexistent topic")
	}
}

func TestHandleInvestigate_FindingWhenCoverageCompletePromotesAssess(t *testing.T) {
	srv := testServer(t)
	startReq := makeRequest("investigate", map[string]any{
		"action": "start",
		"topic":  "server crash on startup",
	})
	startResult, err := srv.handleInvestigate(context.Background(), startReq)
	if err != nil {
		t.Fatalf("investigate start: %v", err)
	}
	startData := parseResult(t, startResult)
	startResultData, ok := startData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected start result payload")
	}
	sessionID, _ := startResultData["session_id"].(string)
	if sessionID == "" {
		t.Fatal("expected session_id")
	}

	areas := []string{"reproduction", "isolation", "hypothesis_formation", "root_cause_analysis", "fix_verification", "regression_prevention", "environmental_factors", "error_trail"}
	previousGapCount := len(areas)
	for i, area := range areas {
		findingReq := makeRequest("investigate", map[string]any{
			"action":        "finding",
			"session_id":    sessionID,
			"description":   "finding " + area,
			"source":        "file.go:42",
			"severity":      "P2",
			"coverage_area": area,
		})
		findingResult, findErr := srv.handleInvestigate(context.Background(), findingReq)
		if findErr != nil {
			t.Fatalf("finding %d: %v", i, findErr)
		}
		data := parseResult(t, findingResult)
		gaps, ok := data["gaps"].([]any)
		if i < len(areas)-1 {
			if !ok {
				t.Fatalf("expected gaps array after finding %d, got %T", i, data["gaps"])
			}
			if len(gaps) >= previousGapCount {
				t.Fatalf("gaps did not shrink after finding %d: got %d want < %d", i, len(gaps), previousGapCount)
			}
			previousGapCount = len(gaps)
			continue
		}
		if ok && len(gaps) != 0 {
			t.Fatalf("expected no gaps after full coverage, got %v", gaps)
		}
		choose, ok := data["choose_your_path"].(map[string]any)
		if !ok {
			t.Fatal("expected choose_your_path after full coverage")
		}
		self, ok := choose["self"].(map[string]any)
		if !ok {
			t.Fatal("expected self branch after full coverage")
		}
		nextCall, _ := self["next_call"].(string)
		if nextCall != `investigate(action="assess", session_id="<session_id>")` && nextCall != `investigate(action="report", session_id="<session_id>")` {
			t.Fatalf("expected assess/report next_call after full coverage, got %q", nextCall)
		}
	}
}

func TestHandleInvestigate_ReportZeroFindings_BlockedKeepsSession(t *testing.T) {
	srv := testServer(t)

	startReq := makeRequest("investigate", map[string]any{
		"action": "start",
		"topic":  "no findings yet",
	})
	startResult, err := srv.handleInvestigate(context.Background(), startReq)
	if err != nil {
		t.Fatalf("investigate start: %v", err)
	}
	startData := parseResult(t, startResult)
	startPayload, ok := startData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected start result payload")
	}
	sessionID, _ := startPayload["session_id"].(string)
	if sessionID == "" {
		t.Fatal("expected session_id")
	}

	reportReq := makeRequest("investigate", map[string]any{
		"action":     "report",
		"session_id": sessionID,
	})
	reportResult, err := srv.handleInvestigate(context.Background(), reportReq)
	if err != nil {
		t.Fatalf("investigate report: %v", err)
	}
	reportData := parseResult(t, reportResult)
	if reportData["state"] != "report_blocked" {
		t.Fatalf("state = %v, want report_blocked", reportData["state"])
	}
	if _, leaked := reportData["report"]; leaked {
		t.Fatal("unexpected top-level report field leak")
	}
	reportPayload, ok := reportData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected report result payload")
	}
	reportText, _ := reportPayload["report"].(string)
	if reportText == "" {
		t.Fatal("expected non-empty result.report")
	}
	choose, ok := reportData["choose_your_path"].(map[string]any)
	if !ok {
		t.Fatal("expected choose_your_path")
	}
	self, ok := choose["self"].(map[string]any)
	if !ok {
		t.Fatal("expected self path")
	}
	nextCall, _ := self["next_call"].(string)
	if nextCall != `investigate(action="finding", session_id="<session_id>", description="...", source="...", severity="P2")` {
		t.Fatalf("next_call = %q, want finding corrective call", nextCall)
	}

	statusReq := makeRequest("investigate", map[string]any{
		"action":     "status",
		"session_id": sessionID,
	})
	statusResult, err := srv.handleInvestigate(context.Background(), statusReq)
	if err != nil {
		t.Fatalf("investigate status after blocked report: %v", err)
	}
	if statusResult.IsError {
		t.Fatalf("status should remain usable after blocked report, got error result: %+v", statusResult)
	}
}

func TestHandleInvestigate_ReportWeakEvidence_PreliminaryKeepsSession(t *testing.T) {
	srv := testServer(t)

	startReq := makeRequest("investigate", map[string]any{
		"action": "start",
		"topic":  "startup crash",
	})
	startResult, err := srv.handleInvestigate(context.Background(), startReq)
	if err != nil {
		t.Fatalf("investigate start: %v", err)
	}
	startData := parseResult(t, startResult)
	startPayload, ok := startData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected start result payload")
	}
	sessionID, _ := startPayload["session_id"].(string)
	if sessionID == "" {
		t.Fatal("expected session_id")
	}

	findingReq := makeRequest("investigate", map[string]any{
		"action":      "finding",
		"session_id":  sessionID,
		"description": "single weak finding",
		"source":      "main.go:42",
		"severity":    "P2",
	})
	if _, err := srv.handleInvestigate(context.Background(), findingReq); err != nil {
		t.Fatalf("investigate finding: %v", err)
	}

	reportReq := makeRequest("investigate", map[string]any{
		"action":     "report",
		"session_id": sessionID,
	})
	reportResult, err := srv.handleInvestigate(context.Background(), reportReq)
	if err != nil {
		t.Fatalf("investigate report: %v", err)
	}
	reportData := parseResult(t, reportResult)
	if reportData["state"] != "report_preliminary" {
		t.Fatalf("state = %v, want report_preliminary", reportData["state"])
	}
	if _, leaked := reportData["report"]; leaked {
		t.Fatal("unexpected top-level report field leak")
	}
	reportPayload, ok := reportData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected report result payload")
	}
	reportText, _ := reportPayload["report"].(string)
	if reportText == "" {
		t.Fatal("expected non-empty result.report")
	}
	choose, ok := reportData["choose_your_path"].(map[string]any)
	if !ok {
		t.Fatal("expected choose_your_path")
	}
	self, ok := choose["self"].(map[string]any)
	if !ok {
		t.Fatal("expected self path")
	}
	nextCall, _ := self["next_call"].(string)
	if nextCall != `investigate(action="assess", session_id="<session_id>")` {
		t.Fatalf("next_call = %q, want assess corrective call", nextCall)
	}

	statusReq := makeRequest("investigate", map[string]any{
		"action":     "status",
		"session_id": sessionID,
	})
	statusResult, err := srv.handleInvestigate(context.Background(), statusReq)
	if err != nil {
		t.Fatalf("investigate status after preliminary report: %v", err)
	}
	if statusResult.IsError {
		t.Fatalf("status should remain usable after preliminary report, got error result: %+v", statusResult)
	}
}

func TestHandleInvestigate_ReportWeakEvidenceForce_IncompleteForcedKeepsSession(t *testing.T) {
	srv := testServer(t)

	startReq := makeRequest("investigate", map[string]any{
		"action": "start",
		"topic":  "startup crash",
	})
	startResult, err := srv.handleInvestigate(context.Background(), startReq)
	if err != nil {
		t.Fatalf("investigate start: %v", err)
	}
	startData := parseResult(t, startResult)
	startPayload, ok := startData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected start result payload")
	}
	sessionID, _ := startPayload["session_id"].(string)
	if sessionID == "" {
		t.Fatal("expected session_id")
	}

	findingReq := makeRequest("investigate", map[string]any{
		"action":      "finding",
		"session_id":  sessionID,
		"description": "single weak finding",
		"source":      "main.go:42",
		"severity":    "P2",
	})
	if _, err := srv.handleInvestigate(context.Background(), findingReq); err != nil {
		t.Fatalf("investigate finding: %v", err)
	}

	reportReq := makeRequest("investigate", map[string]any{
		"action":     "report",
		"session_id": sessionID,
		"force":      true,
	})
	reportResult, err := srv.handleInvestigate(context.Background(), reportReq)
	if err != nil {
		t.Fatalf("investigate report force=true: %v", err)
	}
	reportData := parseResult(t, reportResult)
	if reportData["state"] != "report_incomplete_forced" {
		t.Fatalf("state = %v, want report_incomplete_forced", reportData["state"])
	}
	reportPayload, ok := reportData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected report result payload")
	}
	reportText, _ := reportPayload["report"].(string)
	if reportText == "" {
		t.Fatal("expected non-empty result.report")
	}
	metadata, ok := reportPayload["metadata"].(map[string]any)
	if !ok {
		t.Fatal("expected result.metadata")
	}
	force, _ := metadata["force"].(bool)
	if !force {
		t.Fatalf("result.metadata.force = %v, want true", metadata["force"])
	}
	choose, ok := reportData["choose_your_path"].(map[string]any)
	if !ok {
		t.Fatal("expected choose_your_path")
	}
	self, ok := choose["self"].(map[string]any)
	if !ok {
		t.Fatal("expected self path")
	}
	nextCall, _ := self["next_call"].(string)
	if nextCall != `investigate(action="assess", session_id="<session_id>")` {
		t.Fatalf("next_call = %q, want assess corrective call", nextCall)
	}

	statusReq := makeRequest("investigate", map[string]any{
		"action":     "status",
		"session_id": sessionID,
	})
	statusResult, err := srv.handleInvestigate(context.Background(), statusReq)
	if err != nil {
		t.Fatalf("investigate status after forced incomplete report: %v", err)
	}
	if statusResult.IsError {
		t.Fatalf("status should remain usable after forced incomplete report, got error result: %+v", statusResult)
	}
}

func TestHandleInvestigate_InvalidAction(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("investigate", map[string]any{
		"action": "unknown_action",
	})

	result, err := srv.handleInvestigate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigate: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for unknown action")
	}
}

func TestHandleInvestigate_FullCycle(t *testing.T) {
	srv := testServer(t)

	// 1. Start — omit domain to let AutoDetectDomain pick "debugging" from "crash"
	startReq := makeRequest("investigate", map[string]any{
		"action": "start",
		"topic":  "server crash on startup",
	})
	startResult, err := srv.handleInvestigate(context.Background(), startReq)
	if err != nil {
		t.Fatalf("investigate start: %v", err)
	}
	startData := parseResult(t, startResult)
	startResultData, ok := startData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected start result payload")
	}
	sessionID, ok := startResultData["session_id"].(string)
	if !ok || sessionID == "" {
		t.Fatal("expected session_id from start")
	}

	// 2. Status
	statusReq := makeRequest("investigate", map[string]any{
		"action":     "status",
		"session_id": sessionID,
	})
	statusResult, err := srv.handleInvestigate(context.Background(), statusReq)
	if err != nil {
		t.Fatalf("investigate status: %v", err)
	}
	statusData := parseResult(t, statusResult)
	statusResultData, ok := statusData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected status result payload")
	}
	if statusResultData["session_id"] != sessionID {
		t.Errorf("result.session_id = %v, want %q", statusResultData["session_id"], sessionID)
	}
	if statusResultData["topic"] != "server crash on startup" {
		t.Errorf("result.topic = %v, want 'server crash on startup'", statusResultData["topic"])
	}
	if statusResultData["iteration"] == nil {
		t.Error("expected result.iteration")
	}
	if statusResultData["findings_count"] == nil {
		t.Error("expected result.findings_count")
	}
	if statusResultData["coverage_unchecked"] == nil {
		t.Error("expected result.coverage_unchecked")
	}

	// 3. Finding
	findingReq := makeRequest("investigate", map[string]any{
		"action":      "finding",
		"session_id":  sessionID,
		"description": "Null pointer dereference in init()",
		"source":      "main.go:42",
		"severity":    "P0",
	})
	findingResult, err := srv.handleInvestigate(context.Background(), findingReq)
	if err != nil {
		t.Fatalf("investigate finding: %v", err)
	}
	findingData := parseResult(t, findingResult)
	findingResultData, ok := findingData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected finding result payload")
	}
	if findingResultData["finding_id"] == nil {
		t.Error("expected finding_id")
	}
	if gaps, ok := findingData["gaps"].([]any); !ok || len(gaps) == 0 {
		t.Fatalf("expected non-empty gaps after first finding, got %v", findingData["gaps"])
	}
	choose, ok := findingData["choose_your_path"].(map[string]any)
	if !ok {
		t.Fatal("expected choose_your_path in finding response")
	}
	self, ok := choose["self"].(map[string]any)
	if !ok {
		t.Fatal("expected self branch in finding response")
	}
	if nextCall, _ := self["next_call"].(string); nextCall == `investigate(action="assess", session_id="<session_id>")` || nextCall == `investigate(action="report", session_id="<session_id>")` {
		t.Fatalf("finding next_call switched too early: %q", nextCall)
	}

	// 4. Assess
	assessReq := makeRequest("investigate", map[string]any{
		"action":     "assess",
		"session_id": sessionID,
	})
	assessResult, err := srv.handleInvestigate(context.Background(), assessReq)
	if err != nil {
		t.Fatalf("investigate assess: %v", err)
	}
	if assessResult.IsError {
		t.Errorf("assess returned error: %v", assessResult.Content)
	}

	// 5. Report (also deletes the session)
	reportReq := makeRequest("investigate", map[string]any{
		"action":     "report",
		"session_id": sessionID,
	})
	reportResult, err := srv.handleInvestigate(context.Background(), reportReq)
	if err != nil {
		t.Fatalf("investigate report: %v", err)
	}
	reportData := parseResult(t, reportResult)
	reportResultData, ok := reportData["result"].(map[string]any)
	if !ok {
		t.Fatal("expected report result payload")
	}
	if reportResultData["report"] == nil {
		t.Error("expected report content")
	}
}

func TestHandleInvestigate_AutoReturnsJobIDAndPersistsDelegateResult(t *testing.T) {
	srv := testServer(t)
	srv.executor = &stubExecutor{run: func(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
		return &types.Result{Content: "delegate completed root cause found", ExitCode: 0}, nil
	}}

	autoReq := makeRequest("investigate", map[string]any{
		"action": "auto",
		"topic":  "startup crash in bootstrap",
		"cli":    "codex",
	})
	autoResult, err := srv.handleInvestigate(context.Background(), autoReq)
	if err != nil {
		t.Fatalf("investigate auto: %v", err)
	}
	if autoResult.IsError {
		t.Fatalf("investigate auto returned error result: %+v", autoResult)
	}

	autoData := parseResult(t, autoResult)
	jobID, _ := autoData["job_id"].(string)
	sessionID, _ := autoData["session_id"].(string)
	if jobID == "" {
		t.Fatal("expected job_id from investigate auto")
	}
	if sessionID == "" {
		t.Fatal("expected session_id from investigate auto")
	}
	if autoData["status"] != "running" {
		t.Fatalf("status = %v, want running", autoData["status"])
	}

	statusReq := makeRequest("status", map[string]any{"job_id": jobID})
	finalStatus := ""
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusResult, statusErr := srv.handleStatus(context.Background(), statusReq)
		if statusErr != nil {
			t.Fatalf("handleStatus: %v", statusErr)
		}
		statusData := parseResult(t, statusResult)
		finalStatus, _ = statusData["status"].(string)
		if finalStatus == "completed" || finalStatus == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if finalStatus != "completed" {
		t.Fatalf("auto investigation job did not complete successfully, final status=%q", finalStatus)
	}

	state := inv.GetInvestigation(sessionID)
	if state == nil {
		t.Fatalf("expected investigation state for session %q", sessionID)
	}
	if len(state.Findings) == 0 {
		t.Fatal("expected delegated completion to persist findings in investigation state")
	}

	infoReq := makeRequest("sessions", map[string]any{
		"action":     "info",
		"session_id": sessionID,
	})
	infoResult, err := srv.handleSessions(context.Background(), infoReq)
	if err != nil {
		t.Fatalf("handleSessions info: %v", err)
	}
	if infoResult.IsError {
		t.Fatalf("handleSessions info returned error: %+v", infoResult)
	}
	infoData := parseResult(t, infoResult)
	sessionData, ok := infoData["session"].(map[string]any)
	if !ok {
		t.Fatalf("expected session payload, got %T", infoData["session"])
	}
	metadata, ok := sessionData["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected session metadata map, got %T", sessionData["metadata"])
	}
	if metadata["source"] != "delegate" {
		t.Fatalf("session metadata source = %v, want delegate", metadata["source"])
	}
	if metadata["cli"] != "codex" {
		t.Fatalf("session metadata cli = %v, want codex", metadata["cli"])
	}
	delegateReport, _ := metadata["delegate_report"].(string)
	if delegateReport == "" {
		t.Fatal("expected delegate_report in session metadata")
	}
}

func TestHandleInvestigate_AutoCancelUsesSessionsInfrastructure(t *testing.T) {
	srv := testServer(t)
	blocked := make(chan struct{})
	srv.executor = &stubExecutor{run: func(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
		<-ctx.Done()
		close(blocked)
		return nil, ctx.Err()
	}}

	autoReq := makeRequest("investigate", map[string]any{
		"action": "auto",
		"topic":  "cancel path coverage",
	})
	autoResult, err := srv.handleInvestigate(context.Background(), autoReq)
	if err != nil {
		t.Fatalf("investigate auto: %v", err)
	}
	if autoResult.IsError {
		t.Fatalf("investigate auto returned error result: %+v", autoResult)
	}
	autoData := parseResult(t, autoResult)
	jobID, _ := autoData["job_id"].(string)
	if jobID == "" {
		t.Fatal("expected job_id from investigate auto")
	}

	cancelReq := makeRequest("sessions", map[string]any{
		"action": "cancel",
		"job_id": jobID,
	})
	cancelResult, err := srv.handleSessions(context.Background(), cancelReq)
	if err != nil {
		t.Fatalf("handleSessions cancel: %v", err)
	}
	if cancelResult.IsError {
		t.Fatalf("cancel returned error result: %+v", cancelResult)
	}
	cancelData := parseResult(t, cancelResult)
	if cancelData["status"] != "cancelled" {
		t.Fatalf("cancel status = %v, want cancelled", cancelData["status"])
	}

	statusReq := makeRequest("status", map[string]any{"job_id": jobID})
	finalStatus := ""
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		statusResult, statusErr := srv.handleStatus(context.Background(), statusReq)
		if statusErr != nil {
			t.Fatalf("handleStatus: %v", statusErr)
		}
		statusData := parseResult(t, statusResult)
		finalStatus, _ = statusData["status"].(string)
		if finalStatus == "failed" || finalStatus == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if finalStatus != "failed" && finalStatus != "completed" {
		t.Fatalf("expected terminal status after cancel, got %q", finalStatus)
	}
}

// --- CheckConcurrencyLimit ---

func TestCheckConcurrencyLimit_NoLimit(t *testing.T) {
	srv := testServer(t)
	// Default config has MaxConcurrentJobs=0 (no limit)
	if err := srv.checkConcurrencyLimit(); err != nil {
		t.Errorf("expected no error with no limit, got: %v", err)
	}
}

func TestCheckConcurrencyLimit_LimitReached(t *testing.T) {
	srv := testServer(t)
	srv.cfg.Server.MaxConcurrentJobs = 1

	// Create a running job to hit the limit
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, t.TempDir())
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 0)

	if err := srv.checkConcurrencyLimit(); err == nil {
		t.Error("expected error when concurrency limit reached")
	}
}

func TestCheckConcurrencyLimit_UnderLimit(t *testing.T) {
	srv := testServer(t)
	srv.cfg.Server.MaxConcurrentJobs = 5

	// No running jobs — should be fine
	if err := srv.checkConcurrencyLimit(); err != nil {
		t.Errorf("expected no error when under limit, got: %v", err)
	}
}

// --- Think: unknown pattern ---

func TestHandleThink_UnknownPattern(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("think", map[string]any{
		"pattern": "nonexistent_pattern_xyz",
	})

	result, err := srv.handleThink(context.Background(), req)
	if err != nil {
		t.Fatalf("handleThink: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for unknown think pattern")
	}
}

// --- InjectBootstrap with actual template ---

func TestInjectBootstrap_WithPromptEngine(t *testing.T) {
	srv := testServer(t)
	// promptEng is initialized but ConfigDir has no templates — falls back to original
	original := "test prompt"
	got := srv.injectBootstrap("coding", original)
	// Either returns original (no template) or prepended (template exists) — both valid
	if got == "" {
		t.Error("injectBootstrap should never return empty string")
	}
	if !strings.Contains(got, original) {
		t.Errorf("injectBootstrap result should contain original prompt, got: %q", got)
	}
}

// --- Legacy Prompt Handlers ---

func TestHandleBackgroundPrompt_NoArgs(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{}

	result, err := srv.handleBackgroundPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("handleBackgroundPrompt: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Messages) == 0 {
		t.Error("expected at least one message")
	}
}

func TestHandleBackgroundPrompt_WithTask(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Arguments: map[string]string{
				"task_description": "review the codebase for security issues",
			},
		},
	}

	result, err := srv.handleBackgroundPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("handleBackgroundPrompt: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestHandleBackgroundPrompt_CodingTask(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Arguments: map[string]string{
				"task_description": "implement a new feature for the auth system",
			},
		},
	}

	result, err := srv.handleBackgroundPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("handleBackgroundPrompt: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestHandleGuidePrompt(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{}

	result, err := srv.handleGuidePrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGuidePrompt: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Messages) == 0 {
		t.Error("expected at least one message")
	}
}

func TestHandleInvestigatePrompt_NoTopic(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{}

	result, err := srv.handleInvestigatePrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigatePrompt: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestHandleInvestigatePrompt_WithTopic(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Arguments: map[string]string{
				"topic": "crash on startup in auth module",
			},
		},
	}

	result, err := srv.handleInvestigatePrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInvestigatePrompt: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Messages) == 0 {
		t.Error("expected at least one message")
	}
}

func TestHandleWorkflowPrompt_NoGoal(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{}

	result, err := srv.handleWorkflowPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWorkflowPrompt: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestHandleWorkflowPrompt_WithGoal(t *testing.T) {
	srv := testServer(t)
	req := mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Arguments: map[string]string{
				"goal": "run tests, then deploy to staging",
			},
		},
	}

	result, err := srv.handleWorkflowPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWorkflowPrompt: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- HandleConsensus async path ---

func TestHandleConsensus_InsufficientCLIs_WithPromptParam(t *testing.T) {
	srv := testServer(t)
	// "prompt" is not the right param — consensus uses "topic"
	// This tests that using the wrong param name triggers the right error.
	req := makeRequest("consensus", map[string]any{
		"prompt": "is Go better than Rust?",
	})

	result, err := srv.handleConsensus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConsensus: %v", err)
	}

	// topic is required — this should fail with missing topic error
	if !result.IsError {
		t.Error("expected error for missing topic (using wrong param)")
	}
}

// --- HandleAgents: run with explicit CLI but no agents registered ---

func TestHandleAgents_Run_NoAgentsRegistered(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("agents", map[string]any{
		"action": "run",
		"prompt": "analyze this code for bugs",
	})

	result, err := srv.handleAgents(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgents run: %v", err)
	}

	// Either auto-selects a builtin agent or returns an error — both are valid
	// paths when no explicit agent name is provided.
	if result == nil {
		t.Fatal("result is nil")
	}
	// result.IsError may be true (no matching agent) or false (builtin auto-selected).
	// We only assert the result is non-nil and has content.
	if len(result.Content) == 0 {
		t.Error("expected result to have content")
	}
}

// --- HandleAgents: info for builtin agent ---

func TestHandleAgents_Info_Builtin(t *testing.T) {
	srv := testServer(t)

	// List agents first to find a valid builtin
	listReq := makeRequest("agents", map[string]any{"action": "list"})
	listResult, err := srv.handleAgents(context.Background(), listReq)
	if err != nil {
		t.Fatalf("handleAgents list: %v", err)
	}

	listData := parseResult(t, listResult)
	agents, ok := listData["agents"].([]any)
	if !ok || len(agents) == 0 {
		t.Skip("no agents registered to test info on")
	}

	// Get info on the first agent
	firstAgent, ok := agents[0].(map[string]any)
	if !ok {
		t.Skip("unexpected agent format")
	}
	agentName, _ := firstAgent["name"].(string)
	if agentName == "" {
		t.Skip("first agent has no name")
	}

	infoReq := makeRequest("agents", map[string]any{
		"action": "info",
		"agent":  agentName,
	})

	infoResult, err := srv.handleAgents(context.Background(), infoReq)
	if err != nil {
		t.Fatalf("handleAgents info: %v", err)
	}

	if infoResult.IsError {
		t.Errorf("info for known agent %q returned error", agentName)
	}
}

// --- HandleAgentRun: async path ---

func TestHandleAgentRun_Async_BuiltinAgent(t *testing.T) {
	srv := testServer(t)

	// Find a builtin agent first
	listReq := makeRequest("agents", map[string]any{"action": "list"})
	listResult, _ := srv.handleAgents(context.Background(), listReq)
	listData := parseResult(t, listResult)
	agents, ok := listData["agents"].([]any)
	if !ok || len(agents) == 0 {
		t.Skip("no agents registered")
	}
	firstAgent, _ := agents[0].(map[string]any)
	agentName, _ := firstAgent["name"].(string)
	if agentName == "" {
		t.Skip("no agent name available")
	}

	req := makeRequest("agent", map[string]any{
		"agent":  agentName,
		"prompt": "test prompt",
		"async":  true,
	})

	result, err := srv.handleAgentRun(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgentRun async: %v", err)
	}

	data := parseResult(t, result)
	if data["job_id"] == nil || data["job_id"] == "" {
		t.Error("expected job_id for async agent run")
	}
	if data["status"] != "running" {
		t.Errorf("status = %v, want running", data["status"])
	}

	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Fatal("job_id must be string")
	}
	if job := srv.jobs.Get(jobID); job == nil {
		t.Fatal("job should exist for async agent run")
	}

	statusReq := makeRequest("status", map[string]any{"job_id": jobID})
	var statusData map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusResult, statusErr := srv.handleStatus(context.Background(), statusReq)
		if statusErr != nil {
			t.Fatalf("handleStatus: %v", statusErr)
		}
		statusData = parseResult(t, statusResult)
		if progress, _ := statusData["progress"].(string); progress != "" {
			break
		}
		if st, _ := statusData["status"].(string); st == "failed" || st == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	progress, _ := statusData["progress"].(string)
	if progress == "" {
		t.Fatalf("expected non-empty progress for async agent run, got %v", statusData)
	}
	if statusData["job_id"] != jobID {
		t.Fatalf("status job_id = %v, want %s", statusData["job_id"], jobID)
	}
}

func TestHandleAgentRun_EnvOverrideBeatsAgentFrontmatter(t *testing.T) {
	t.Setenv("AIMUX_ROLE_CODING", "codex:gpt-5.3-codex-spark:high")

	srv := testServer(t)
	agent := &agents.Agent{
		Name:   "test-coding-agent",
		Role:   "coding",
		Model:  "gpt-5.4",
		Effort: "low",
		Meta: map[string]string{
			"cli": "codex",
		},
	}
	srv.agentReg.Register(agent)

	req := makeRequest("agent", map[string]any{
		"agent":  agent.Name,
		"prompt": "test prompt",
	})

	result, err := srv.handleAgentRun(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgentRun: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error result")
	}

	data := parseResult(t, result)
	content, _ := data["content"].(string)
	if content == "" {
		t.Fatal("missing content")
	}
	if !strings.Contains(content, "gpt-5.3-codex-spark") {
		t.Fatalf("content = %q, want env override model", content)
	}
	if !strings.Contains(content, "model_reasoning_effort=high") {
		t.Fatalf("content = %q, want env override effort", content)
	}
	if strings.Contains(content, "gpt-5.4") {
		t.Fatalf("content = %q, got frontmatter model", content)
	}
	if strings.Contains(content, "model_reasoning_effort=low") {
		t.Fatalf("content = %q, got frontmatter effort", content)
	}
}

func TestHandleAgentRun_FrontmatterBeatsRoleDefaultsWithoutEnv(t *testing.T) {
	srv := testServer(t)
	agent := &agents.Agent{
		Name:   "test-coding-agent-frontmatter",
		Role:   "coding",
		Model:  "gpt-5.4",
		Effort: "low",
		Meta: map[string]string{
			"cli": "codex",
		},
	}
	srv.agentReg.Register(agent)

	req := makeRequest("agent", map[string]any{
		"agent":  agent.Name,
		"prompt": "test prompt",
	})

	result, err := srv.handleAgentRun(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAgentRun: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error result")
	}

	data := parseResult(t, result)
	content, _ := data["content"].(string)
	if content == "" {
		t.Fatal("missing content")
	}
	if !strings.Contains(content, "gpt-5.4") {
		t.Fatalf("content = %q, want frontmatter model", content)
	}
	if !strings.Contains(content, "model_reasoning_effort=low") {
		t.Fatalf("content = %q, want frontmatter effort", content)
	}
	if strings.Contains(content, "gpt-5.3-codex") {
		t.Fatalf("content = %q, got role default model", content)
	}
	if strings.Contains(content, "model_reasoning_effort=medium") {
		t.Fatalf("content = %q, got role default effort", content)
	}
}

// --- Sessions: cancel nonexistent job ---

func TestHandleSessions_CancelNonexistentJob(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{
		"action": "cancel",
		"job_id": "nonexistent-job",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions cancel: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for cancelling nonexistent job")
	}
}

func TestHandleSessions_KillNonexistentSession(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("sessions", map[string]any{
		"action":     "kill",
		"session_id": "nonexistent-session",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions kill: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for killing nonexistent session")
	}
}
