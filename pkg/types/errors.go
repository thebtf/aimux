package types

import (
	"errors"
	"fmt"
)

// ErrorType classifies errors at API boundaries.
// Constitution P7: string error messages prohibited at API boundaries.
type ErrorType string

const (
	ErrorTypeExecutor    ErrorType = "ExecutorError"
	ErrorTypeTimeout     ErrorType = "TimeoutError"
	ErrorTypeValidation  ErrorType = "ValidationError"
	ErrorTypeConfig      ErrorType = "ConfigError"
	ErrorTypeNotFound    ErrorType = "NotFoundError"
	ErrorTypeCircuitOpen ErrorType = "CircuitOpenError"
)

// TypedError carries structured error info with optional partial output.
type TypedError struct {
	Type          ErrorType `json:"type"`
	Message       string    `json:"message"`
	PartialOutput string    `json:"partial_output,omitempty"`
	Cause         error     `json:"-"`
}

// Error implements the error interface.
func (e *TypedError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (caused by: %v)", e.Type, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// Unwrap returns the underlying cause for errors.Is/As support.
func (e *TypedError) Unwrap() error {
	return e.Cause
}

// NewExecutorError creates an executor error with optional partial output.
func NewExecutorError(msg string, cause error, partial string) *TypedError {
	return &TypedError{
		Type:          ErrorTypeExecutor,
		Message:       msg,
		Cause:         cause,
		PartialOutput: partial,
	}
}

// NewTimeoutError creates a timeout error with optional partial output.
func NewTimeoutError(msg string, partial string) *TypedError {
	return &TypedError{
		Type:          ErrorTypeTimeout,
		Message:       msg,
		PartialOutput: partial,
	}
}

// NewValidationError creates a validation error.
func NewValidationError(msg string) *TypedError {
	return &TypedError{
		Type:    ErrorTypeValidation,
		Message: msg,
	}
}

// NewConfigError creates a configuration error.
func NewConfigError(msg string, cause error) *TypedError {
	return &TypedError{
		Type:    ErrorTypeConfig,
		Message: msg,
		Cause:   cause,
	}
}

// NewNotFoundError creates a not-found error.
func NewNotFoundError(msg string) *TypedError {
	return &TypedError{
		Type:    ErrorTypeNotFound,
		Message: msg,
	}
}

// NewCircuitOpenError creates a circuit breaker open error.
func NewCircuitOpenError(cli string) *TypedError {
	return &TypedError{
		Type:    ErrorTypeCircuitOpen,
		Message: fmt.Sprintf("circuit breaker open for CLI %q — too many recent failures", cli),
	}
}

// IsTypedError checks if an error is a TypedError of the given type.
func IsTypedError(err error, t ErrorType) bool {
	var te *TypedError
	if errors.As(err, &te) {
		return te.Type == t
	}
	return false
}
