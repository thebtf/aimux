package types_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/thebtf/aimux/loom/clierror"
	"github.com/thebtf/aimux/pkg/executor/types"
)

func TestConstructorsCoverAllCodes(t *testing.T) {
	sentinel := errors.New("sentinel")
	cases := []struct {
		name      string
		err       *types.CLIError
		wantCode  types.CLIErrorCode
		retryable bool
	}{
		{"NewUnknown", types.NewUnknown("msg", sentinel), types.CLIErrorCodeUnknown, false},
		{"NewRateLimit", types.NewRateLimit("msg", sentinel), types.CLIErrorCodeRateLimit, true},
		{"NewAuthExpiry", types.NewAuthExpiry("msg", sentinel), types.CLIErrorCodeAuthExpiry, true},
		{"NewTimeout", types.NewTimeout("msg", sentinel), types.CLIErrorCodeTimeout, true},
		{"NewCapabilityMismatch", types.NewCapabilityMismatch("msg", sentinel), types.CLIErrorCodeCapabilityMismatch, true},
		{"NewUserInputError", types.NewUserInputError("msg", sentinel), types.CLIErrorCodeUserInputError, false},
		{"NewSandboxDenial", types.NewSandboxDenial("msg", sentinel), types.CLIErrorCodeSandboxDenial, false},
		{"NewBinaryNotFound", types.NewBinaryNotFound("msg", sentinel), types.CLIErrorCodeBinaryNotFound, false},
		{"NewCanceled", types.NewCanceled("msg", sentinel), types.CLIErrorCodeCanceled, false},
		{"NewResumeWorkerMismatch", types.NewResumeWorkerMismatch("msg", sentinel), types.CLIErrorCodeResumeWorkerMismatch, false},
		{"NewClassificationAmbiguous", types.NewClassificationAmbiguous("msg", sentinel), types.CLIErrorCodeClassificationAmbiguous, false},
	}

	if len(cases) != 11 {
		t.Fatalf("constructor coverage = %d, want 11", len(cases))
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Code != tc.wantCode {
				t.Fatalf("Code = %v, want %v", tc.err.Code, tc.wantCode)
			}
			if tc.err.Message != "msg" {
				t.Fatalf("Message = %q, want msg", tc.err.Message)
			}
			if tc.err.Cause != sentinel {
				t.Fatalf("Cause = %v, want sentinel", tc.err.Cause)
			}
			if tc.err.CauseStr != sentinel.Error() {
				t.Fatalf("CauseStr = %q, want %q", tc.err.CauseStr, sentinel.Error())
			}
			if tc.err.Retryable != tc.retryable {
				t.Fatalf("Retryable = %v, want %v", tc.err.Retryable, tc.retryable)
			}
		})
	}
}

func TestJSONRoundTripPreservesWireFields(t *testing.T) {
	original := types.NewTimeout("deadline exceeded", errors.New("context deadline exceeded")).WithCLI("codex")
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("json.Marshal produced invalid JSON: %s", raw)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("json.Unmarshal wire map: %v", err)
	}
	if _, ok := wire["cause"]; !ok {
		t.Fatalf("wire JSON missing cause field: %s", raw)
	}
	if _, ok := wire["cause_str"]; ok {
		t.Fatalf("wire JSON must use cause, not cause_str: %s", raw)
	}
	if _, ok := wire["Cause"]; ok {
		t.Fatalf("wire JSON must not include internal Cause field: %s", raw)
	}

	var roundTrip types.CLIError
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if roundTrip.Code != original.Code {
		t.Fatalf("Code = %v, want %v", roundTrip.Code, original.Code)
	}
	if roundTrip.Message != original.Message {
		t.Fatalf("Message = %q, want %q", roundTrip.Message, original.Message)
	}
	if roundTrip.CauseStr != original.CauseStr {
		t.Fatalf("CauseStr = %q, want %q", roundTrip.CauseStr, original.CauseStr)
	}
	if roundTrip.CLI != original.CLI {
		t.Fatalf("CLI = %q, want %q", roundTrip.CLI, original.CLI)
	}
	if roundTrip.Retryable != original.Retryable {
		t.Fatalf("Retryable = %v, want %v", roundTrip.Retryable, original.Retryable)
	}
	if roundTrip.Cause != nil {
		t.Fatalf("Cause after JSON = %v, want nil", roundTrip.Cause)
	}
}

func TestErrorFormat(t *testing.T) {
	err := types.NewRateLimit("too many requests", nil)
	got := err.Error()
	want := "cli error RateLimit: too many requests"
	if got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestUnwrap(t *testing.T) {
	sentinel := errors.New("original cause")
	err := types.NewTimeout("deadline exceeded", sentinel)
	if !errors.Is(err, sentinel) {
		t.Fatal("errors.Is(err, sentinel) = false, want true")
	}
	if err.Unwrap() != sentinel {
		t.Fatalf("Unwrap() = %v, want sentinel", err.Unwrap())
	}
}

func TestErrorsAsThroughFmtWrap(t *testing.T) {
	inner := types.NewBinaryNotFound("codex not installed", nil)
	wrapped := fmt.Errorf("pool acquire: %w", inner)
	var target *types.CLIError
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As through fmt.Errorf wrapper returned false")
	}
	if target.Code != types.CLIErrorCodeBinaryNotFound {
		t.Fatalf("extracted code = %v, want BinaryNotFound", target.Code)
	}
}

func TestCLIErrorAliasAcceptsLoomCLIError(t *testing.T) {
	var err error = clierror.NewCapabilityMismatch("subtask ProjectID must match parent ProjectID", nil)
	var target *types.CLIError
	if !errors.As(err, &target) {
		t.Fatal("errors.As into *types.CLIError returned false")
	}
	if target.Code != types.CLIErrorCodeCapabilityMismatch {
		t.Fatalf("Code = %v, want CapabilityMismatch", target.Code)
	}
}

func TestCLIErrorCodeStringCoversAllCodes(t *testing.T) {
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
		{types.CLIErrorCodeCanceled, "Canceled"},
		{types.CLIErrorCodeResumeWorkerMismatch, "ResumeWorkerMismatch"},
		{types.CLIErrorCodeClassificationAmbiguous, "ClassificationAmbiguous"},
	}

	if len(cases) != 11 {
		t.Fatalf("string coverage = %d, want 11", len(cases))
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.code.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCLIErrorCodeUnknownDefault(t *testing.T) {
	var code types.CLIErrorCode
	if code != types.CLIErrorCodeUnknown {
		t.Fatalf("zero-value CLIErrorCode = %v, want Unknown", code)
	}
	if types.RetryableByCode(code) {
		t.Fatal("Unknown must not be retryable")
	}
}
