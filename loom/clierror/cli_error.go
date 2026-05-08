// Package clierror defines the shared typed CLI error contract.
package clierror

import "fmt"

// CLIErrorCode classifies the root cause of a CLI worker failure.
// CLIErrorCodeUnknown is intentionally zero so uninitialized values fail closed.
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
	// CLIErrorCodeCapabilityMismatch indicates an unsupported capability.
	CLIErrorCodeCapabilityMismatch
	// CLIErrorCodeUserInputError indicates invalid prompt or parameter validation failure.
	CLIErrorCodeUserInputError
	// CLIErrorCodeSandboxDenial indicates a sandbox read/write denial.
	CLIErrorCodeSandboxDenial
	// CLIErrorCodeBinaryNotFound indicates a missing CLI binary.
	CLIErrorCodeBinaryNotFound
	// CLIErrorCodeCanceled indicates deliberate caller or system cancellation.
	CLIErrorCodeCanceled
	// CLIErrorCodeResumeWorkerMismatch indicates resume_id cannot be used by this worker or worktree.
	CLIErrorCodeResumeWorkerMismatch
	// CLIErrorCodeClassificationAmbiguous indicates task_class classifier confidence was too low.
	CLIErrorCodeClassificationAmbiguous
)

// String returns a human-readable label for logging and JSON-adjacent diagnostics.
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
	case CLIErrorCodeResumeWorkerMismatch:
		return "ResumeWorkerMismatch"
	case CLIErrorCodeClassificationAmbiguous:
		return "ClassificationAmbiguous"
	default:
		return fmt.Sprintf("CLIErrorCode(%d)", int(c))
	}
}

// CLIError is the typed error emitted by CLI workers on failure.
// Cause is not JSON-serializable; CauseStr is the stable wire/debug form.
type CLIError struct {
	Code      CLIErrorCode `json:"code"`
	Message   string       `json:"message"`
	Cause     error        `json:"-"`
	CauseStr  string       `json:"cause,omitempty"`
	CLI       string       `json:"cli,omitempty"`
	Retryable bool         `json:"retryable"`
}

// Error implements the error interface.
func (e *CLIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("cli error %s: %s", e.Code, e.Message)
}

// Unwrap returns the original cause, enabling errors.Is/errors.As chains.
func (e *CLIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// WithCLI returns a copy annotated with the CLI backend that produced the error.
func (e *CLIError) WithCLI(cli string) *CLIError {
	if e == nil {
		return nil
	}
	cp := *e
	cp.CLI = cli
	return &cp
}

// RetryableByCode returns the default fallback/retry hint for a code.
func RetryableByCode(code CLIErrorCode) bool {
	switch code {
	case CLIErrorCodeRateLimit, CLIErrorCodeAuthExpiry, CLIErrorCodeTimeout, CLIErrorCodeCapabilityMismatch:
		return true
	default:
		return false
	}
}

// NewCLIError creates a CLIError with defaults derived from its code.
func NewCLIError(code CLIErrorCode, msg string, cause error) *CLIError {
	return &CLIError{
		Code:      code,
		Message:   msg,
		Cause:     cause,
		CauseStr:  causeString(cause),
		Retryable: RetryableByCode(code),
	}
}

func causeString(cause error) string {
	if cause == nil {
		return ""
	}
	return cause.Error()
}

// NewRateLimit creates a CLIError with CLIErrorCodeRateLimit.
func NewRateLimit(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeRateLimit, msg, cause)
}

// NewAuthExpiry creates a CLIError with CLIErrorCodeAuthExpiry.
func NewAuthExpiry(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeAuthExpiry, msg, cause)
}

// NewTimeout creates a CLIError with CLIErrorCodeTimeout.
func NewTimeout(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeTimeout, msg, cause)
}

// NewCapabilityMismatch creates a CLIError with CLIErrorCodeCapabilityMismatch.
func NewCapabilityMismatch(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeCapabilityMismatch, msg, cause)
}

// NewUserInputError creates a CLIError with CLIErrorCodeUserInputError.
func NewUserInputError(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeUserInputError, msg, cause)
}

// NewSandboxDenial creates a CLIError with CLIErrorCodeSandboxDenial.
func NewSandboxDenial(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeSandboxDenial, msg, cause)
}

// NewBinaryNotFound creates a CLIError with CLIErrorCodeBinaryNotFound.
func NewBinaryNotFound(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeBinaryNotFound, msg, cause)
}

// NewCanceled creates a CLIError with CLIErrorCodeCanceled.
func NewCanceled(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeCanceled, msg, cause)
}

// NewResumeWorkerMismatch creates a CLIError with CLIErrorCodeResumeWorkerMismatch.
func NewResumeWorkerMismatch(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeResumeWorkerMismatch, msg, cause)
}

// NewClassificationAmbiguous creates a CLIError with CLIErrorCodeClassificationAmbiguous.
func NewClassificationAmbiguous(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeClassificationAmbiguous, msg, cause)
}

// NewUnknown creates a CLIError with CLIErrorCodeUnknown.
func NewUnknown(msg string, cause error) *CLIError {
	return NewCLIError(CLIErrorCodeUnknown, msg, cause)
}
