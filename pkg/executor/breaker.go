// Package executor provides the executor selection and circuit breaker logic.
package executor

import (
	"sync"
	"time"
)

// BreakerState represents circuit breaker states.
type BreakerState int

const (
	BreakerClosed   BreakerState = iota // Normal — requests flow through
	BreakerOpen                          // Tripped — requests rejected
	BreakerHalfOpen                      // Testing — one probe allowed
)

// CircuitBreaker tracks failure state per CLI to prevent cascading failures.
type CircuitBreaker struct {
	state            BreakerState
	failures         int
	lastFailure      time.Time
	halfOpenAttempts int

	failureThreshold  int
	cooldownDuration  time.Duration
	halfOpenMaxCalls  int

	mu sync.Mutex
}

// NewCircuitBreaker creates a circuit breaker with configured thresholds.
func NewCircuitBreaker(failureThreshold int, cooldownSeconds int, halfOpenMax int) *CircuitBreaker {
	return &CircuitBreaker{
		state:            BreakerClosed,
		failureThreshold: failureThreshold,
		cooldownDuration: time.Duration(cooldownSeconds) * time.Second,
		halfOpenMaxCalls: halfOpenMax,
	}
}

// Allow checks if a request should be allowed through.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case BreakerClosed:
		return true

	case BreakerOpen:
		// Check if cooldown has elapsed
		if time.Since(cb.lastFailure) > cb.cooldownDuration {
			cb.state = BreakerHalfOpen
			cb.halfOpenAttempts = 1 // This transition counts as the first probe
			return true
		}
		return false

	case BreakerHalfOpen:
		if cb.halfOpenAttempts < cb.halfOpenMaxCalls {
			cb.halfOpenAttempts++
			return true
		}
		return false

	default:
		return false
	}
}

// CanAllow checks whether the breaker would allow a request without
// advancing state. Unlike Allow(), this is side-effect-free and safe
// to call in read-only queries like AvailableCLIs.
func (cb *CircuitBreaker) CanAllow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		return time.Since(cb.lastFailure) > cb.cooldownDuration
	case BreakerHalfOpen:
		return cb.halfOpenAttempts < cb.halfOpenMaxCalls
	default:
		return false
	}
}

// RecordSuccess records a successful call. Resets the breaker to closed.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = BreakerClosed
}

// RecordFailure records a failed call. May trip the breaker to open.
// permanent=true means the failure is not transient (e.g., CLI not found)
// and should immediately open the circuit.
func (cb *CircuitBreaker) RecordFailure(permanent bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	if permanent || cb.failures >= cb.failureThreshold {
		cb.state = BreakerOpen
	}
}

// State returns the current breaker state.
func (cb *CircuitBreaker) State() BreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Failures returns the current failure count.
func (cb *CircuitBreaker) Failures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failures
}

// BreakerRegistry manages circuit breakers for all CLIs.
type BreakerRegistry struct {
	breakers map[string]*CircuitBreaker
	config   BreakerConfig
	mu       sync.RWMutex
}

// BreakerConfig holds circuit breaker configuration.
type BreakerConfig struct {
	FailureThreshold int
	CooldownSeconds  int
	HalfOpenMaxCalls int
}

// NewBreakerRegistry creates a registry with the given config.
func NewBreakerRegistry(cfg BreakerConfig) *BreakerRegistry {
	return &BreakerRegistry{
		breakers: make(map[string]*CircuitBreaker),
		config:   cfg,
	}
}

// Get returns the circuit breaker for a CLI, creating one if needed.
func (r *BreakerRegistry) Get(cli string) *CircuitBreaker {
	r.mu.RLock()
	if cb, ok := r.breakers[cli]; ok {
		r.mu.RUnlock()
		return cb
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after upgrade
	if cb, ok := r.breakers[cli]; ok {
		return cb
	}

	cb := NewCircuitBreaker(r.config.FailureThreshold, r.config.CooldownSeconds, r.config.HalfOpenMaxCalls)
	r.breakers[cli] = cb
	return cb
}

// AvailableCLIs returns CLIs whose circuit breakers allow requests.
func (r *BreakerRegistry) AvailableCLIs(clis []string) []string {
	var available []string
	for _, cli := range clis {
		if r.Get(cli).CanAllow() {
			available = append(available, cli)
		}
	}
	return available
}
