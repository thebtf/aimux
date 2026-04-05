package executor

import (
	"context"
	"math"
	"math/rand/v2"
	"time"
)

// Backoff implements exponential backoff with jitter for transient retries.
type Backoff struct {
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	MaxRetries int
}

// DefaultBackoff returns a backoff with sensible defaults.
func DefaultBackoff() Backoff {
	return Backoff{
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   30 * time.Second,
		MaxRetries: 3,
	}
}

// Delay calculates the delay for a given attempt number (0-based).
// Uses full jitter: random value between 0 and min(maxDelay, baseDelay * 2^attempt).
func (b Backoff) Delay(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	exp := math.Pow(2, float64(attempt))
	maxDelay := time.Duration(float64(b.BaseDelay) * exp)

	if maxDelay > b.MaxDelay {
		maxDelay = b.MaxDelay
	}

	// Full jitter
	jitter := rand.Int64N(int64(maxDelay))
	return time.Duration(jitter)
}

// Wait sleeps for the calculated delay, respecting context cancellation.
func (b Backoff) Wait(ctx context.Context, attempt int) error {
	delay := b.Delay(attempt)
	if delay == 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ShouldRetry returns true if another attempt is allowed.
func (b Backoff) ShouldRetry(attempt int) bool {
	return attempt < b.MaxRetries
}
