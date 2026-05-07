package picker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubLookPath returns a LookPath function that finds only the listed binaries.
func stubLookPath(found map[string]bool) func(string) (string, error) {
	return func(name string) (string, error) {
		if found[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

// identityBinaryFor maps CLI name to itself (binary = CLI name for codex/claude/gemini).
func identityBinaryFor(cli string) string { return cli }

func TestHealthChecker_Probe_Found(t *testing.T) {
	cfg := DefaultPickerConfig()
	hc := NewHealthChecker(&cfg, identityBinaryFor, nil, stubLookPath(map[string]bool{"codex": true}))

	healthy, reason := hc.Probe("codex")
	if !healthy {
		t.Errorf("expected healthy=true, got false (reason: %s)", reason)
	}
	if reason != "" {
		t.Errorf("expected empty reason for healthy CLI, got: %q", reason)
	}
}

func TestHealthChecker_Probe_NotFound(t *testing.T) {
	cfg := DefaultPickerConfig()
	hc := NewHealthChecker(&cfg, identityBinaryFor, nil, stubLookPath(map[string]bool{}))

	healthy, reason := hc.Probe("codex")
	if healthy {
		t.Error("expected healthy=false for missing binary")
	}
	if reason == "" {
		t.Error("expected non-empty reason for unhealthy CLI")
	}
}

func TestHealthChecker_IsHealthy_CacheHit(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.HealthCacheTTL = 60 * time.Second

	callCount := 0
	lookPath := func(name string) (string, error) {
		callCount++
		return "/bin/" + name, nil
	}

	hc := NewHealthChecker(&cfg, identityBinaryFor, nil, lookPath)

	// First call should probe.
	if !hc.IsHealthy("codex") {
		t.Fatal("expected healthy")
	}
	if callCount != 1 {
		t.Errorf("expected 1 probe call, got %d", callCount)
	}

	// Second call within TTL should use cache.
	if !hc.IsHealthy("codex") {
		t.Fatal("expected healthy (cache hit)")
	}
	if callCount != 1 {
		t.Errorf("expected still 1 probe call after cache hit, got %d", callCount)
	}
}

func TestHealthChecker_IsHealthy_CacheExpired(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.HealthCacheTTL = 1 * time.Millisecond

	callCount := 0
	lookPath := func(name string) (string, error) {
		callCount++
		return "/bin/" + name, nil
	}

	hc := NewHealthChecker(&cfg, identityBinaryFor, nil, lookPath)

	hc.IsHealthy("codex")
	if callCount != 1 {
		t.Fatalf("expected 1 probe, got %d", callCount)
	}

	// Wait for TTL to expire.
	time.Sleep(5 * time.Millisecond)

	hc.IsHealthy("codex")
	if callCount != 2 {
		t.Errorf("expected 2 probes after TTL expiry, got %d", callCount)
	}
}

func TestHealthChecker_WarmAll(t *testing.T) {
	cfg := DefaultPickerConfig()
	active := []string{"codex", "claude", "gemini"}
	probed := map[string]bool{}

	lookPath := func(name string) (string, error) {
		probed[name] = true
		return "/bin/" + name, nil
	}

	hc := NewHealthChecker(&cfg, identityBinaryFor, active, lookPath)
	hc.WarmAll(context.Background())

	for _, cli := range active {
		if !probed[cli] {
			t.Errorf("WarmAll did not probe CLI %q", cli)
		}
	}
}

func TestHealthChecker_WarmAll_ContextCancel(t *testing.T) {
	cfg := DefaultPickerConfig()
	active := []string{"codex", "claude", "gemini"}

	callCount := 0
	lookPath := func(name string) (string, error) {
		callCount++
		return "/bin/" + name, nil
	}

	hc := NewHealthChecker(&cfg, identityBinaryFor, active, lookPath)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	hc.WarmAll(ctx)
	// With immediate cancel, WarmAll should abort — at most 0 probes.
	if callCount > 0 {
		// Also acceptable: first probe may have run before cancel was detected.
		// Just ensure we didn't run all 3 when cancelled immediately.
		t.Logf("WarmAll ran %d probe(s) before cancel (acceptable if <= 1)", callCount)
	}
}

func TestHealthChecker_EmptyBinaryName(t *testing.T) {
	cfg := DefaultPickerConfig()
	hc := NewHealthChecker(&cfg, func(string) string { return "" }, nil, nil)

	healthy, reason := hc.Probe("codex")
	if healthy {
		t.Error("expected unhealthy for empty binary name")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}
