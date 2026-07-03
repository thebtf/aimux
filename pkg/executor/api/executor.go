// Package api provides ExecutorV2 implementations backed by remote AI APIs
// (OpenAI, Anthropic, Google AI) instead of local CLI processes.
package api

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// Option configures an API executor. Use With* functions to create options.
type Option func(*baseExecutor)

// WithCooldownTracker sets a cooldown tracker for rate-limit integration.
func WithCooldownTracker(t types.ModelCooldownTracker) Option {
	return func(b *baseExecutor) {
		b.cooldown = t
	}
}

// WithTimeout overrides the default API call timeout.
func WithTimeout(d time.Duration) Option {
	return func(b *baseExecutor) {
		if d > 0 {
			b.timeout = d
		}
	}
}

// WithBaseURL overrides the provider's default API base URL.
// Provider executors that support it read b.baseURL during client creation.
func WithBaseURL(url string) Option {
	return func(b *baseExecutor) {
		b.baseURL = url
	}
}

// WithMaxRetries sets the maximum number of automatic retries on transient
// errors (timeouts, 5xx). Zero means no retries. Provider executors read
// b.maxRetries at call time.
func WithMaxRetries(n int) Option {
	return func(b *baseExecutor) {
		if n >= 0 {
			b.maxRetries = n
		}
	}
}

// Config is a declarative executor configuration used by NewFromConfig to
// create any supported API executor without knowing provider-specific
// constructors.
type Config struct {
	// Provider is "openai", "anthropic", or "google".
	Provider string
	APIKey   string
	Model    string // empty → provider default
	BaseURL  string // empty → provider default
	Timeout  time.Duration
	MaxRetries int
	Cooldown types.ModelCooldownTracker
}

// NewFromConfig creates an API executor from a Config struct.
// The returned value satisfies types.ExecutorV2.
func NewFromConfig(cfg Config) (types.ExecutorV2, error) {
	var opts []Option
	if cfg.Cooldown != nil {
		opts = append(opts, WithCooldownTracker(cfg.Cooldown))
	}
	if cfg.Timeout > 0 {
		opts = append(opts, WithTimeout(cfg.Timeout))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, WithBaseURL(cfg.BaseURL))
	}
	if cfg.MaxRetries > 0 {
		opts = append(opts, WithMaxRetries(cfg.MaxRetries))
	}

	switch cfg.Provider {
	case "openai":
		return NewOpenAI(cfg.APIKey, cfg.Model, opts...)
	case "anthropic":
		return NewAnthropic(cfg.APIKey, cfg.Model, opts...)
	case "google":
		return NewGoogleAI(cfg.APIKey, cfg.Model, opts...)
	default:
		return nil, fmt.Errorf("api executor: unknown provider %q (supported: openai, anthropic, google)", cfg.Provider)
	}
}

const (
	DefaultOpenAIModel    = "gpt-4o"
	DefaultAnthropicModel = "claude-sonnet-4-5-20250929"
	DefaultGoogleAIModel  = "gemini-2.0-flash"

	// DefaultTimeout is the maximum time allowed for a single API call.
	DefaultTimeout = 5 * time.Minute
)

// baseExecutor holds fields common to all API executor implementations.
// It is embedded by value (not pointer) in each concrete executor so that
// the zero value is valid (alive == false until set).
type baseExecutor struct {
	apiKey      string
	model       string
	provider    string // "openai", "anthropic", "google"
	timeout     time.Duration
	baseURL     string // optional; empty → provider default
	maxRetries  int    // 0 → no retries
	alive       atomic.Bool
	cooldown    types.ModelCooldownTracker // optional; nil means no cooldown tracking
	keyInjector func() string             // optional; per-request API key
	lastErr     lastError                 // most recent error for diagnostics
}

func newBase(apiKey, model string, opts ...Option) (*baseExecutor, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api executor: API key must not be empty")
	}
	if model == "" {
		return nil, fmt.Errorf("api executor: model must not be empty")
	}
	b := &baseExecutor{
		apiKey:  apiKey,
		model:   model,
		timeout: DefaultTimeout,
	}
	for _, opt := range opts {
		opt(b)
	}
	b.alive.Store(true)
	return b, nil
}

// isAlive returns HealthAlive when the executor has not been closed, HealthDead
// otherwise.
func (b *baseExecutor) isAlive() types.HealthStatus {
	if b.alive.Load() {
		return types.HealthAlive
	}
	return types.HealthDead
}

// close marks the executor as shut down.  Callers must embed this in their
// Close() implementations.
func (b *baseExecutor) close() error {
	b.alive.Store(false)
	return nil
}

// buildHistory converts a slice of types.Turn into a list of role-tagged
// strings.  The concrete executors use this helper to iterate and build their
// own SDK-specific message slices.
func buildHistory(history []types.Turn) []types.Turn {
	// Return a defensive copy to uphold immutability rules.
	if len(history) == 0 {
		return nil
	}
	out := make([]types.Turn, len(history))
	copy(out, history)
	return out
}
