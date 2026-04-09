// Package ratelimit implements a per-tool token bucket rate limiter using Go stdlib only.
package ratelimit

import (
	"sync"
	"time"
)

const (
	DefaultRPS   = 10.0
	DefaultBurst = 20
)

// bucket is a token bucket for a single tool.
type bucket struct {
	tokens    float64
	lastFill  time.Time
	rps       float64
	burst     float64
}

// Limiter holds independent token buckets per tool name.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rps     float64
	burst   float64
}

// New creates a rate limiter with the given rate and burst.
// If rps <= 0, DefaultRPS is used. If burst <= 0, DefaultBurst is used.
func New(rps float64, burst int) *Limiter {
	if rps <= 0 {
		rps = DefaultRPS
	}
	b := float64(burst)
	if b <= 0 {
		b = DefaultBurst
	}
	return &Limiter{
		buckets: make(map[string]*bucket),
		rps:     rps,
		burst:   b,
	}
}

// Allow returns true if the request for the named tool is within the rate limit.
// A false return means the request should be rejected.
func (l *Limiter) Allow(tool string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[tool]
	if !ok {
		// New tool — start with a full bucket and consume one token immediately.
		b = &bucket{
			tokens:   l.burst - 1,
			lastFill: now,
			rps:      l.rps,
			burst:    l.burst,
		}
		l.buckets[tool] = b
		return true
	}

	// Replenish tokens based on elapsed time.
	elapsed := now.Sub(b.lastFill).Seconds()
	b.tokens += elapsed * b.rps
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.lastFill = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
