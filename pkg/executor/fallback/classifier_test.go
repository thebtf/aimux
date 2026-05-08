package fallback

import (
	"errors"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/types"
)

func TestFailureClassifier_AllCodes(t *testing.T) {
	c := NewFailureClassifier()

	cases := []struct {
		code     types.CLIErrorCode
		want     Eligibility
		codeName string
	}{
		{types.CLIErrorCodeRateLimit, Eligible, "RateLimit"},
		{types.CLIErrorCodeAuthExpiry, Eligible, "AuthExpiry"},
		{types.CLIErrorCodeTimeout, Eligible, "Timeout"},
		{types.CLIErrorCodeCapabilityMismatch, Eligible, "CapabilityMismatch"},
		{types.CLIErrorCodeUserInputError, Terminal, "UserInputError"},
		{types.CLIErrorCodeSandboxDenial, Terminal, "SandboxDenial"},
		{types.CLIErrorCodeBinaryNotFound, Terminal, "BinaryNotFound"},
		{types.CLIErrorCodeCanceled, Terminal, "Canceled"},
		{types.CLIErrorCodeResumeWorkerMismatch, Terminal, "ResumeWorkerMismatch"},
		{types.CLIErrorCodeClassificationAmbiguous, Terminal, "ClassificationAmbiguous"},
		{types.CLIErrorCodeUnknown, Terminal, "Unknown"},
	}

	if len(cases) != 11 {
		t.Fatalf("classifier coverage = %d, want 11", len(cases))
	}

	for _, tc := range cases {
		t.Run(tc.codeName, func(t *testing.T) {
			err := &types.CLIError{Code: tc.code, Message: "test"}
			got := c.Classify(err)
			if got != tc.want {
				wantStr := map[Eligibility]string{Eligible: "Eligible", Terminal: "Terminal"}
				t.Errorf("Classify(%s) = %s, want %s", tc.codeName, wantStr[got], wantStr[tc.want])
			}
		})
	}
}

func TestFailureClassifier_NonCLIError_IsTerminal(t *testing.T) {
	c := NewFailureClassifier()
	got := c.Classify(errors.New("plain error"))
	if got != Terminal {
		t.Errorf("non-CLIError: got %v, want Terminal", got)
	}
}

func TestFailureClassifier_WrappedCLIError(t *testing.T) {
	c := NewFailureClassifier()
	inner := &types.CLIError{Code: types.CLIErrorCodeRateLimit, Message: "rate limited"}
	wrapped := errors.Join(errors.New("outer"), inner)
	got := c.Classify(wrapped)
	if got != Eligible {
		t.Errorf("wrapped CLIError(RateLimit): got %v, want Eligible", got)
	}
}

// TestFailureClassifier_FutureCode verifies the default branch is Terminal.
func TestFailureClassifier_FutureCode(t *testing.T) {
	got := classifyCode(types.CLIErrorCode(999))
	if got != Terminal {
		t.Errorf("unknown future code: got %v, want Terminal", got)
	}
}
