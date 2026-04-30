package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
)

func TestLoad(t *testing.T) {
	// Use the real config directory from the project
	cfg, err := config.Load(findConfigDir(t))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify server defaults
	if cfg.Server.LogLevel != "info" {
		t.Errorf("expected log_level=info, got %s", cfg.Server.LogLevel)
	}
	if cfg.Server.MaxConcurrentJobs != 10 {
		t.Errorf("expected max_concurrent_jobs=10, got %d", cfg.Server.MaxConcurrentJobs)
	}
	if cfg.Server.DefaultTimeoutSeconds != 300 {
		t.Errorf("expected default_timeout_seconds=300, got %d", cfg.Server.DefaultTimeoutSeconds)
	}
	if cfg.Server.StreamingGraceSeconds != 60 {
		t.Errorf("expected streaming_grace_seconds=60, got %d", cfg.Server.StreamingGraceSeconds)
	}
	if cfg.Server.StreamingSoftWarningSeconds != 120 {
		t.Errorf("expected streaming_soft_warning_seconds=120, got %d", cfg.Server.StreamingSoftWarningSeconds)
	}
	if cfg.Server.StreamingHardStallSeconds != 600 {
		t.Errorf("expected streaming_hard_stall_seconds=600, got %d", cfg.Server.StreamingHardStallSeconds)
	}
	if cfg.Server.StreamingAutoCancelSeconds != 900 {
		t.Errorf("expected streaming_auto_cancel_seconds=900, got %d", cfg.Server.StreamingAutoCancelSeconds)
	}

	// Verify audit config
	if cfg.Server.Audit.ScannerRole != "codereview" {
		t.Errorf("expected audit.scanner_role=codereview, got %s", cfg.Server.Audit.ScannerRole)
	}
	if cfg.Server.Audit.ParallelScanners != 3 {
		t.Errorf("expected audit.parallel_scanners=3, got %d", cfg.Server.Audit.ParallelScanners)
	}

	// Verify pair config
	if cfg.Server.Pair.MaxRounds != 3 {
		t.Errorf("expected pair.max_rounds=3, got %d", cfg.Server.Pair.MaxRounds)
	}

	// Verify roles loaded
	if len(cfg.Roles) == 0 {
		t.Fatal("expected roles to be loaded")
	}
	codingPref, ok := cfg.Roles["coding"]
	if !ok {
		t.Fatal("expected 'coding' role to exist")
	}
	if codingPref.CLI != "codex" {
		t.Errorf("expected coding.cli=codex, got %s", codingPref.CLI)
	}

	// Verify circuit breaker defaults
	if cfg.CircuitBreaker.FailureThreshold != 3 {
		t.Errorf("expected failure_threshold=3, got %d", cfg.CircuitBreaker.FailureThreshold)
	}
}

func TestLoad_CLIProfiles(t *testing.T) {
	cfg, err := config.Load(findConfigDir(t))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	expectedCLIs := []string{"codex", "gemini", "claude", "qwen", "aider", "droid", "opencode"}
	for _, cli := range expectedCLIs {
		profile, ok := cfg.CLIProfiles[cli]
		if !ok {
			t.Errorf("expected CLI profile %q to be loaded", cli)
			continue
		}
		if profile.Binary == "" {
			t.Errorf("CLI %q has empty binary", cli)
		}
		if profile.TimeoutSeconds == 0 {
			t.Errorf("CLI %q has zero timeout", cli)
		}
	}
}

func TestLoad_AppliesStreamingDefaultsWhenOmitted(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "default.yaml")
	cfgYAML := []byte(`
server:
  log_level: info
`)
	if err := os.WriteFile(cfgPath, cfgYAML, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cliDir := filepath.Join(tmpDir, "cli.d")
	if err := os.Mkdir(cliDir, 0o755); err != nil {
		t.Fatalf("create cli.d: %v", err)
	}

	cfg, err := config.Load(tmpDir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.StreamingGraceSeconds != 60 {
		t.Errorf("expected streaming_grace_seconds=60, got %d", cfg.Server.StreamingGraceSeconds)
	}
	if cfg.Server.StreamingSoftWarningSeconds != 120 {
		t.Errorf("expected streaming_soft_warning_seconds=120, got %d", cfg.Server.StreamingSoftWarningSeconds)
	}
	if cfg.Server.StreamingHardStallSeconds != 600 {
		t.Errorf("expected streaming_hard_stall_seconds=600, got %d", cfg.Server.StreamingHardStallSeconds)
	}
	if cfg.Server.StreamingAutoCancelSeconds != 900 {
		t.Errorf("expected streaming_auto_cancel_seconds=900, got %d", cfg.Server.StreamingAutoCancelSeconds)
	}
}

func TestLoad_MissingConfig(t *testing.T) {
	_, err := config.Load("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

// TestLoad_ClaudeProfileT11Fields verifies AIMUX-16 CR-001: the claude profile
// declares cooldown_seconds, model_fallback, and completion_pattern (T11
// transport contract). These three fields must load into the parsed profile
// so MarkCooledDown / RunWithModelFallback / completion-pattern matching can
// fire for claude (per spec FR-1 acceptance signals).
func TestLoad_ClaudeProfileT11Fields(t *testing.T) {
	cfg, err := config.Load(findConfigDir(t))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	profile, ok := cfg.CLIProfiles["claude"]
	if !ok {
		t.Fatal("expected claude profile to be loaded")
	}

	if profile.CooldownSeconds != 3600 {
		t.Errorf("claude cooldown_seconds: want 3600 (1h Anthropic quota window), got %d",
			profile.CooldownSeconds)
	}

	wantModels := []string{"claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"}
	if len(profile.ModelFallback) != len(wantModels) {
		t.Fatalf("claude model_fallback: want %d entries, got %d (%v)",
			len(wantModels), len(profile.ModelFallback), profile.ModelFallback)
	}
	for i, want := range wantModels {
		if profile.ModelFallback[i] != want {
			t.Errorf("claude model_fallback[%d]: want %q, got %q",
				i, want, profile.ModelFallback[i])
		}
	}

	if profile.CompletionPattern != "^---END---$" {
		t.Errorf("claude completion_pattern: want %q (anchored canonical sentinel), got %q",
			"^---END---$", profile.CompletionPattern)
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input string
		want  string
	}{
		{"~/logs/aimux.log", filepath.Join(home, "logs/aimux.log")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		got := config.ExpandPath(tt.input)
		if got != tt.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func findConfigDir(t *testing.T) string {
	t.Helper()

	// Walk up to find config/ directory containing default.yaml
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, "config")
		yamlPath := filepath.Join(candidate, "default.yaml")
		if _, err := os.Stat(yamlPath); err == nil {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find config/default.yaml — walked 10 levels up")
	return ""
}
