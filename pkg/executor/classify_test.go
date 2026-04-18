package executor_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
)

// TestClassifyError_QuotaPatterns verifies that usage-limit and rate-limit messages
// in either stdout or stderr are classified as ErrorClassQuota.
func TestClassifyError_UsageLimitInContent(t *testing.T) {
	got := executor.ClassifyError("Error: you have hit your usage limit for this period", "", 1)
	if got != executor.ErrorClassQuota {
		t.Fatalf("expected ErrorClassQuota, got %v", got)
	}
}

func TestClassifyError_RateLimitInContent(t *testing.T) {
	got := executor.ClassifyError("rate limit exceeded, please wait", "", 1)
	if got != executor.ErrorClassQuota {
		t.Fatalf("expected ErrorClassQuota, got %v", got)
	}
}

func TestClassifyError_429InStderr(t *testing.T) {
	got := executor.ClassifyError("", "HTTP 429 Too Many Requests", 1)
	if got != executor.ErrorClassQuota {
		t.Fatalf("expected ErrorClassQuota, got %v", got)
	}
}

func TestClassifyError_QuotaExceededInStderr(t *testing.T) {
	got := executor.ClassifyError("", "quota exceeded: monthly cap reached", 1)
	if got != executor.ErrorClassQuota {
		t.Fatalf("expected ErrorClassQuota, got %v", got)
	}
}

// TestClassifyError_TransientPatterns verifies that network errors are classified
// as ErrorClassTransient.
func TestClassifyError_ConnectionRefusedInContent(t *testing.T) {
	got := executor.ClassifyError("connection refused to api.example.com:443", "", 1)
	if got != executor.ErrorClassTransient {
		t.Fatalf("expected ErrorClassTransient, got %v", got)
	}
}

func TestClassifyError_TimeoutInStderr(t *testing.T) {
	got := executor.ClassifyError("", "request timeout after 30s", 1)
	if got != executor.ErrorClassTransient {
		t.Fatalf("expected ErrorClassTransient, got %v", got)
	}
}

func TestClassifyError_ETIMEDOUTInStderr(t *testing.T) {
	got := executor.ClassifyError("", "ETIMEDOUT: connect ETIMEDOUT 1.2.3.4:443", 1)
	if got != executor.ErrorClassTransient {
		t.Fatalf("expected ErrorClassTransient, got %v", got)
	}
}

func TestClassifyError_DNSResolutionInContent(t *testing.T) {
	got := executor.ClassifyError("DNS resolution failed for api.example.com", "", 1)
	if got != executor.ErrorClassTransient {
		t.Fatalf("expected ErrorClassTransient, got %v", got)
	}
}

// TestClassifyError_FatalPatterns verifies that auth and configuration errors
// are classified as ErrorClassFatal.
func TestClassifyError_AuthenticationInStderr(t *testing.T) {
	got := executor.ClassifyError("", "authentication failed: bad credentials", 1)
	if got != executor.ErrorClassFatal {
		t.Fatalf("expected ErrorClassFatal, got %v", got)
	}
}

func TestClassifyError_InvalidAPIKeyInContent(t *testing.T) {
	got := executor.ClassifyError("Error: invalid api key provided", "", 1)
	if got != executor.ErrorClassFatal {
		t.Fatalf("expected ErrorClassFatal, got %v", got)
	}
}

func TestClassifyError_ModelNotFoundInStderr(t *testing.T) {
	got := executor.ClassifyError("", "model not found: gpt-99-turbo", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ErrorClassModelUnavailable, got %v", got)
	}
}

// TestClassifyError_None verifies that clean output with exit code 0 is not an error.
func TestClassifyError_NormalOutputExitZero(t *testing.T) {
	got := executor.ClassifyError("Here is the answer to your question.", "", 0)
	if got != executor.ErrorClassNone {
		t.Fatalf("expected ErrorClassNone, got %v", got)
	}
}

// TestClassifyError_ExitZeroOverridesRateLimit verifies that exit code 0 always
// wins, even when the output text contains rate-limit language.
func TestClassifyError_ExitZeroWithRateLimitText(t *testing.T) {
	got := executor.ClassifyError("You have hit your usage limit", "rate limit info logged", 0)
	if got != executor.ErrorClassNone {
		t.Fatalf("expected ErrorClassNone (exit 0 overrides content), got %v", got)
	}
}

// TestClassifyError_QuotaWinsOverTransient verifies that when both quota and
// transient patterns appear (across content and stderr), quota takes priority.
func TestClassifyError_QuotaInContentTransientInStderr(t *testing.T) {
	got := executor.ClassifyError("rate limit reached", "ETIMEDOUT connecting to API", 1)
	if got != executor.ErrorClassQuota {
		t.Fatalf("expected ErrorClassQuota to win over ErrorClassTransient, got %v", got)
	}
}

// --- FR-9: ErrorClassModelUnavailable pattern tests ---

func TestClassifyError_ModelUnavailable_ModelNotFound(t *testing.T) {
	got := executor.ClassifyError("Error: model not found: gpt-5.3-codex-spark", "", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ErrorClassModelUnavailable, got %v", got)
	}
}

func TestClassifyError_ModelUnavailable_NotAvailableForAccount(t *testing.T) {
	got := executor.ClassifyError("", "This model is not available for your account.", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ErrorClassModelUnavailable, got %v", got)
	}
}

func TestClassifyError_ModelUnavailable_NotAuthorizedForModel(t *testing.T) {
	got := executor.ClassifyError("You are not authorized for model gpt-5.3-codex-spark", "", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ErrorClassModelUnavailable, got %v", got)
	}
}

func TestClassifyError_ModelUnavailable_ModelNotEnabled(t *testing.T) {
	got := executor.ClassifyError("", "model not enabled for this workspace", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ErrorClassModelUnavailable, got %v", got)
	}
}

func TestClassifyError_ModelUnavailable_AccessDeniedToModel(t *testing.T) {
	got := executor.ClassifyError("access denied to model gpt-5.3-codex-spark", "", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ErrorClassModelUnavailable, got %v", got)
	}
}

func TestClassifyError_ModelUnavailable_AccessDeniedToThisModel(t *testing.T) {
	got := executor.ClassifyError("access denied to this model on your plan", "", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ErrorClassModelUnavailable, got %v", got)
	}
}

func TestClassifyError_ModelUnavailable_YouDoNotHaveAccessToThisModel(t *testing.T) {
	got := executor.ClassifyError("", "you do not have access to this model", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ErrorClassModelUnavailable, got %v", got)
	}
}

func TestClassifyError_ModelUnavailable_YouDontHaveAccessToModel(t *testing.T) {
	got := executor.ClassifyError("", "you don't have access to model gpt-5.3-codex-spark", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ErrorClassModelUnavailable, got %v", got)
	}
}

// TestClassifyError_Fatal_BareAccessDenied is a regression test: bare "access denied"
// without the "to model" qualifier must remain Fatal (auth/permission problem, not model-level).
func TestClassifyError_Fatal_BareAccessDenied(t *testing.T) {
	got := executor.ClassifyError("", "access denied", 1)
	if got != executor.ErrorClassFatal {
		t.Fatalf("expected ErrorClassFatal, got %v", got)
	}
}

// TestClassifyError_Fatal_BareUnauthorized is a regression test: bare "unauthorized"
// without a "for model" qualifier must remain Fatal (credential problem, not model-level).
func TestClassifyError_Fatal_BareUnauthorized(t *testing.T) {
	got := executor.ClassifyError("", "unauthorized", 1)
	if got != executor.ErrorClassFatal {
		t.Fatalf("expected ErrorClassFatal, got %v", got)
	}
}

// TestClassifyError_QuotaWinsOverModelUnavailable verifies the priority ordering:
// Quota > ModelUnavailable when both patterns appear in the same message.
func TestClassifyError_QuotaWinsOverModelUnavailable(t *testing.T) {
	got := executor.ClassifyError("rate limit exceeded; model not found", "", 1)
	if got != executor.ErrorClassQuota {
		t.Fatalf("expected ErrorClassQuota to win over ErrorClassModelUnavailable, got %v", got)
	}
}

// --- Q5b: edge-case and uppercase tests ---

func TestClassifyError_EmptyInputs_NonZeroExit(t *testing.T) {
	got := executor.ClassifyError("", "", 1)
	if got != executor.ErrorClassUnknown {
		t.Fatalf("expected ErrorClassUnknown for empty non-zero, got %v", got)
	}
}

func TestClassifyError_EmptyInputs_ZeroExit(t *testing.T) {
	got := executor.ClassifyError("", "", 0)
	if got != executor.ErrorClassNone {
		t.Fatalf("expected ErrorClassNone for empty zero, got %v", got)
	}
}

func TestClassifyError_Uppercase_ModelUnavailable(t *testing.T) {
	got := executor.ClassifyError("ERROR: MODEL NOT FOUND", "", 1)
	if got != executor.ErrorClassModelUnavailable {
		t.Fatalf("expected ModelUnavailable for uppercase input, got %v", got)
	}
}

// TestClassifyError_QuotaInContent_ModelUnavailableInStderr verifies that Quota
// beats ModelUnavailable even when they appear in different fields (content vs stderr).
func TestClassifyError_QuotaInContent_ModelUnavailableInStderr(t *testing.T) {
	got := executor.ClassifyError("rate limit exceeded", "model not found", 1)
	if got != executor.ErrorClassQuota {
		t.Fatalf("expected Quota (cross-field priority), got %v", got)
	}
}
