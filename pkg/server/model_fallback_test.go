package server

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/types"
)

// testServerWithFallback creates a server whose "codex" profile has a two-model
// fallback chain: ["model-a", "model-b"]. The cooldown tracker is freshly initialised
// so each test starts with a clean slate.
func testServerWithFallback(t *testing.T) *Server {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{
			LogLevel:              "error",
			LogFile:               t.TempDir() + "/test.log",
			DefaultTimeoutSeconds: 10,
		},
		Roles: map[string]types.RolePreference{
			"default": {CLI: "codex"},
		},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 3,
			CooldownSeconds:  5,
			HalfOpenMaxCalls: 1,
		},
		CLIProfiles: map[string]*config.CLIProfile{
			"codex": {
				Name:            "codex",
				Binary:          "echo",
				DisplayName:     "Test CLI",
				Command:         config.CommandConfig{Base: "echo"},
				PromptFlag:      "-p",
				ModelFlag:       "-m",
				TimeoutSeconds:  10,
				ModelFallback:   []string{"model-a", "model-b"},
				CooldownSeconds: 1, // short cooldown for tests
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

	srv := New(cfg, log, reg, router)
	// Replace cooldown tracker to guarantee test isolation.
	srv.cooldownTracker = executor.NewModelCooldownTracker()
	return srv
}

// waitJobDone polls until the job reaches a terminal state or the deadline is exceeded.
func waitJobDone(t *testing.T, srv *Server, jobID string, deadline time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("job %s did not complete within %v", jobID, deadline)
		case <-time.After(10 * time.Millisecond):
			j := srv.jobs.GetSnapshot(jobID)
			if j != nil && (j.Status == types.JobStatusCompleted || j.Status == types.JobStatusFailed) {
				return
			}
		}
	}
}

// --- T012: TestModelFallback_QuotaThenSuccess ---

// TestModelFallback_QuotaThenSuccess verifies that when model-a returns a quota
// error the fallback chain advances to model-b and the job completes successfully.
func TestModelFallback_QuotaThenSuccess(t *testing.T) {
	srv := testServerWithFallback(t)

	calls := make(map[string]int)
	srv.executor = &stubExecutor{run: func(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
		// Detect which model is being used from the args.
		model := modelFromArgs(args.Args)
		calls[model]++
		switch model {
		case "model-a":
			// Quota error: non-zero exit + quota pattern in content.
			return &types.Result{Content: "rate limit exceeded", ExitCode: 1}, nil
		default:
			return &types.Result{Content: "success from " + model, ExitCode: 0}, nil
		}
	}}

	// Run synchronously via runWithModelFallback directly.
	profile := srv.cfg.CLIProfiles["codex"]
	baseArgs := types.SpawnArgs{
		CLI:     "codex",
		Command: "echo",
		Args:    []string{"-p", "hello"},
	}

	result, err := srv.runWithModelFallback(context.Background(), srv.executor, profile, baseArgs)
	if err != nil {
		t.Fatalf("runWithModelFallback: unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Content != "success from model-b" {
		t.Errorf("content = %q, want %q", result.Content, "success from model-b")
	}
	if calls["model-a"] != 1 {
		t.Errorf("model-a call count = %d, want 1", calls["model-a"])
	}
	if calls["model-b"] != 1 {
		t.Errorf("model-b call count = %d, want 1", calls["model-b"])
	}
	// model-a must be on cooldown now.
	if srv.cooldownTracker.IsAvailable("codex", "model-a") {
		t.Error("model-a should be on cooldown after quota error")
	}
}

// --- T012: TestModelFallback_AllModelsQuota_FallsToNextCLI ---

// TestModelFallback_AllModelsQuota_FallsToNextCLI verifies that when all models
// in the fallback chain hit quota, executeJob surfaces an error (and would
// proceed to CLI fallback if a second CLI were configured).
func TestModelFallback_AllModelsQuota_FallsToNextCLI(t *testing.T) {
	srv := testServerWithFallback(t)

	srv.executor = &stubExecutor{run: func(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
		return &types.Result{Content: "quota exceeded", ExitCode: 1}, nil
	}}

	profile := srv.cfg.CLIProfiles["codex"]
	baseArgs := types.SpawnArgs{
		CLI:     "codex",
		Command: "echo",
		Args:    []string{"-p", "hello"},
	}

	_, err := srv.runWithModelFallback(context.Background(), srv.executor, profile, baseArgs)
	if err == nil {
		t.Fatal("expected error when all models are quota-limited")
	}

	// Both models should now be on cooldown.
	if srv.cooldownTracker.IsAvailable("codex", "model-a") {
		t.Error("model-a should be on cooldown")
	}
	if srv.cooldownTracker.IsAvailable("codex", "model-b") {
		t.Error("model-b should be on cooldown")
	}
}

// --- T012: TestModelFallback_TransientRetry_NoCooldown ---

// TestModelFallback_TransientRetry_NoCooldown verifies that a transient error
// causes a single retry on the same model without marking it as cooled down.
func TestModelFallback_TransientRetry_NoCooldown(t *testing.T) {
	srv := testServerWithFallback(t)

	var callCount atomic.Int32
	srv.executor = &stubExecutor{run: func(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
		n := callCount.Add(1)
		if n == 1 {
			// First call: transient error.
			return &types.Result{Content: "connection refused", ExitCode: 1}, nil
		}
		// Second call (retry): success.
		return &types.Result{Content: "ok", ExitCode: 0}, nil
	}}

	profile := srv.cfg.CLIProfiles["codex"]
	baseArgs := types.SpawnArgs{
		CLI:     "codex",
		Command: "echo",
		Args:    []string{"-p", "hello"},
	}

	result, err := srv.runWithModelFallback(context.Background(), srv.executor, profile, baseArgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Content != "ok" {
		t.Errorf("result content = %q, want %q", result.Content, "ok")
	}
	if callCount.Load() != 2 {
		t.Errorf("executor called %d times, want 2 (1 transient + 1 retry)", callCount.Load())
	}
	// Transient errors must NOT trigger cooldown.
	if !srv.cooldownTracker.IsAvailable("codex", "model-a") {
		t.Error("model-a should NOT be on cooldown after transient error")
	}
}

// --- T012: TestModelFallback_CooldownPreventsReuse ---

// TestModelFallback_CooldownPreventsReuse verifies that a model already on
// cooldown is skipped on the next call and the next available model is used.
func TestModelFallback_CooldownPreventsReuse(t *testing.T) {
	srv := testServerWithFallback(t)

	// Manually place model-a on cooldown.
	srv.cooldownTracker.MarkCooledDown("codex", "model-a", 10*time.Second)

	usedModels := make([]string, 0)
	srv.executor = &stubExecutor{run: func(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
		model := modelFromArgs(args.Args)
		usedModels = append(usedModels, model)
		return &types.Result{Content: "ok", ExitCode: 0}, nil
	}}

	profile := srv.cfg.CLIProfiles["codex"]
	baseArgs := types.SpawnArgs{
		CLI:     "codex",
		Command: "echo",
		Args:    []string{"-p", "hello"},
	}

	result, err := srv.runWithModelFallback(context.Background(), srv.executor, profile, baseArgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(usedModels) != 1 || usedModels[0] != "model-b" {
		t.Errorf("used models = %v, want [model-b] (model-a was on cooldown)", usedModels)
	}
}

// --- T012: TestModelFallback_NoFallbackConfig_ExistingBehavior ---

// TestModelFallback_NoFallbackConfig_ExistingBehavior verifies that a profile
// with no ModelFallback goes through the direct executor.Run path unchanged.
func TestModelFallback_NoFallbackConfig_ExistingBehavior(t *testing.T) {
	srv := testServer(t) // base testServer has no ModelFallback

	var callCount atomic.Int32
	srv.executor = &stubExecutor{run: func(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
		callCount.Add(1)
		return &types.Result{Content: "direct", ExitCode: 0}, nil
	}}

	// Submit an async exec and wait for it.
	req := makeRequest("exec", map[string]any{
		"prompt": "test",
		"cli":    "codex",
		"async":  true,
	})
	result, err := srv.handleExec(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExec: %v", err)
	}

	data := parseResult(t, result)
	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Fatal("missing job_id")
	}

	waitJobDone(t, srv, jobID, 5*time.Second)

	j := srv.jobs.GetSnapshot(jobID)
	if j == nil {
		t.Fatal("job not found")
	}
	if j.Status != types.JobStatusCompleted {
		t.Errorf("job status = %v, want completed; error = %v", j.Status, j.Error)
	}
	// Exactly one executor call — no model-fallback iteration.
	if callCount.Load() != 1 {
		t.Errorf("executor call count = %d, want 1 (no fallback chain)", callCount.Load())
	}
}

// --- T013: TestModelFallback_ExecHandler_RateLimitTriggersFallback ---

// TestModelFallback_ExecHandler_RateLimitTriggersFallback is an end-to-end handler test.
// It exercises handleExec → executeJob → runWithModelFallback with a rate limit on the
// primary model, verifying the fallback model produces the successful result.
func TestModelFallback_ExecHandler_RateLimitTriggersFallback(t *testing.T) {
	srv := testServer(t)

	// Configure codex profile with model fallback chain.
	profile, err := srv.registry.Get("codex")
	if err != nil {
		t.Skipf("codex profile not available: %v", err)
	}
	profile.ModelFallback = []string{"spark", "default"}
	profile.CooldownSeconds = 60
	profile.ModelFlag = "-m"

	var mu sync.Mutex
	var calls []string
	srv.executor = &stubExecutor{run: func(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
		model := modelFromArgs(args.Args)
		mu.Lock()
		calls = append(calls, model)
		mu.Unlock()
		if model == "spark" {
			return &types.Result{Content: "You've hit your usage limit", ExitCode: 1}, nil
		}
		return &types.Result{Content: "success from default", ExitCode: 0}, nil
	}}

	req := makeRequest("exec", map[string]any{
		"prompt": "test fallback",
		"cli":    "codex",
		"async":  true,
	})
	result, execErr := srv.handleExec(context.Background(), req)
	if execErr != nil {
		t.Fatalf("handleExec: %v", execErr)
	}
	data := parseResult(t, result)
	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Fatal("missing job_id")
	}

	waitJobDone(t, srv, jobID, 5*time.Second)

	j := srv.jobs.GetSnapshot(jobID)
	if j == nil {
		t.Fatal("job not found")
	}
	if j.Status != types.JobStatusCompleted {
		t.Errorf("job status = %v, want completed; content = %s; error = %v", j.Status, j.Content, j.Error)
	}
	if !strings.Contains(j.Content, "success from default") {
		t.Errorf("content = %q, want fallback model output", j.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 2 {
		t.Fatalf("expected 2+ calls (spark → default), got %d: %v", len(calls), calls)
	}
	if calls[0] != "spark" {
		t.Errorf("first call model = %q, want spark", calls[0])
	}
	// Verify spark is on cooldown after the rate limit.
	if srv.cooldownTracker.IsAvailable("codex", "spark") {
		t.Error("spark should be on cooldown after quota error")
	}
}

// --- helpers ---

// modelFromArgs extracts the value following "-m" in an args slice.
// Returns an empty string when no model flag is present.
func modelFromArgs(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-m" {
			return args[i+1]
		}
	}
	return ""
}
