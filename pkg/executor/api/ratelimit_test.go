package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// ---------------------------------------------------------------------------
// parseRetryAfter
// ---------------------------------------------------------------------------

func TestParseRetryAfter_HeaderPattern(t *testing.T) {
	got := parseRetryAfter("HTTP 429: Retry-After: 30")
	if got != 30*time.Second {
		t.Errorf("got %v, want 30s", got)
	}
}

func TestParseRetryAfter_ProsePattern(t *testing.T) {
	got := parseRetryAfter("Rate limit exceeded. Please retry after 60 seconds.")
	if got != 60*time.Second {
		t.Errorf("got %v, want 60s", got)
	}
}

func TestParseRetryAfter_JSONPattern(t *testing.T) {
	got := parseRetryAfter(`{"error":"rate_limit","retry_after":45}`)
	if got != 45*time.Second {
		t.Errorf("got %v, want 45s", got)
	}
}

func TestParseRetryAfter_NoHint(t *testing.T) {
	got := parseRetryAfter("Internal server error")
	if got != defaultCooldownDuration {
		t.Errorf("got %v, want %v", got, defaultCooldownDuration)
	}
}

// ---------------------------------------------------------------------------
// isRateLimitError
// ---------------------------------------------------------------------------

func TestIsRateLimitError_Nil(t *testing.T) {
	if isRateLimitError(nil) {
		t.Error("nil error should not be a rate limit error")
	}
}

func TestIsRateLimitError_429InMessage(t *testing.T) {
	err := fmt.Errorf("openai: request failed with status 429")
	if !isRateLimitError(err) {
		t.Error("expected 429 in message to be detected")
	}
}

func TestIsRateLimitError_RateLimitString(t *testing.T) {
	err := fmt.Errorf("rate_limit_error: too many requests")
	if !isRateLimitError(err) {
		t.Error("expected rate_limit to be detected")
	}
}

func TestIsRateLimitError_TooManyRequests(t *testing.T) {
	err := fmt.Errorf("Too Many Requests")
	if !isRateLimitError(err) {
		t.Error("expected 'Too Many Requests' to be detected")
	}
}

func TestIsRateLimitError_RegularError(t *testing.T) {
	err := fmt.Errorf("connection timeout")
	if isRateLimitError(err) {
		t.Error("regular error should not be detected as rate limit")
	}
}

// statusCodeErr implements the statusCoder interface used by SDK error types.
type statusCodeErr struct {
	code int
	msg  string
}

func (e *statusCodeErr) Error() string  { return e.msg }
func (e *statusCodeErr) StatusCode() int { return e.code }

func TestIsRateLimitError_StatusCodeInterface(t *testing.T) {
	err := &statusCodeErr{code: http.StatusTooManyRequests, msg: "rate limited"}
	if !isRateLimitError(err) {
		t.Error("expected StatusCode() 429 to be detected")
	}
}

func TestIsRateLimitError_StatusCodeNon429(t *testing.T) {
	err := &statusCodeErr{code: http.StatusInternalServerError, msg: "server error"}
	if isRateLimitError(err) {
		t.Error("500 error should not be detected as rate limit")
	}
}

func TestIsRateLimitError_ResourceExhausted(t *testing.T) {
	err := fmt.Errorf("google ai: RESOURCE_EXHAUSTED: quota exceeded")
	if !isRateLimitError(err) {
		t.Error("expected resource_exhausted to be detected")
	}
}

// ---------------------------------------------------------------------------
// checkCooldown
// ---------------------------------------------------------------------------

func TestCheckCooldown_NilTracker(t *testing.T) {
	if err := checkCooldown(nil, "openai", "gpt-4o"); err != nil {
		t.Errorf("nil tracker should return nil, got: %v", err)
	}
}

func TestCheckCooldown_Available(t *testing.T) {
	tracker := &mockCooldownTracker{available: true}
	if err := checkCooldown(tracker, "openai", "gpt-4o"); err != nil {
		t.Errorf("available model should return nil, got: %v", err)
	}
}

func TestCheckCooldown_CooledDown(t *testing.T) {
	tracker := &mockCooldownTracker{available: false}
	err := checkCooldown(tracker, "openai", "gpt-4o")
	if err == nil {
		t.Fatal("cooled-down model should return error")
	}
	if !errors.Is(err, ErrModelCooledDown) {
		t.Errorf("expected ErrModelCooledDown, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// handleRateLimitError
// ---------------------------------------------------------------------------

func TestHandleRateLimitError_NilError(t *testing.T) {
	tracker := &mockCooldownTracker{available: true}
	if err := handleRateLimitError(tracker, "openai", "gpt-4o", nil); err != nil {
		t.Errorf("nil error should pass through, got: %v", err)
	}
	if tracker.markCalled {
		t.Error("MarkCooledDown should not be called for nil error")
	}
}

func TestHandleRateLimitError_NilTracker(t *testing.T) {
	err := fmt.Errorf("429 rate limited")
	got := handleRateLimitError(nil, "openai", "gpt-4o", err)
	if got != err {
		t.Errorf("should return original error, got: %v", got)
	}
}

func TestHandleRateLimitError_NonRateLimit(t *testing.T) {
	tracker := &mockCooldownTracker{available: true}
	err := fmt.Errorf("connection timeout")
	got := handleRateLimitError(tracker, "openai", "gpt-4o", err)
	if got != err {
		t.Error("should return original error")
	}
	if tracker.markCalled {
		t.Error("non-rate-limit error should not trigger cooldown")
	}
}

func TestHandleRateLimitError_RateLimitTriggersMarkCooledDown(t *testing.T) {
	tracker := &mockCooldownTracker{available: true}
	err := fmt.Errorf("429: rate_limit_error, retry after 30 seconds")
	got := handleRateLimitError(tracker, "anthropic", "claude-3-5-sonnet", err)
	if got != err {
		t.Error("should return original error")
	}
	if !tracker.markCalled {
		t.Fatal("MarkCooledDown should be called for 429")
	}
	if tracker.lastCLI != "anthropic" {
		t.Errorf("cli: got %q, want %q", tracker.lastCLI, "anthropic")
	}
	if tracker.lastModel != "claude-3-5-sonnet" {
		t.Errorf("model: got %q, want %q", tracker.lastModel, "claude-3-5-sonnet")
	}
	if tracker.lastDuration != 30*time.Second {
		t.Errorf("duration: got %v, want 30s", tracker.lastDuration)
	}
}

// ---------------------------------------------------------------------------
// mockCooldownTracker
// ---------------------------------------------------------------------------

type mockCooldownTracker struct {
	available    bool
	markCalled   bool
	lastCLI      string
	lastModel    string
	lastDuration time.Duration
}

func (m *mockCooldownTracker) MarkCooledDown(cli, model string, duration time.Duration, triggerStderr string) {
	m.markCalled = true
	m.lastCLI = cli
	m.lastModel = model
	m.lastDuration = duration
}

func (m *mockCooldownTracker) IsAvailable(cli, model string) bool {
	return m.available
}

func (m *mockCooldownTracker) FilterAvailable(cli string, models []string) []string {
	if m.available {
		return models
	}
	return nil
}

func (m *mockCooldownTracker) SetDuration(cli, model string, duration time.Duration) {}

func (m *mockCooldownTracker) Flush(cli, model string) error { return nil }

func (m *mockCooldownTracker) List() []types.CooldownEntry { return nil }
