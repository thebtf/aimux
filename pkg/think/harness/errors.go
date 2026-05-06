package harness

import (
	"errors"
	"fmt"
)

// Sentinel errors support errors.Is checks while HarnessError carries user-facing recovery guidance.
var (
	ErrUnknownSession = errors.New("unknown session")
	ErrInvalidInput   = errors.New("invalid harness input")
	ErrDuplicateID    = errors.New("duplicate session id")
)

type ErrorCode string

const (
	ErrorCodeUnknownSession ErrorCode = "unknown_session"
	ErrorCodeInvalidInput   ErrorCode = "invalid_input"
	ErrorCodeDuplicateID    ErrorCode = "duplicate_session_id"
)

type HarnessError struct {
	Code     ErrorCode `json:"code"`
	Message  string    `json:"message"`
	NextStep string    `json:"next_step"`
	cause    error
}

func (e *HarnessError) Error() string {
	if e == nil {
		return ""
	}
	if e.NextStep == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s; next: %s", e.Code, e.Message, e.NextStep)
}

func (e *HarnessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func unknownSessionError(id string) error {
	return &HarnessError{
		Code:     ErrorCodeUnknownSession,
		Message:  fmt.Sprintf("thinking session %q was not found", id),
		NextStep: "Start a new thinking session or pass a valid session_id from think(action=start).",
		cause:    ErrUnknownSession,
	}
}

func invalidInputError(message, nextStep string) error {
	return &HarnessError{
		Code:     ErrorCodeInvalidInput,
		Message:  message,
		NextStep: nextStep,
		cause:    ErrInvalidInput,
	}
}

func duplicateSessionError(id string) error {
	return &HarnessError{
		Code:     ErrorCodeDuplicateID,
		Message:  fmt.Sprintf("thinking session %q already exists", id),
		NextStep: "Use a unique session_id or update the existing session.",
		cause:    ErrDuplicateID,
	}
}
