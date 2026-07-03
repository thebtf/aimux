package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// ErrModelCooledDown is returned when Send/SendStream is called for a model
// that is currently on cooldown due to a prior rate-limit response.
var ErrModelCooledDown = errors.New("api executor: model is on cooldown")

// defaultCooldownDuration is the fallback duration when a 429 response does
// not include a Retry-After header.
const defaultCooldownDuration = 60 * time.Second

// parseRetryAfter extracts a cooldown duration from an HTTP 429 error message
// or an API error body. It looks for patterns like "Retry-After: 30",
// "retry after 30 seconds", or "retry_after":30.
//
// If no parseable retry hint is found, it returns defaultCooldownDuration.
func parseRetryAfter(errMsg string) time.Duration {
	lower := strings.ToLower(errMsg)

	// Pattern 1: "retry-after: <seconds>" (HTTP header in error text)
	if idx := strings.Index(lower, "retry-after:"); idx >= 0 {
		if d := extractSeconds(lower[idx+len("retry-after:"):]); d > 0 {
			return d
		}
	}

	// Pattern 2: "retry after <seconds> seconds" (prose in API error body)
	if idx := strings.Index(lower, "retry after "); idx >= 0 {
		if d := extractSeconds(lower[idx+len("retry after "):]); d > 0 {
			return d
		}
	}

	// Pattern 3: "retry_after":<seconds> or "retry_after": <seconds> (JSON)
	for _, prefix := range []string{`"retry_after":`, `"retry_after": `} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			if d := extractSeconds(lower[idx+len(prefix):]); d > 0 {
				return d
			}
		}
	}

	return defaultCooldownDuration
}

// extractSeconds reads leading digits from s and interprets them as seconds.
// Returns 0 if no valid number is found.
func extractSeconds(s string) time.Duration {
	s = strings.TrimSpace(s)
	var digits []byte
	for i := 0; i < len(s) && i < 10; i++ {
		if s[i] >= '0' && s[i] <= '9' {
			digits = append(digits, s[i])
		} else if len(digits) > 0 {
			break
		}
	}
	if len(digits) == 0 {
		return 0
	}
	n, err := strconv.Atoi(string(digits))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// isRateLimitError detects HTTP 429 / rate-limit errors from any of the three
// provider SDKs. Each SDK wraps the HTTP status differently, so we check both
// the error message string and any http.Response embedded in the error chain.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	// Check for embedded HTTP response with 429 status.
	// The OpenAI and Anthropic Go SDKs return errors that may implement
	// an interface with StatusCode or wrap an *http.Response.
	type statusCoder interface {
		StatusCode() int
	}
	var sc statusCoder
	if errors.As(err, &sc) && sc.StatusCode() == http.StatusTooManyRequests {
		return true
	}

	// Fallback: string matching for rate limit indicators in error message.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "resource_exhausted")
}

// checkCooldown verifies the model is available before making an API call.
// Returns nil if no tracker is configured or the model is available.
func checkCooldown(tracker types.ModelCooldownTracker, provider, model string) error {
	if tracker == nil {
		return nil
	}
	if !tracker.IsAvailable(provider, model) {
		return fmt.Errorf("%w: provider=%s model=%s", ErrModelCooledDown, provider, model)
	}
	return nil
}

// handleRateLimitError checks if err is a rate-limit error and, if so, marks
// the model as cooled down. Returns the original error unchanged.
func handleRateLimitError(tracker types.ModelCooldownTracker, provider, model string, err error) error {
	if err == nil || tracker == nil {
		return err
	}
	if isRateLimitError(err) {
		duration := parseRetryAfter(err.Error())
		tracker.MarkCooledDown(provider, model, duration, err.Error())
	}
	return err
}
