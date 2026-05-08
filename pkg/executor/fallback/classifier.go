package fallback

import (
	"errors"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// Eligibility is the result of classifying a CLIError for fallback purposes.
type Eligibility int

const (
	// Eligible means the failure is transient and another CLI may succeed.
	// Fallback should be attempted.
	Eligible Eligibility = iota
	// Terminal means the failure is permanent or user-caused.
	// Surfacing the error immediately is correct; retrying will not help.
	Terminal
)

// FailureClassifier classifies a *types.CLIError as Eligible or Terminal (spec FR-2, ADR-002).
// Classification is based exclusively on the typed CLIErrorCode enum — no string matching.
//
// Eligible codes: RateLimit, AuthExpiry, Timeout, CapabilityMismatch.
// Terminal codes: UserInputError, SandboxDenial, BinaryNotFound, Canceled,
// ResumeWorkerMismatch, ClassificationAmbiguous, Unknown.
// Unknown code → Terminal (safer default per architecture.md §6).
type FailureClassifier struct{}

// NewFailureClassifier constructs a FailureClassifier.
func NewFailureClassifier() *FailureClassifier {
	return &FailureClassifier{}
}

// Classify examines err and returns Eligible if the error code warrants a fallback attempt.
// If err is not a *types.CLIError, it is treated as Unknown → Terminal.
func (c *FailureClassifier) Classify(err error) Eligibility {
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		// Non-typed error — treat as unknown, which is terminal (safe default).
		return Terminal
	}
	return classifyCode(cliErr.Code)
}

// classifyCode maps CLIErrorCode to Eligibility.
// This is the single authoritative table per architecture.md §6.
func classifyCode(code types.CLIErrorCode) Eligibility {
	switch code {
	// --- Eligible: transient per-CLI issues ---
	case types.CLIErrorCodeRateLimit:
		// HTTP 429 / quota exhaustion — another CLI may be under its limit.
		return Eligible
	case types.CLIErrorCodeAuthExpiry:
		// Auth token expired for this CLI — another CLI may have a valid token.
		return Eligible
	case types.CLIErrorCodeTimeout:
		// Deadline exceeded — another CLI may respond faster.
		return Eligible
	case types.CLIErrorCodeCapabilityMismatch:
		// CLI reported unsupported method (-32601) — another CLI may support it.
		return Eligible

	// --- Terminal: user-caused or infrastructure errors ---
	case types.CLIErrorCodeUserInputError:
		// Invalid prompt or param validation failure — same prompt fails everywhere.
		return Terminal
	case types.CLIErrorCodeSandboxDenial:
		// Sandbox policy block — this is a user-facing policy decision, not a CLI health issue.
		return Terminal
	case types.CLIErrorCodeBinaryNotFound:
		// Binary not on PATH — infrastructure error; surfaces as health error, not fallback.
		return Terminal
	case types.CLIErrorCodeCanceled:
		// Deliberate cancellation — no retry or fallback (spec FR-2 note).
		return Terminal
	case types.CLIErrorCodeResumeWorkerMismatch:
		// Resume continuation is bound to the original worker/project context.
		return Terminal
	case types.CLIErrorCodeClassificationAmbiguous:
		// Caller must pick an explicit task_class; another CLI cannot resolve it.
		return Terminal
	case types.CLIErrorCodeUnknown:
		// Unknown error code — terminal is the safer default (architecture.md §6).
		return Terminal

	default:
		// Future enum additions default to Terminal until explicitly classified.
		return Terminal
	}
}
