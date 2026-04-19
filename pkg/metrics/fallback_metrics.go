// Package metrics provides a thread-safe collector for request/latency/error counters.
package metrics

import (
	"sync"
	"sync/atomic"
)

// Fallback result label constants for aimux_fallback_attempts_total.
const (
	FallbackResultSuccess     = "success"
	FallbackResultRateLimit   = "rate_limit"
	FallbackResultUnavailable = "unavailable"
	FallbackResultTransient   = "transient"
	FallbackResultFatal       = "fatal"
)

// FallbackAttemptKey is the label tuple (cli, model, result) for the counter.
type FallbackAttemptKey struct {
	CLI    string
	Model  string
	Result string
}

// FallbackCounter is a thread-safe counter for aimux_fallback_attempts_total{cli,model,result}.
// The zero value is NOT valid; use NewFallbackCounter.
type FallbackCounter struct {
	mu      sync.RWMutex
	entries map[FallbackAttemptKey]*atomic.Int64
}

// NewFallbackCounter creates a ready-to-use FallbackCounter.
func NewFallbackCounter() *FallbackCounter {
	return &FallbackCounter{
		entries: make(map[FallbackAttemptKey]*atomic.Int64),
	}
}

// Inc increments the counter for (cli, model, result).
func (c *FallbackCounter) Inc(cli, model, result string) {
	key := FallbackAttemptKey{CLI: cli, Model: model, Result: result}
	c.mu.RLock()
	e := c.entries[key]
	c.mu.RUnlock()
	if e != nil {
		e.Add(1)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e = c.entries[key]; e == nil {
		e = &atomic.Int64{}
		c.entries[key] = e
	}
	e.Add(1)
}

// Get returns the current count for (cli, model, result). Returns 0 if never incremented.
func (c *FallbackCounter) Get(cli, model, result string) int64 {
	key := FallbackAttemptKey{CLI: cli, Model: model, Result: result}
	c.mu.RLock()
	e := c.entries[key]
	c.mu.RUnlock()
	if e == nil {
		return 0
	}
	return e.Load()
}

// Total returns the sum of all counters across all label tuples.
func (c *FallbackCounter) Total() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var total int64
	for _, e := range c.entries {
		total += e.Load()
	}
	return total
}

// Snapshot returns a copy of all (key → count) entries for metrics export.
func (c *FallbackCounter) Snapshot() map[FallbackAttemptKey]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[FallbackAttemptKey]int64, len(c.entries))
	for k, e := range c.entries {
		out[k] = e.Load()
	}
	return out
}
