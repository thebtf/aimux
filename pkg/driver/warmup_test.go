package driver

// Internal (whitebox) test file for warmup logic.
// Uses package driver (not driver_test) for access to unexported helpers:
// runWarmupWithExec, parseWarmupResponse, and registry fields.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/types"
)

// --- Stub executor ---

// warmupStubExecutor is a minimal types.Executor whose Run behaviour is
// controlled per-CLI via a map of handler functions.
type warmupStubExecutor struct {
	handlers map[string]func(types.SpawnArgs) (*types.Result, error)
}

func (s *warmupStubExecutor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	if h, ok := s.handlers[args.CLI]; ok {
		type resultPair struct {
			r *types.Result
			e error
		}
		ch := make(chan resultPair, 1)
		go func() {
			r, e := h(args)
			ch <- resultPair{r, e}
		}()
		select {
		case p := <-ch:
			return p.r, p.e
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Default: success with valid JSON.
	return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil
}

func (s *warmupStubExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, errors.New("warmupStubExecutor.Start: not implemented")
}

func (s *warmupStubExecutor) Name() string { return "warmup-stub" }

func (s *warmupStubExecutor) Available() bool { return true }

// --- Helper ---

// makeWarmupRegistry creates a Registry with the given CLI names available.
// Profiles have echo/cmd as binary (guaranteed present) and a minimal config.
func makeWarmupRegistry(t *testing.T, names ...string) *Registry {
	t.Helper()

	profiles := make(map[string]*config.CLIProfile, len(names))
	for _, name := range names {
		profiles[name] = &config.CLIProfile{
			Name:         name,
			Binary:       "echo",
			Command:      config.CommandConfig{Base: "echo"},
			ResolvedPath: "/fake/path/" + name, // simulate successful Probe()
			// WarmupTimeoutSeconds: 0 → use global default
		}
	}

	reg := NewRegistry(profiles)

	// Mark all CLIs as available (simulates post-Probe state).
	reg.mu.Lock()
	for _, name := range names {
		reg.available[name] = true
	}
	reg.mu.Unlock()

	return reg
}

func defaultCfg() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			WarmupEnabled:        true,
			WarmupTimeoutSeconds: 5,
		},
	}
}

// --- T034: TestRunWarmup_AllSucceed ---

// TestRunWarmup_AllSucceed verifies that all CLIs remain enabled when each
// probe returns {"ok":true}.
func TestRunWarmup_AllSucceed(t *testing.T) {
	reg := makeWarmupRegistry(t, "codex", "gemini")

	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex":  func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil },
			"gemini": func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil },
		},
	}

	if err := runWarmupWithExec(context.Background(), reg, defaultCfg(), exec, nil); err != nil {
		t.Fatalf("RunWarmup: %v", err)
	}

	enabled := reg.EnabledCLIs()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled CLIs, got %d: %v", len(enabled), enabled)
	}
}

// --- T034: TestRunWarmup_OneFails ---

// TestRunWarmup_OneFails verifies that a CLI returning non-JSON is excluded
// while other CLIs remain enabled.
func TestRunWarmup_OneFails(t *testing.T) {
	reg := makeWarmupRegistry(t, "codex", "gemini")

	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex":  func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil },
			"gemini": func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: "error: not found", ExitCode: 1}, nil },
		},
	}

	if err := runWarmupWithExec(context.Background(), reg, defaultCfg(), exec, nil); err != nil {
		t.Fatalf("RunWarmup: %v", err)
	}

	enabled := reg.EnabledCLIs()
	if len(enabled) != 1 {
		t.Errorf("expected 1 enabled CLI after one failure, got %d: %v", len(enabled), enabled)
	}
	if enabled[0] != "codex" {
		t.Errorf("expected codex to be enabled, got %q", enabled[0])
	}
}

// --- T034: TestRunWarmup_OneTimesOut ---

// TestRunWarmup_OneTimesOut verifies that a CLI whose probe exceeds the per-profile
// timeout is excluded from EnabledCLIs after warmup.
func TestRunWarmup_OneTimesOut(t *testing.T) {
	profiles := map[string]*config.CLIProfile{
		"codex": {
			Name:                 "codex",
			Binary:               "echo",
			Command:              config.CommandConfig{Base: "echo"},
			ResolvedPath:         "/fake/path/codex",
			WarmupTimeoutSeconds: 1, // 1-second probe timeout
		},
		"gemini": {
			Name:                 "gemini",
			Binary:               "echo",
			Command:              config.CommandConfig{Base: "echo"},
			ResolvedPath:         "/fake/path/gemini",
			WarmupTimeoutSeconds: 1,
		},
	}

	reg := NewRegistry(profiles)
	reg.mu.Lock()
	reg.available["codex"] = true
	reg.available["gemini"] = true
	reg.mu.Unlock()

	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex": func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil
			},
			// gemini blocks indefinitely — relies on context cancellation from the
			// per-profile timeout (1s). This avoids a fixed sleep that causes CI
			// false-failures under heavy load; the stub executor selects on ctx.Done().
			"gemini": func(_ types.SpawnArgs) (*types.Result, error) {
				select {} //nolint:staticcheck // intentionally blocks; stub respects ctx
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := runWarmupWithExec(ctx, reg, defaultCfg(), exec, nil); err != nil {
		t.Fatalf("RunWarmup: %v", err)
	}

	enabled := reg.EnabledCLIs()
	if len(enabled) != 1 {
		t.Errorf("expected 1 enabled CLI after timeout, got %d: %v", len(enabled), enabled)
	}
	if len(enabled) == 1 && enabled[0] != "codex" {
		t.Errorf("expected codex to be enabled, got %q", enabled[0])
	}
}

// --- T034: TestRunWarmup_OptOut ---

// TestRunWarmup_OptOut verifies that setting AIMUX_WARMUP=false causes RunWarmup
// to be a no-op — all binary-probed CLIs remain in their current state.
func TestRunWarmup_OptOut(t *testing.T) {
	t.Setenv("AIMUX_WARMUP", "false")

	reg := makeWarmupRegistry(t, "codex", "gemini")

	// Executor that always fails — if called, it would exclude both CLIs.
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex":  func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: "error", ExitCode: 1}, nil },
			"gemini": func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: "error", ExitCode: 1}, nil },
		},
	}

	if err := runWarmupWithExec(context.Background(), reg, defaultCfg(), exec, nil); err != nil {
		t.Fatalf("RunWarmup: %v", err)
	}

	// Both CLIs should remain available — warmup was skipped entirely.
	enabled := reg.EnabledCLIs()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled CLIs (warmup no-op), got %d: %v", len(enabled), enabled)
	}
}

// --- T034: TestRunWarmup_ConfigDisabled ---

// TestRunWarmup_ConfigDisabled verifies that warmup_enabled: false in config
// causes RunWarmup to be a no-op — CLIs remain in their current state.
func TestRunWarmup_ConfigDisabled(t *testing.T) {
	reg := makeWarmupRegistry(t, "codex", "gemini")

	// Executor that always fails — if called, it would exclude both CLIs.
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex":  func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: "error", ExitCode: 1}, nil },
			"gemini": func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: "error", ExitCode: 1}, nil },
		},
	}

	cfg := defaultCfg()
	cfg.Server.WarmupEnabled = false // explicit config-level disable

	if err := runWarmupWithExec(context.Background(), reg, cfg, exec, nil); err != nil {
		t.Fatalf("RunWarmup: %v", err)
	}

	// Both CLIs should remain available — warmup was skipped entirely.
	enabled := reg.EnabledCLIs()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled CLIs (warmup no-op), got %d: %v", len(enabled), enabled)
	}
}

// --- T035: TestWarmup_JSONParse ---

// TestWarmup_JSONParse is a table-driven test covering all significant
// parseWarmupResponse cases.
func TestWarmup_JSONParse(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "invalid JSON", content: "not json at all", want: false},
		{name: "ok true", content: `{"ok":true}`, want: true},
		{name: "ok false", content: `{"ok":false}`, want: false},
		{name: "empty string", content: "", want: false},
		{name: "valid JSON missing ok field", content: `{"status":"ok"}`, want: false},
		{name: "preamble before JSON", content: `preamble {"ok":true} suffix`, want: true},
		{name: "JSON with extra fields", content: `{"ok":true,"extra":42}`, want: true},
		{name: "ok false with extra", content: `{"ok":false,"msg":"rate limited"}`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWarmupResponse(tt.content)
			if got != tt.want {
				t.Errorf("parseWarmupResponse(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

// --- T036: TestDaemon_WarmupExcludesMisconfiguredCLI ---

// TestDaemon_WarmupExcludesMisconfiguredCLI is an integration test that registers
// a CLI with a bogus binary path. After warmup (with a mock executor that returns
// an error for that CLI), the CLI must be absent from EnabledCLIs, while other
// valid CLIs remain enabled. No panic or fatal error occurs.
func TestDaemon_WarmupExcludesMisconfiguredCLI(t *testing.T) {
	// "bogus-cli" has a non-existent binary but is marked available by Probe
	// (simulating a misconfigured or stale CLI entry).
	profiles := map[string]*config.CLIProfile{
		"bogus-cli": {
			Name:         "bogus-cli",
			Binary:       "/nonexistent/path/to/bogus-cli-xyz-99999",
			Command:      config.CommandConfig{Base: "/nonexistent/path/to/bogus-cli-xyz-99999"},
			ResolvedPath: "/nonexistent/path/to/bogus-cli-xyz-99999", // simulates Probe match on stale path
		},
		"valid-cli": {
			Name:         "valid-cli",
			Binary:       "echo",
			Command:      config.CommandConfig{Base: "echo"},
			ResolvedPath: "/fake/path/valid-cli",
		},
	}

	reg := NewRegistry(profiles)

	// Manually mark both as available (simulates post-Probe state where the
	// binary-based probe passed but runtime fails — e.g., auth env missing).
	reg.mu.Lock()
	reg.available["bogus-cli"] = true
	reg.available["valid-cli"] = true
	reg.mu.Unlock()

	// Mock executor: valid-cli succeeds, bogus-cli fails with a process error.
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"bogus-cli": func(_ types.SpawnArgs) (*types.Result, error) {
				return nil, errors.New("exec: no such file or directory: /nonexistent/path/to/bogus-cli-xyz-99999")
			},
			"valid-cli": func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil
			},
		},
	}

	cfg := defaultCfg()

	// Must not panic.
	if err := runWarmupWithExec(context.Background(), reg, cfg, exec, nil); err != nil {
		t.Fatalf("RunWarmup should not return error (only excludes CLI): %v", err)
	}

	enabled := reg.EnabledCLIs()

	// valid-cli must remain enabled.
	foundValid := false
	for _, e := range enabled {
		if e == "valid-cli" {
			foundValid = true
		}
		if e == "bogus-cli" {
			t.Errorf("bogus-cli should have been excluded from EnabledCLIs after warmup failure")
		}
	}
	if !foundValid {
		t.Errorf("valid-cli should remain enabled after warmup, got: %v", enabled)
	}
}

// TestRunWarmup_ReEnablesPassingCLI verifies that a CLI marked unavailable by
// a prior warmup pass is restored to available when it passes a subsequent probe.
// Regression guard for PR #97 (findings #3): `refresh-warmup` must be able to
// bring a transiently-failed CLI back online.
func TestRunWarmup_ReEnablesPassingCLI(t *testing.T) {
	reg := makeWarmupRegistry(t, "codex", "gemini")

	// Simulate a prior warmup that marked gemini unavailable.
	reg.SetAvailable("gemini", false)
	if got := reg.EnabledCLIs(); len(got) != 1 || got[0] != "codex" {
		t.Fatalf("setup: expected only codex enabled before refresh, got %v", got)
	}

	// Second warmup: both CLIs now pass the probe.
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex":  func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil },
			"gemini": func(_ types.SpawnArgs) (*types.Result, error) { return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil },
		},
	}

	if err := runWarmupWithExec(context.Background(), reg, defaultCfg(), exec, nil); err != nil {
		t.Fatalf("RunWarmup: %v", err)
	}

	enabled := reg.EnabledCLIs()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled CLIs after re-warmup (gemini re-enabled), got %d: %v", len(enabled), enabled)
	}
}
