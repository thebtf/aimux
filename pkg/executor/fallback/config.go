// Package fallback provides the runtime CLI fallback re-rank engine (AIMUX-4).
//
// When a picker-selected CLI fails at dispatch with an eligible error (rate-limit,
// auth expiry, timeout, capability mismatch), the engine re-ranks remaining candidates
// by composite score and re-attempts dispatch up to max_attempts times.
//
// Architecture: architecture.md §3 component map.
// Spec: spec.md FR-1..FR-12, NFR-1..NFR-4.
// All 7 ADRs from architecture.md are honored.
package fallback

import "time"

// DefaultMaxAttempts is the default cap on fallback re-attempts (ADR-007).
// With 3 active CLIs (codex, claude, gemini), this allows all 3 to be tried.
const DefaultMaxAttempts = 2

// DefaultLatencyBudget is the p50 latency beyond which latency_score = 0.
const DefaultLatencyBudget = 30 * time.Second

// DefaultDecayWindow is the exponential recency decay window.
// last_success more than one window ago yields recency_weight → 0.
const DefaultDecayWindow = 1 * time.Hour

// ScoreWeights defines the four weight terms in the composite score formula (ADR-001).
// All weights must be in [0.0, 1.0]; they are normalized at config load if they do not
// sum to 1.0 (see NormalizeWeights).
type ScoreWeights struct {
	Capability   float64 `yaml:"capability"`
	SuccessRate  float64 `yaml:"success_rate"`
	Latency      float64 `yaml:"latency"`
	Recency      float64 `yaml:"recency"`
}

// DefaultScoreWeights returns the spec-mandated default weights (ADR-001).
// Biased toward static capability in v1 (cold-start data regime).
func DefaultScoreWeights() ScoreWeights {
	return ScoreWeights{
		Capability:  0.40,
		SuccessRate: 0.30,
		Latency:     0.20,
		Recency:     0.10,
	}
}

// Sum returns the sum of all weight terms. Used by NormalizeWeights to detect mis-configuration.
func (w ScoreWeights) Sum() float64 {
	return w.Capability + w.SuccessRate + w.Latency + w.Recency
}

// NormalizeWeights returns a copy of w with all terms scaled so they sum to 1.0.
// If the sum is 0 (all-zero config), DefaultScoreWeights is returned.
// Per spec edge case: "Score weights mis-configured (sum ≠ 1.0) → normalize at config load; warn in log".
func NormalizeWeights(w ScoreWeights) ScoreWeights {
	s := w.Sum()
	if s == 0 {
		return DefaultScoreWeights()
	}
	return ScoreWeights{
		Capability:  w.Capability / s,
		SuccessRate: w.SuccessRate / s,
		Latency:     w.Latency / s,
		Recency:     w.Recency / s,
	}
}

// FallbackConfig holds all configuration for the fallback engine.
// Loaded from the executor.fallback section of config.yaml.
// All fields are optional; zero values yield sensible defaults.
type FallbackConfig struct {
	// MaxAttempts caps the total number of fallback re-attempts (ADR-007).
	// Default: DefaultMaxAttempts (2). Hard upper bound: len(activeCLIs) - 1.
	// YAML key: max_attempts
	MaxAttempts int `yaml:"max_attempts"`

	// ScoreWeights are the four composite score weight terms (ADR-001).
	// Sum is normalized to 1.0 at config load.
	// YAML key: score_weights
	ScoreWeights ScoreWeights `yaml:"score_weights"`

	// LatencyBudget is the p50 latency value at which latency_score = 0.
	// latency_score = 1 - clamp(p50 / budget, 0, 1).
	// YAML key: latency_budget (duration string, e.g. "30s")
	LatencyBudget time.Duration `yaml:"latency_budget"`

	// DecayWindow is the exponential decay window for recency_weight.
	// recency_weight = exp(-(now - last_success) / window).
	// YAML key: decay_window (duration string, e.g. "1h")
	DecayWindow time.Duration `yaml:"decay_window"`

	// FallbackEnabled is a master switch. Default true.
	// When false, the FallbackPicker behaves identically to a bare Picker.
	// YAML key: fallback_enabled
	FallbackEnabled *bool `yaml:"fallback_enabled"`
}

// DefaultFallbackConfig returns a FallbackConfig with all defaults applied.
func DefaultFallbackConfig() FallbackConfig {
	t := true
	return FallbackConfig{
		MaxAttempts:     DefaultMaxAttempts,
		ScoreWeights:    DefaultScoreWeights(),
		LatencyBudget:   DefaultLatencyBudget,
		DecayWindow:     DefaultDecayWindow,
		FallbackEnabled: &t,
	}
}

// IsEnabled reports whether fallback is globally enabled.
// Nil FallbackEnabled pointer defaults to true.
func (c *FallbackConfig) IsEnabled() bool {
	if c.FallbackEnabled == nil {
		return true
	}
	return *c.FallbackEnabled
}

// maxAttempts returns the effective max attempt count, ensuring at least 0.
func (c *FallbackConfig) maxAttempts() int {
	if c.MaxAttempts <= 0 {
		return DefaultMaxAttempts
	}
	return c.MaxAttempts
}

// latencyBudget returns the effective latency budget, ensuring a non-zero value.
func (c *FallbackConfig) latencyBudget() time.Duration {
	if c.LatencyBudget <= 0 {
		return DefaultLatencyBudget
	}
	return c.LatencyBudget
}

// decayWindow returns the effective decay window, ensuring a non-zero value.
func (c *FallbackConfig) decayWindow() time.Duration {
	if c.DecayWindow <= 0 {
		return DefaultDecayWindow
	}
	return c.DecayWindow
}
