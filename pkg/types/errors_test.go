package types_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

func TestTypedError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  *types.TypedError
		want string
	}{
		{
			name: "without cause",
			err:  types.NewValidationError("bad input"),
			want: "ValidationError: bad input",
		},
		{
			name: "with cause",
			err:  types.NewConfigError("parse failed", fmt.Errorf("invalid yaml")),
			want: "ConfigError: parse failed (caused by: invalid yaml)",
		},
		{
			name: "with partial output",
			err:  types.NewTimeoutError("timed out after 60s", "partial data here"),
			want: "TimeoutError: timed out after 60s",
		},
		{
			name: "circuit open",
			err:  types.NewCircuitOpenError("codex"),
			want: `CircuitOpenError: circuit breaker open for CLI "codex" — too many recent failures`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTypedError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("root cause")
	err := types.NewExecutorError("exec failed", cause, "")

	unwrapped := errors.Unwrap(err)
	if unwrapped != cause {
		t.Errorf("Unwrap got %v, want %v", unwrapped, cause)
	}
}

func TestIsTypedError(t *testing.T) {
	err := types.NewTimeoutError("timed out", "partial")
	wrapped := fmt.Errorf("wrapper: %w", err)

	if !types.IsTypedError(wrapped, types.ErrorTypeTimeout) {
		t.Error("expected IsTypedError to return true for wrapped TimeoutError")
	}

	if types.IsTypedError(wrapped, types.ErrorTypeExecutor) {
		t.Error("expected IsTypedError to return false for wrong type")
	}

	if types.IsTypedError(fmt.Errorf("plain"), types.ErrorTypeTimeout) {
		t.Error("expected IsTypedError to return false for non-TypedError")
	}
}

func TestNewExecutorError_PartialOutput(t *testing.T) {
	err := types.NewExecutorError("crash", nil, "partial content")
	if err.PartialOutput != "partial content" {
		t.Errorf("expected partial output %q, got %q", "partial content", err.PartialOutput)
	}
}
