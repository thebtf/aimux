package executor_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor"
)

// TestCooldown_ClaudeProfileDuration_Integration — AIMUX-16 CR-001 FR-1:
// claude profile's cooldown_seconds (3600) plumbs through MarkCooledDown,
// the (claude, model) pair surfaces in List() with the correct ExpiresAt
// window, and untouched fallback models stay available.
func TestCooldown_ClaudeProfileDuration_Integration(t *testing.T) {
	cfg, err := config.Load(findRepoConfigDir(t))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	claude := cfg.CLIProfiles["claude"]
	if claude == nil || claude.CooldownSeconds <= 0 || len(claude.ModelFallback) == 0 {
		t.Fatalf("claude profile prerequisites unmet: %+v", claude)
	}

	tracker := executor.NewModelCooldownTracker()
	model := claude.ModelFallback[0]
	duration := time.Duration(claude.CooldownSeconds) * time.Second
	tracker.MarkCooledDown("claude", model, duration, "rate_limit_error: quota exceeded")

	if tracker.IsAvailable("claude", model) {
		t.Errorf("claude:%s must be on cooldown immediately after MarkCooledDown", model)
	}
	// Tolerance window for clock-drift / scheduling jitter between MarkCooledDown
	// and time.Until(ExpiresAt). 100s is generous but well below any realistic
	// cooldown duration so spurious passes are not a concern.
	const tolerance = 100 * time.Second
	var found bool
	for _, e := range tracker.List() {
		if e.CLI == "claude" && e.Model == model {
			found = true
			remaining := time.Until(e.ExpiresAt)
			if remaining < duration-tolerance || remaining > duration+tolerance {
				t.Errorf("claude:%s remaining=%s, want ~%s", model, remaining, duration)
			}
		}
	}
	if !found {
		t.Errorf("claude:%s missing from List() snapshot", model)
	}
	for _, m := range claude.ModelFallback[1:] {
		if !tracker.IsAvailable("claude", m) {
			t.Errorf("claude:%s should remain available", m)
		}
	}
}

// findRepoConfigDir walks up from cwd to locate config/default.yaml.
func findRepoConfigDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "config", "default.yaml")); err == nil {
			return filepath.Join(dir, "config")
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("config/default.yaml not found")
	return ""
}
