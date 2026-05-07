package fallback

import (
	"testing"
	"time"
)

func TestDefaultFallbackConfig_Values(t *testing.T) {
	cfg := DefaultFallbackConfig()
	cfgp := &cfg

	if cfgp.MaxAttempts != 2 {
		t.Errorf("MaxAttempts = %d, want 2", cfgp.MaxAttempts)
	}
	if !cfgp.IsEnabled() {
		t.Errorf("FallbackEnabled should be true by default")
	}
	if cfgp.latencyBudget() != 30*time.Second {
		t.Errorf("LatencyBudget = %v, want 30s", cfgp.latencyBudget())
	}
	if cfgp.decayWindow() != time.Hour {
		t.Errorf("DecayWindow = %v, want 1h", cfgp.decayWindow())
	}
}

func TestDefaultScoreWeights_Sum(t *testing.T) {
	w := DefaultScoreWeights()
	sum := w.Capability + w.SuccessRate + w.Latency + w.Recency
	if abs64(sum-1.0) > 1e-9 {
		t.Errorf("ScoreWeights sum = %v, want 1.0", sum)
	}
}

func TestDefaultScoreWeights_Values(t *testing.T) {
	w := DefaultScoreWeights()
	if abs64(w.Capability-0.40) > 1e-9 {
		t.Errorf("Capability = %v, want 0.40", w.Capability)
	}
	if abs64(w.SuccessRate-0.30) > 1e-9 {
		t.Errorf("SuccessRate = %v, want 0.30", w.SuccessRate)
	}
	if abs64(w.Latency-0.20) > 1e-9 {
		t.Errorf("Latency = %v, want 0.20", w.Latency)
	}
	if abs64(w.Recency-0.10) > 1e-9 {
		t.Errorf("Recency = %v, want 0.10", w.Recency)
	}
}

func TestNormalizeWeights_AlreadyNormalized(t *testing.T) {
	w := DefaultScoreWeights()
	n := NormalizeWeights(w)
	// Should be unchanged since sum == 1.0
	if abs64(n.Capability-w.Capability) > 1e-9 {
		t.Errorf("normalized Capability = %v, want %v", n.Capability, w.Capability)
	}
}

func TestNormalizeWeights_NonUnit(t *testing.T) {
	w := ScoreWeights{Capability: 4, SuccessRate: 3, Latency: 2, Recency: 1}
	n := NormalizeWeights(w)
	sum := n.Capability + n.SuccessRate + n.Latency + n.Recency
	if abs64(sum-1.0) > 1e-9 {
		t.Errorf("normalized sum = %v, want 1.0", sum)
	}
	// Proportions should match
	if abs64(n.Capability-0.40) > 1e-9 {
		t.Errorf("normalized Capability = %v, want 0.40", n.Capability)
	}
}

func TestNormalizeWeights_ZeroSum_FallsBackToDefault(t *testing.T) {
	w := ScoreWeights{} // all zero
	n := NormalizeWeights(w)
	def := DefaultScoreWeights()
	if n != def {
		t.Errorf("zero-sum weights should fall back to defaults, got %+v", n)
	}
}

func TestFallbackConfig_IsEnabled_NilPtr(t *testing.T) {
	cfg := DefaultFallbackConfig()
	cfg.FallbackEnabled = nil
	if !cfg.IsEnabled() {
		t.Errorf("nil FallbackEnabled should default to true")
	}
}

func TestFallbackConfig_IsEnabled_ExplicitFalse(t *testing.T) {
	cfg := DefaultFallbackConfig()
	f := false
	cfg.FallbackEnabled = &f
	if (&cfg).IsEnabled() {
		t.Errorf("explicit false FallbackEnabled should return false")
	}
}

func TestFallbackConfig_maxAttempts_Zero_FallsBackToDefault(t *testing.T) {
	cfg := DefaultFallbackConfig()
	cfg.MaxAttempts = 0
	// maxAttempts() treats <=0 as "use default"
	if got := (&cfg).maxAttempts(); got != DefaultMaxAttempts {
		t.Errorf("maxAttempts() with 0 = %d, want %d (default)", got, DefaultMaxAttempts)
	}
}

func TestFallbackConfig_maxAttempts_Explicit(t *testing.T) {
	cfg := DefaultFallbackConfig()
	cfg.MaxAttempts = 5
	if got := (&cfg).maxAttempts(); got != 5 {
		t.Errorf("maxAttempts() = %d, want 5", got)
	}
}
