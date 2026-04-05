package executor_test

import (
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
)

func TestCircuitBreaker_ClosedAllows(t *testing.T) {
	cb := executor.NewCircuitBreaker(3, 5, 1)

	if !cb.Allow() {
		t.Error("closed breaker should allow requests")
	}
	if cb.State() != executor.BreakerClosed {
		t.Error("initial state should be closed")
	}
}

func TestCircuitBreaker_TripsAfterThreshold(t *testing.T) {
	cb := executor.NewCircuitBreaker(3, 5, 1)

	cb.RecordFailure(false)
	cb.RecordFailure(false)
	if cb.State() != executor.BreakerClosed {
		t.Error("should still be closed after 2 failures")
	}

	cb.RecordFailure(false)
	if cb.State() != executor.BreakerOpen {
		t.Error("should be open after 3 failures")
	}

	if cb.Allow() {
		t.Error("open breaker should not allow requests")
	}
}

func TestCircuitBreaker_PermanentFailureTripsImmediately(t *testing.T) {
	cb := executor.NewCircuitBreaker(3, 5, 1)

	cb.RecordFailure(true) // permanent
	if cb.State() != executor.BreakerOpen {
		t.Error("permanent failure should trip immediately")
	}
}

func TestCircuitBreaker_CooldownToHalfOpen(t *testing.T) {
	cb := executor.NewCircuitBreaker(3, 1, 1) // 1s cooldown

	// Trip the breaker
	cb.RecordFailure(true)
	if cb.Allow() {
		t.Error("should not allow immediately after trip")
	}

	// Wait for cooldown
	time.Sleep(1100 * time.Millisecond)

	if !cb.Allow() {
		t.Error("should allow after cooldown (half-open)")
	}
	if cb.State() != executor.BreakerHalfOpen {
		t.Error("should be half-open after cooldown")
	}
}

func TestCircuitBreaker_SuccessResetsToClosed(t *testing.T) {
	cb := executor.NewCircuitBreaker(3, 1, 1)

	cb.RecordFailure(true)
	time.Sleep(1100 * time.Millisecond)
	cb.Allow() // transition to half-open

	cb.RecordSuccess()
	if cb.State() != executor.BreakerClosed {
		t.Error("success in half-open should reset to closed")
	}
	if cb.Failures() != 0 {
		t.Error("failure count should be reset")
	}
}

func TestCircuitBreaker_HalfOpenLimitsProbes(t *testing.T) {
	cb := executor.NewCircuitBreaker(3, 1, 1) // max 1 half-open call

	cb.RecordFailure(true)
	time.Sleep(1100 * time.Millisecond)

	if !cb.Allow() {
		t.Error("first probe should be allowed")
	}
	if cb.Allow() {
		t.Error("second probe should be blocked (max 1)")
	}
}

func TestBreakerRegistry(t *testing.T) {
	reg := executor.NewBreakerRegistry(executor.BreakerConfig{
		FailureThreshold: 3,
		CooldownSeconds:  5,
		HalfOpenMaxCalls: 1,
	})

	cb1 := reg.Get("codex")
	cb2 := reg.Get("codex")
	if cb1 != cb2 {
		t.Error("same CLI should return same breaker instance")
	}

	cb3 := reg.Get("gemini")
	if cb1 == cb3 {
		t.Error("different CLIs should have different breakers")
	}
}

func TestBreakerRegistry_AvailableCLIs(t *testing.T) {
	reg := executor.NewBreakerRegistry(executor.BreakerConfig{
		FailureThreshold: 1,
		CooldownSeconds:  300,
		HalfOpenMaxCalls: 1,
	})

	// Trip codex breaker
	reg.Get("codex").RecordFailure(true)

	available := reg.AvailableCLIs([]string{"codex", "gemini", "claude"})
	for _, cli := range available {
		if cli == "codex" {
			t.Error("codex should not be available (breaker open)")
		}
	}
	if len(available) != 2 {
		t.Errorf("expected 2 available, got %d", len(available))
	}
}
