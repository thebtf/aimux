package types_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// TestConstructors verifies that each constructor sets the correct CLIErrorCode.
func TestConstructors(t *testing.T) {
	sentinel := errors.New("sentinel")

	cases := []struct {
		name     string
		err      *types.CLIError
		wantCode types.CLIErrorCode
	}{
		{"NewUnknown", types.NewUnknown("msg", sentinel), types.CLIErrorCodeUnknown},
		{"NewRateLimit", types.NewRateLimit("msg", sentinel), types.CLIErrorCodeRateLimit},
		{"NewAuthExpiry", types.NewAuthExpiry("msg", sentinel), types.CLIErrorCodeAuthExpiry},
		{"NewTimeout", types.NewTimeout("msg", sentinel), types.CLIErrorCodeTimeout},
		{"NewCapabilityMismatch", types.NewCapabilityMismatch("msg", sentinel), types.CLIErrorCodeCapabilityMismatch},
		{"NewUserInputError", types.NewUserInputError("msg", sentinel), types.CLIErrorCodeUserInputError},
		{"NewSandboxDenial", types.NewSandboxDenial("msg", sentinel), types.CLIErrorCodeSandboxDenial},
		{"NewBinaryNotFound", types.NewBinaryNotFound("msg", sentinel), types.CLIErrorCodeBinaryNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Code != tc.wantCode {
				t.Errorf("got code %v, want %v", tc.err.Code, tc.wantCode)
			}
		})
	}
}

// TestErrorFormat verifies the Error() string format.
func TestErrorFormat(t *testing.T) {
	e := types.NewRateLimit("too many requests", nil)
	got := e.Error()
	want := "cli error RateLimit: too many requests"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestUnwrap verifies that Unwrap() returns the wrapped error enabling errors.Is chains.
func TestUnwrap(t *testing.T) {
	sentinel := errors.New("original cause")
	e := types.NewTimeout("deadline exceeded", sentinel)

	if !errors.Is(e, sentinel) {
		t.Error("errors.Is(e, sentinel) = false; want true")
	}

	if unwrapped := e.Unwrap(); unwrapped != sentinel {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, sentinel)
	}
}

// TestUnwrapNil verifies that Unwrap() with nil wrapped returns nil.
func TestUnwrapNil(t *testing.T) {
	e := types.NewUnknown("no cause", nil)
	if e.Unwrap() != nil {
		t.Errorf("Unwrap() = %v, want nil", e.Unwrap())
	}
}

// TestErrorsAs verifies that errors.As works for callers extracting the typed code.
func TestErrorsAs(t *testing.T) {
	e := types.NewAuthExpiry("token expired", nil)
	var target *types.CLIError
	if !errors.As(e, &target) {
		t.Fatal("errors.As returned false; want true")
	}
	if target.Code != types.CLIErrorCodeAuthExpiry {
		t.Errorf("extracted code = %v, want AuthExpiry", target.Code)
	}
}

// TestErrorsAsWrappedInFmt verifies that errors.As traverses fmt.Errorf wrapping.
func TestErrorsAsWrappedInFmt(t *testing.T) {
	inner := types.NewBinaryNotFound("codex not installed", nil)
	wrapped := fmt.Errorf("pool acquire: %w", inner)

	var target *types.CLIError
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As through fmt.Errorf wrapper returned false; want true")
	}
	if target.Code != types.CLIErrorCodeBinaryNotFound {
		t.Errorf("extracted code = %v, want BinaryNotFound", target.Code)
	}
}

// TestCLIErrorCodeString verifies human-readable labels for all code values.
func TestCLIErrorCodeString(t *testing.T) {
	cases := []struct {
		code types.CLIErrorCode
		want string
	}{
		{types.CLIErrorCodeUnknown, "Unknown"},
		{types.CLIErrorCodeRateLimit, "RateLimit"},
		{types.CLIErrorCodeAuthExpiry, "AuthExpiry"},
		{types.CLIErrorCodeTimeout, "Timeout"},
		{types.CLIErrorCodeCapabilityMismatch, "CapabilityMismatch"},
		{types.CLIErrorCodeUserInputError, "UserInputError"},
		{types.CLIErrorCodeSandboxDenial, "SandboxDenial"},
		{types.CLIErrorCodeBinaryNotFound, "BinaryNotFound"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.code.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCLIErrorCodeUnknownDefault verifies that zero-value CLIErrorCode is Unknown.
func TestCLIErrorCodeUnknownDefault(t *testing.T) {
	var code types.CLIErrorCode
	if code != types.CLIErrorCodeUnknown {
		t.Errorf("zero-value CLIErrorCode = %v, want Unknown", code)
	}
}
