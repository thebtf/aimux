// Package types defines shared CLI error contracts for executor workers.
//
// Every CLI worker (codex, claude, gemini, ...) MUST emit *CLIError on failure.
// AIMUX-4 FailureClassifier switches over CLIErrorCode to decide fallback eligibility.
// String matching on CLI stderr is FORBIDDEN — use this typed contract instead.
//
// Contract for future CLI workers (FR-16): all public error-returning functions MUST
// wrap internal errors via a mapToCliError function (or equivalent) and return
// *CLIError. Callers extract the typed code with errors.As(err, &cliErr).
package types

import "fmt"

// CLIErrorCode classifies the root cause of a CLI worker failure.
// AIMUX-4 FailureClassifier switches over this enum to decide fallback eligibility.
// CLIErrorCodeUnknown is intentionally iota (value 0) so that an uninitialized
// CLIError defaults to Unknown, which is treated as terminal — a safe default.
type CLIErrorCode int

const (
	// CLIErrorCodeUnknown is the default for unmapped errors; treat as terminal.
	CLIErrorCodeUnknown CLIErrorCode = iota
	// CLIErrorCodeRateLimit indicates HTTP 429 or rate-limit quota exhaustion.
	CLIErrorCodeRateLimit
	// CLIErrorCodeAuthExpiry indicates authentication failure or token expiry.
	CLIErrorCodeAuthExpiry
	// CLIErrorCodeTimeout indicates context.DeadlineExceeded or turn deadline.
	CLIErrorCodeTimeout
	// CLIErrorCodeCapabilityMismatch indicates JSON-RPC -32601 or unsupported method.
	CLIErrorCodeCapabilityMismatch
	// CLIErrorCodeUserInputError indicates invalid prompt or param validation failure.
	CLIErrorCodeUserInputError
	// CLIErrorCodeSandboxDenial indicates a sandbox read-only denial or patch rejected.
	CLIErrorCodeSandboxDenial
	// CLIErrorCodeBinaryNotFound indicates exec.LookPath failure (binary not installed).
	CLIErrorCodeBinaryNotFound
	// CLIErrorCodeCanceled indicates the operation was canceled by the caller or system.
	// Distinct from CLIErrorCodeTimeout: cancellation is deliberate, not a deadline breach.
	// AIMUX-4 FailureClassifier treats Canceled as terminal — no retry or fallback.
	CLIErrorCodeCanceled
)

// String returns a human-readable label for logging.
func (c CLIErrorCode) String() string {
	switch c {
	case CLIErrorCodeUnknown:
		return "Unknown"
	case CLIErrorCodeRateLimit:
		return "RateLimit"
	case CLIErrorCodeAuthExpiry:
		return "AuthExpiry"
	case CLIErrorCodeTimeout:
		return "Timeout"
	case CLIErrorCodeCapabilityMismatch:
		return "CapabilityMismatch"
	case CLIErrorCodeUserInputError:
		return "UserInputError"
	case CLIErrorCodeSandboxDenial:
		return "SandboxDenial"
	case CLIErrorCodeBinaryNotFound:
		return "BinaryNotFound"
	case CLIErrorCodeCanceled:
		return "Canceled"
	default:
		return fmt.Sprintf("CLIErrorCode(%d)", int(c))
	}
}

// CLIError is the typed error emitted by all CLI workers on failure.
// Use errors.As(err, &cliErr) to extract the typed code from a returned error.
type CLIError struct {
	// Code classifies the failure for AIMUX-4 FailureClassifier routing.
	Code CLIErrorCode
	// Message is the human-readable error description.
	Message string
	// Wrapped is the original error, preserved for debugging and errors.Is chains.
	Wrapped error
}

// Error implements the error interface. Format: "cli error <Code>: <Message>".
func (e *CLIError) Error() string {
	return fmt.Sprintf("cli error %s: %s", e.Code, e.Message)
}

// Unwrap returns the wrapped error, enabling errors.Is and errors.As chains
// to traverse through the CLIError to the underlying cause.
func (e *CLIError) Unwrap() error {
	return e.Wrapped
}

// --- Constructors ---

// NewRateLimit creates a CLIError with CLIErrorCodeRateLimit.
func NewRateLimit(msg string, wrapped error) *CLIError {
	return &CLIError{Code: CLIErrorCodeRateLimit, Message: msg, Wrapped: wrapped}
}

// NewAuthExpiry creates a CLIError with CLIErrorCodeAuthExpiry.
func NewAuthExpiry(msg string, wrapped error) *CLIError {
	return &CLIError{Code: CLIErrorCodeAuthExpiry, Message: msg, Wrapped: wrapped}
}

// NewTimeout creates a CLIError with CLIErrorCodeTimeout.
func NewTimeout(msg string, wrapped error) *CLIError {
	return &CLIError{Code: CLIErrorCodeTimeout, Message: msg, Wrapped: wrapped}
}

// NewCapabilityMismatch creates a CLIError with CLIErrorCodeCapabilityMismatch.
func NewCapabilityMismatch(msg string, wrapped error) *CLIError {
	return &CLIError{Code: CLIErrorCodeCapabilityMismatch, Message: msg, Wrapped: wrapped}
}

// NewUserInputError creates a CLIError with CLIErrorCodeUserInputError.
func NewUserInputError(msg string, wrapped error) *CLIError {
	return &CLIError{Code: CLIErrorCodeUserInputError, Message: msg, Wrapped: wrapped}
}

// NewSandboxDenial creates a CLIError with CLIErrorCodeSandboxDenial.
func NewSandboxDenial(msg string, wrapped error) *CLIError {
	return &CLIError{Code: CLIErrorCodeSandboxDenial, Message: msg, Wrapped: wrapped}
}

// NewBinaryNotFound creates a CLIError with CLIErrorCodeBinaryNotFound.
func NewBinaryNotFound(msg string, wrapped error) *CLIError {
	return &CLIError{Code: CLIErrorCodeBinaryNotFound, Message: msg, Wrapped: wrapped}
}

// NewCanceled creates a CLIError with CLIErrorCodeCanceled.
// Use when the caller or system deliberately canceled the operation.
func NewCanceled(msg string, wrapped error) *CLIError {
	return &CLIError{Code: CLIErrorCodeCanceled, Message: msg, Wrapped: wrapped}
}

// NewUnknown creates a CLIError with CLIErrorCodeUnknown.
// Use for errors that do not match any known classification; treated as terminal.
func NewUnknown(msg string, wrapped error) *CLIError {
	return &CLIError{Code: CLIErrorCodeUnknown, Message: msg, Wrapped: wrapped}
}
