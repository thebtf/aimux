// Package types defines executor shared types.
package types

import "github.com/thebtf/aimux/loom/clierror"

// CLIErrorCode classifies the root cause of a CLI worker failure.
type CLIErrorCode = clierror.CLIErrorCode

const (
	CLIErrorCodeUnknown                 = clierror.CLIErrorCodeUnknown
	CLIErrorCodeRateLimit               = clierror.CLIErrorCodeRateLimit
	CLIErrorCodeAuthExpiry              = clierror.CLIErrorCodeAuthExpiry
	CLIErrorCodeTimeout                 = clierror.CLIErrorCodeTimeout
	CLIErrorCodeCapabilityMismatch      = clierror.CLIErrorCodeCapabilityMismatch
	CLIErrorCodeUserInputError          = clierror.CLIErrorCodeUserInputError
	CLIErrorCodeSandboxDenial           = clierror.CLIErrorCodeSandboxDenial
	CLIErrorCodeBinaryNotFound          = clierror.CLIErrorCodeBinaryNotFound
	CLIErrorCodeCanceled                = clierror.CLIErrorCodeCanceled
	CLIErrorCodeResumeWorkerMismatch    = clierror.CLIErrorCodeResumeWorkerMismatch
	CLIErrorCodeClassificationAmbiguous = clierror.CLIErrorCodeClassificationAmbiguous
)

// CLIError is the typed error emitted by CLI workers on failure.
type CLIError = clierror.CLIError

// RetryableByCode returns the default fallback/retry hint for a code.
func RetryableByCode(code CLIErrorCode) bool {
	return clierror.RetryableByCode(code)
}

// NewCLIError creates a CLIError with defaults derived from its code.
func NewCLIError(code CLIErrorCode, msg string, cause error) *CLIError {
	return clierror.NewCLIError(code, msg, cause)
}

// NewRateLimit creates a CLIError with CLIErrorCodeRateLimit.
func NewRateLimit(msg string, cause error) *CLIError {
	return clierror.NewRateLimit(msg, cause)
}

// NewAuthExpiry creates a CLIError with CLIErrorCodeAuthExpiry.
func NewAuthExpiry(msg string, cause error) *CLIError {
	return clierror.NewAuthExpiry(msg, cause)
}

// NewTimeout creates a CLIError with CLIErrorCodeTimeout.
func NewTimeout(msg string, cause error) *CLIError {
	return clierror.NewTimeout(msg, cause)
}

// NewCapabilityMismatch creates a CLIError with CLIErrorCodeCapabilityMismatch.
func NewCapabilityMismatch(msg string, cause error) *CLIError {
	return clierror.NewCapabilityMismatch(msg, cause)
}

// NewUserInputError creates a CLIError with CLIErrorCodeUserInputError.
func NewUserInputError(msg string, cause error) *CLIError {
	return clierror.NewUserInputError(msg, cause)
}

// NewSandboxDenial creates a CLIError with CLIErrorCodeSandboxDenial.
func NewSandboxDenial(msg string, cause error) *CLIError {
	return clierror.NewSandboxDenial(msg, cause)
}

// NewBinaryNotFound creates a CLIError with CLIErrorCodeBinaryNotFound.
func NewBinaryNotFound(msg string, cause error) *CLIError {
	return clierror.NewBinaryNotFound(msg, cause)
}

// NewCanceled creates a CLIError with CLIErrorCodeCanceled.
func NewCanceled(msg string, cause error) *CLIError {
	return clierror.NewCanceled(msg, cause)
}

// NewResumeWorkerMismatch creates a CLIError with CLIErrorCodeResumeWorkerMismatch.
func NewResumeWorkerMismatch(msg string, cause error) *CLIError {
	return clierror.NewResumeWorkerMismatch(msg, cause)
}

// NewClassificationAmbiguous creates a CLIError with CLIErrorCodeClassificationAmbiguous.
func NewClassificationAmbiguous(msg string, cause error) *CLIError {
	return clierror.NewClassificationAmbiguous(msg, cause)
}

// NewUnknown creates a CLIError with CLIErrorCodeUnknown.
func NewUnknown(msg string, cause error) *CLIError {
	return clierror.NewUnknown(msg, cause)
}
