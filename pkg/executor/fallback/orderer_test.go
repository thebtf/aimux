package fallback

import (
	"context"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/executor/types"
)

// alwaysHealthy returns a HealthChecker that considers all CLIs healthy
// by injecting a lookPath that always succeeds.
func alwaysHealthyChecker(clis []string) *picker.HealthChecker {
	cfg := &picker.PickerConfig{HealthCacheTTL: time.Hour}
	return picker.NewHealthChecker(cfg, func(cli string) string { return cli }, clis,
		func(name string) (string, error) { return "/fake/" + name, nil })
}

// neverHealthy returns a HealthChecker that considers all CLIs unhealthy.
func neverHealthyChecker(clis []string) *picker.HealthChecker {
	cfg := &picker.PickerConfig{HealthCacheTTL: time.Hour}
	return picker.NewHealthChecker(cfg, func(cli string) string { return cli }, clis,
		func(name string) (string, error) { return "", &exec_fake_err{name} })
}

type exec_fake_err struct{ name string }

func (e *exec_fake_err) Error() string { return "not found: " + e.name }

func capScore() *picker.CapabilityScore {
	return picker.NewCapabilityScore(&picker.PickerConfig{})
}

func defaultOrdCfg() *FallbackConfig {
	c := DefaultFallbackConfig()
	return &c
}

// --- Cold start: capability dominates ---

func TestOrderer_ColdStart_CapabilityDominates(t *testing.T) {
	candidates := []string{"codex", "claude", "gemini"}
	health := alwaysHealthyChecker(candidates)
	score := capScore()
	cfg := defaultOrdCfg()
	store := NewInMemoryScoreStore()

	o := NewOrderer(score, health, cfg)
	ranked := o.Rank(context.Background(), candidates, "code", map[string]struct{}{}, store)

	// For "code" task: codex=95, claude=80, gemini=60 → codex first
	if len(ranked) != 3 {
		t.Fatalf("expected 3 ranked CLIs, got %d", len(ranked))
	}
	if ranked[0] != "codex" {
		t.Errorf("cold start: top rank = %q, want codex (highest code score)", ranked[0])
	}
	if ranked[len(ranked)-1] != "gemini" {
		t.Errorf("cold start: last rank = %q, want gemini (lowest code score)", ranked[len(ranked)-1])
	}
}

// --- Warm store: success rate influences ranking ---

func TestOrderer_WarmStore_SuccessRateInfluences(t *testing.T) {
	candidates := []string{"codex", "claude", "gemini"}
	health := alwaysHealthyChecker(candidates)
	score := capScore()
	cfg := defaultOrdCfg()
	store := NewInMemoryScoreStore()

	// Give gemini high success rate to boost it above codex on "research"
	for i := 0; i < 20; i++ {
		store.RecordSuccess("gemini", 100)
	}
	for i := 0; i < 10; i++ {
		store.RecordFailure("codex", types.CLIErrorCodeRateLimit)
	}

	o := NewOrderer(score, health, cfg)
	ranked := o.Rank(context.Background(), candidates, "research", map[string]struct{}{}, store)

	if len(ranked) != 3 {
		t.Fatalf("expected 3 CLIs, got %d", len(ranked))
	}
	// gemini has research score 90 + perfect success rate vs codex score 40 + 0 success rate
	// gemini should rank above codex
	geminiIdx := indexOf(ranked, "gemini")
	codexIdx := indexOf(ranked, "codex")
	if geminiIdx >= codexIdx {
		t.Errorf("warm store: gemini (%d) should rank above codex (%d) on research", geminiIdx, codexIdx)
	}
}

// --- Attempted CLIs are excluded ---

func TestOrderer_ExcludesAttempted(t *testing.T) {
	candidates := []string{"codex", "claude", "gemini"}
	health := alwaysHealthyChecker(candidates)
	store := NewInMemoryScoreStore()
	o := NewOrderer(capScore(), health, defaultOrdCfg())

	attempted := map[string]struct{}{"codex": {}}
	ranked := o.Rank(context.Background(), candidates, "code", attempted, store)

	for _, cli := range ranked {
		if cli == "codex" {
			t.Errorf("codex should be excluded (already attempted)")
		}
	}
	if len(ranked) != 2 {
		t.Errorf("expected 2 ranked CLIs (codex excluded), got %d: %v", len(ranked), ranked)
	}
}

// --- Unhealthy CLIs are excluded ---

func TestOrderer_ExcludesUnhealthy(t *testing.T) {
	candidates := []string{"codex", "claude", "gemini"}
	// Only claude and gemini are healthy
	health := picker.NewHealthChecker(
		&picker.PickerConfig{HealthCacheTTL: time.Hour},
		func(cli string) string { return cli },
		candidates,
		func(name string) (string, error) {
			if name == "codex" {
				return "", &exec_fake_err{name}
			}
			return "/fake/" + name, nil
		},
	)
	store := NewInMemoryScoreStore()
	o := NewOrderer(capScore(), health, defaultOrdCfg())

	ranked := o.Rank(context.Background(), candidates, "code", map[string]struct{}{}, store)
	for _, cli := range ranked {
		if cli == "codex" {
			t.Errorf("codex (unhealthy) should be excluded")
		}
	}
	if len(ranked) != 2 {
		t.Errorf("expected 2 CLIs (codex excluded), got %d: %v", len(ranked), ranked)
	}
}

// --- All excluded returns empty ---

func TestOrderer_AllExcluded_ReturnsEmpty(t *testing.T) {
	candidates := []string{"codex"}
	health := alwaysHealthyChecker(candidates)
	store := NewInMemoryScoreStore()
	o := NewOrderer(capScore(), health, defaultOrdCfg())

	// codex already attempted
	ranked := o.Rank(context.Background(), candidates, "code",
		map[string]struct{}{"codex": {}}, store)
	if len(ranked) != 0 {
		t.Errorf("all excluded: expected empty slice, got %v", ranked)
	}
}

// --- All unhealthy returns empty ---

func TestOrderer_AllUnhealthy_ReturnsEmpty(t *testing.T) {
	candidates := []string{"codex", "claude"}
	health := neverHealthyChecker(candidates)
	store := NewInMemoryScoreStore()
	o := NewOrderer(capScore(), health, defaultOrdCfg())

	ranked := o.Rank(context.Background(), candidates, "code", map[string]struct{}{}, store)
	if len(ranked) != 0 {
		t.Errorf("all unhealthy: expected empty slice, got %v", ranked)
	}
}

func indexOf(slice []string, val string) int {
	for i, s := range slice {
		if s == val {
			return i
		}
	}
	return -1
}
