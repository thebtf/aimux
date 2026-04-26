// Package api provides ExecutorV2 implementations backed by remote AI APIs
// (OpenAI, Anthropic, Google AI) instead of local CLI processes.
package api

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

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
	apiKey  string
	model   string
	timeout time.Duration
	alive   atomic.Bool
}

func newBase(apiKey, model string) (*baseExecutor, error) {
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
