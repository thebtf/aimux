package codex

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// TestMapToCliError verifies the FR-15 mapping table with one case per CLIErrorCode.
func TestMapToCliError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode types.CLIErrorCode
	}{
		{
			name:     "nil returns nil",
			err:      nil,
			wantCode: -1, // sentinel: result must be nil
		},
		{
			name:     "BinaryNotFound via exec.ErrNotFound",
			err:      fmt.Errorf("spawn codex: %w", exec.ErrNotFound),
			wantCode: types.CLIErrorCodeBinaryNotFound,
		},
		{
			name:     "BinaryNotFound via message",
			err:      errors.New("start process: executable file not found in $PATH"),
			wantCode: types.CLIErrorCodeBinaryNotFound,
		},
		{
			name:     "Timeout via context.DeadlineExceeded",
			err:      context.DeadlineExceeded,
			wantCode: types.CLIErrorCodeTimeout,
		},
		{
			name:     "Timeout via wrapped DeadlineExceeded",
			err:      fmt.Errorf("turn start: %w", context.DeadlineExceeded),
			wantCode: types.CLIErrorCodeTimeout,
		},
		{
			name:     "Timeout via message",
			err:      errors.New("request timeout after 30s"),
			wantCode: types.CLIErrorCodeTimeout,
		},
		{
			name:     "RateLimit via 429 text",
			err:      errors.New("HTTP 429: too many requests"),
			wantCode: types.CLIErrorCodeRateLimit,
		},
		{
			name:     "RateLimit via rate limit text",
			err:      errors.New("error: rate limit exceeded"),
			wantCode: types.CLIErrorCodeRateLimit,
		},
		{
			name:     "RateLimit via quota text",
			err:      errors.New("quota exhausted for project"),
			wantCode: types.CLIErrorCodeRateLimit,
		},
		{
			name:     "AuthExpiry via auth failed",
			err:      errors.New("codex auth failed: token invalid"),
			wantCode: types.CLIErrorCodeAuthExpiry,
		},
		{
			name:     "AuthExpiry via auth expiry",
			err:      errors.New("auth token expired"),
			wantCode: types.CLIErrorCodeAuthExpiry,
		},
		{
			name:     "AuthExpiry via 401",
			err:      errors.New("HTTP 401 unauthorized"),
			wantCode: types.CLIErrorCodeAuthExpiry,
		},
		{
			name:     "AuthExpiry via unauthorised",
			err:      errors.New("request unauthorized"),
			wantCode: types.CLIErrorCodeAuthExpiry,
		},
		{
			name:     "SandboxDenial via patch rejected",
			err:      errors.New("operation failed: patch rejected by sandbox"),
			wantCode: types.CLIErrorCodeSandboxDenial,
		},
		{
			name:     "SandboxDenial via sandbox block",
			err:      errors.New("sandbox block: write not permitted"),
			wantCode: types.CLIErrorCodeSandboxDenial,
		},
		{
			name:     "CapabilityMismatch via -32601",
			err:      errors.New("rpc error -32601: method not found"),
			wantCode: types.CLIErrorCodeCapabilityMismatch,
		},
		{
			name:     "CapabilityMismatch via unsupported",
			err:      errors.New("unsupported operation for this model"),
			wantCode: types.CLIErrorCodeCapabilityMismatch,
		},
		{
			name:     "UserInputError via invalid prompt",
			err:      errors.New("invalid prompt: exceeds max length"),
			wantCode: types.CLIErrorCodeUserInputError,
		},
		{
			name:     "UserInputError via validation",
			err:      errors.New("validation failed: missing required field"),
			wantCode: types.CLIErrorCodeUserInputError,
		},
		{
			name:     "Unknown for unrecognised error",
			err:      errors.New("some unexpected internal crash"),
			wantCode: types.CLIErrorCodeUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := mapToCliError(tc.err)

			// nil input must produce nil output
			if tc.wantCode == -1 {
				if result != nil {
					t.Errorf("mapToCliError(nil) = %v, want nil", result)
				}
				return
			}

			if result == nil {
				t.Fatalf("mapToCliError returned nil for non-nil error")
			}
			if result.Code != tc.wantCode {
				t.Errorf("Code = %v, want %v", result.Code, tc.wantCode)
			}
		})
	}
}

// TestMapToCliError_PassThrough verifies that an already-typed *CLIError is not double-wrapped.
func TestMapToCliError_PassThrough(t *testing.T) {
	original := types.NewRateLimit("already typed", nil)
	result := mapToCliError(original)
	if result != original {
		t.Errorf("expected pass-through of existing *CLIError, got different pointer")
	}
}

// TestMapToCliError_PassThroughWrapped verifies pass-through when CLIError is wrapped in fmt.Errorf.
func TestMapToCliError_PassThroughWrapped(t *testing.T) {
	original := types.NewTimeout("timed out", nil)
	wrapped := fmt.Errorf("worker execute: %w", original)
	result := mapToCliError(wrapped)
	if result != original {
		t.Errorf("expected pass-through to original *CLIError, got different instance")
	}
}

// TestMapToCliError_ErrorsAs verifies that callers can use errors.As on the result.
func TestMapToCliError_ErrorsAs(t *testing.T) {
	result := mapToCliError(errors.New("HTTP 429: quota exceeded"))
	if result == nil {
		t.Fatal("unexpected nil result")
	}
	var target *types.CLIError
	if !errors.As(result, &target) {
		t.Fatal("errors.As returned false; want true")
	}
	if target.Code != types.CLIErrorCodeRateLimit {
		t.Errorf("Code = %v, want RateLimit", target.Code)
	}
}

// TestMapToCliError_WrappedErrorPreserved verifies that Wrapped is set to the original error.
func TestMapToCliError_WrappedErrorPreserved(t *testing.T) {
	original := errors.New("original cause")
	result := mapToCliError(original)
	if result == nil {
		t.Fatal("unexpected nil result")
	}
	if !errors.Is(result, original) {
		t.Error("errors.Is(result, original) = false; Wrapped error not preserved")
	}
}
