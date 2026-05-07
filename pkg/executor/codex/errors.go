package codex

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// mapToCliError converts a codex worker internal error into a typed *types.CLIError
// per CR-004 FR-15 mapping table.
//
// Mapping priority (first match wins):
//  1. Already a *types.CLIError — returned as-is (no double-wrap).
//  2. exec.ErrNotFound / "executable file not found" → BinaryNotFound
//  3. context.DeadlineExceeded / "deadline exceeded" / "timeout" → Timeout
//  4. "rate limit" / "rate_limit" / "429" / "quota" → RateLimit
//  5. "auth" + ("fail" / "expir" / "invalid") → AuthExpiry
//  6. "401" / "unauthor" → AuthExpiry
//  7. "patch rejected" / ("sandbox" + "block") → SandboxDenial
//  8. "-32601" / "method not found" / "unsupported" → CapabilityMismatch
//  9. "invalid prompt" / "validation" / "param" → UserInputError
//  10. Anything else → Unknown (terminal; safer than auto-fallback)
//
// mapToCliError never returns nil when err is non-nil.
func mapToCliError(err error) *types.CLIError {
	if err == nil {
		return nil
	}

	// Already typed — pass through without double-wrapping.
	var existing *types.CLIError
	if errors.As(err, &existing) {
		return existing
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

	// BinaryNotFound: exec.LookPath failure or binary-not-found message.
	if errors.Is(err, exec.ErrNotFound) || strings.Contains(lower, "executable file not found") {
		return types.NewBinaryNotFound(msg, err)
	}

	// Timeout: context deadline or explicit timeout language.
	if errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "timeout") {
		return types.NewTimeout(msg, err)
	}

	// RateLimit: HTTP 429 / rate-limit language in stderr.
	if strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "429") ||
		strings.Contains(lower, "quota") {
		return types.NewRateLimit(msg, err)
	}

	// AuthExpiry: codex auth failure / 401 / token expiry.
	if strings.Contains(lower, "auth") &&
		(strings.Contains(lower, "fail") ||
			strings.Contains(lower, "expir") ||
			strings.Contains(lower, "invalid")) {
		return types.NewAuthExpiry(msg, err)
	}
	if strings.Contains(lower, "401") || strings.Contains(lower, "unauthor") {
		return types.NewAuthExpiry(msg, err)
	}

	// SandboxDenial: sandbox read-only denial or patch rejected.
	if strings.Contains(lower, "patch rejected") ||
		(strings.Contains(lower, "sandbox") && strings.Contains(lower, "block")) {
		return types.NewSandboxDenial(msg, err)
	}

	// CapabilityMismatch: JSON-RPC -32601 or method-not-found language.
	if strings.Contains(lower, "-32601") ||
		strings.Contains(lower, "method not found") ||
		strings.Contains(lower, "unsupported") {
		return types.NewCapabilityMismatch(msg, err)
	}

	// UserInputError: invalid prompt or parameter validation failure.
	if strings.Contains(lower, "invalid prompt") ||
		strings.Contains(lower, "validation") ||
		strings.Contains(lower, "param") {
		return types.NewUserInputError(msg, err)
	}

	// Default: Unknown — terminal classification, safer than auto-fallback.
	return types.NewUnknown(msg, err)
}
