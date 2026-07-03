package api

import (
	"sync"

	"github.com/thebtf/aimux/pkg/types"
)

// WithAPIKeyInjector sets a function that provides per-request API keys.
// When set, the injector is called before each Send/SendStream to get a
// fresh key. This supports key rotation, per-tenant keys, and vaulted
// secrets that may change during the executor's lifetime.
//
// If the injector returns an empty string, the original API key from
// construction is used as fallback.
func WithAPIKeyInjector(fn func() string) Option {
	return func(b *baseExecutor) {
		b.keyInjector = fn
	}
}

// effectiveAPIKey returns the API key to use for the current request.
// If a key injector is set and returns a non-empty key, that key is used.
// Otherwise, the original construction-time key is returned.
func (b *baseExecutor) effectiveAPIKey() string {
	if b.keyInjector != nil {
		if key := b.keyInjector(); key != "" {
			return key
		}
	}
	return b.apiKey
}

// Model returns the model name this executor was configured with.
func (b *baseExecutor) Model() string {
	return b.model
}

// Provider returns the provider name for cooldown tracking.
// Concrete executors should call this with their provider string.
func (b *baseExecutor) Provider() string {
	return b.provider
}

// HealthDetail provides extended health information beyond the simple
// HealthStatus enum. Callers can type-assert the executor to get details.
type HealthDetail struct {
	Status       types.HealthStatus `json:"status"`
	Model        string             `json:"model"`
	Provider     string             `json:"provider"`
	RateLimited  bool               `json:"rate_limited"`
	BaseURL      string             `json:"base_url,omitempty"`
	MaxRetries   int                `json:"max_retries"`
}

// healthDetail builds a HealthDetail for the executor's current state.
func (b *baseExecutor) healthDetail() HealthDetail {
	status := b.isAlive()

	rateLimited := false
	if b.cooldown != nil && !b.cooldown.IsAvailable(b.provider, b.model) {
		rateLimited = true
		if status == types.HealthAlive {
			status = types.HealthDegraded
		}
	}

	return HealthDetail{
		Status:      status,
		Model:       b.model,
		Provider:    b.provider,
		RateLimited: rateLimited,
		BaseURL:     b.baseURL,
		MaxRetries:  b.maxRetries,
	}
}

// HealthChecker is an optional interface that API executors implement to
// provide detailed health information beyond the basic IsAlive() status.
type HealthChecker interface {
	HealthDetail() HealthDetail
}

// --- Enriched IsAlive for cooldown-aware health reporting ---

// isAliveWithCooldown returns HealthDegraded when alive but rate-limited.
func (b *baseExecutor) isAliveWithCooldown() types.HealthStatus {
	if !b.alive.Load() {
		return types.HealthDead
	}
	if b.cooldown != nil && !b.cooldown.IsAvailable(b.provider, b.model) {
		return types.HealthDegraded
	}
	return types.HealthAlive
}

// --- Thread-safe last-error tracking for health diagnostics ---

// lastError stores the most recent error for health diagnostics.
type lastError struct {
	mu  sync.RWMutex
	err error
}

func (le *lastError) set(err error) {
	if err == nil {
		return
	}
	le.mu.Lock()
	le.err = err
	le.mu.Unlock()
}

func (le *lastError) get() error {
	le.mu.RLock()
	defer le.mu.RUnlock()
	return le.err
}
