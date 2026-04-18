package executor

import "strings"

// ErrorClass indicates the category of a CLI error and drives retry strategy.
type ErrorClass int

// Values are ordered by retry priority — but ClassifyError uses explicit switch,
// not integer comparison. Do not rely on iota order for priority.
const (
	ErrorClassNone             ErrorClass = iota // 0 — success (exit 0)
	ErrorClassQuota                              // 1 — rate-limited, highest retry priority
	ErrorClassTransient                          // 2 — network blip, retry same model
	ErrorClassModelUnavailable                   // 3 — model inaccessible, fall to next model
	ErrorClassFatal                              // 4 — auth/config broken, skip CLI entirely
	ErrorClassUnknown                            // 5 — non-zero exit, no pattern match
)

// quotaPatterns are substrings that indicate a quota or rate-limit error.
var quotaPatterns = []string{
	"usage limit",
	"hit your usage limit",
	"rate limit",
	"429",
	"quota exceeded",
}

// transientPatterns are substrings that indicate a recoverable network error.
var transientPatterns = []string{
	"connection refused",
	"timeout",
	"econnreset",
	"etimedout",
	"enotfound",
	"dns resolution",
}

// fatalPatterns are substrings that indicate a permanent configuration error.
// Note: "access denied to model" is in modelUnavailablePatterns and takes priority
// (checked first), so bare "access denied" here only fires for non-model denials.
var fatalPatterns = []string{
	"authentication",
	"invalid api key",
	"unauthorized",
	"access denied",
}

// modelUnavailablePatterns are substrings that indicate the requested model is not
// accessible to this caller. Unlike Fatal, these should trigger model fallback rather
// than skipping the CLI entirely — another model on the same CLI may still work.
// Note: bare "access denied" or "unauthorized" without a model qualifier stay Fatal.
var modelUnavailablePatterns = []string{
	"model not found",
	"not available for your account",
	"not authorized for model",
	"not authorized for this model",
	"model not enabled",
	"access denied to model",
	"model not available",
	"this model is not available",
	"you do not have access to model",
	"you don't have access to this model",
}

// ClassifyError determines the retry strategy for a CLI error.
// It checks both stdout content and stderr for known error patterns.
// Exit code 0 always returns ErrorClassNone regardless of message content.
//
// Priority order (highest to lowest):
//  1. Quota — rate-limited; retrying would waste a request
//  2. ModelUnavailable — this model is inaccessible, try next model on same CLI
//  3. Transient — network blip; retry same model once
//  4. Fatal — auth/config error; skip CLI entirely
//  5. Unknown — non-zero exit with unrecognised message
func ClassifyError(content, stderr string, exitCode int) ErrorClass {
	if exitCode == 0 {
		return ErrorClassNone
	}

	lowerContent := strings.ToLower(content)
	lowerStderr := strings.ToLower(stderr)

	hasQuota := matchesAny(lowerContent, quotaPatterns) || matchesAny(lowerStderr, quotaPatterns)
	if hasQuota {
		return ErrorClassQuota
	}

	hasModelUnavailable := matchesAny(lowerContent, modelUnavailablePatterns) || matchesAny(lowerStderr, modelUnavailablePatterns)
	if hasModelUnavailable {
		return ErrorClassModelUnavailable
	}

	hasTransient := matchesAny(lowerContent, transientPatterns) || matchesAny(lowerStderr, transientPatterns)
	if hasTransient {
		return ErrorClassTransient
	}

	hasFatal := matchesAny(lowerContent, fatalPatterns) || matchesAny(lowerStderr, fatalPatterns)
	if hasFatal {
		return ErrorClassFatal
	}

	return ErrorClassUnknown
}

// matchesAny reports whether s contains any of the given substrings.
// s must already be lowercased; substrings are assumed lowercase.
func matchesAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// ReplaceModelFlag swaps or appends the model flag and its value in an args slice.
// When the flag is already present its value is replaced; when absent the flag+value is appended.
// Returns args unchanged when modelFlag is empty.
func ReplaceModelFlag(args []string, modelFlag, newModel string) []string {
	if modelFlag == "" {
		return args
	}
	result := make([]string, 0, len(args)+2)
	replaced := false
	eqPrefix := modelFlag + "="
	for i := 0; i < len(args); i++ {
		if args[i] == modelFlag && i+1 < len(args) {
			// Space-separated form: --model value
			result = append(result, modelFlag, newModel)
			i++ // skip old model value
			replaced = true
		} else if strings.HasPrefix(args[i], eqPrefix) {
			// Equals form: --model=value
			result = append(result, eqPrefix+newModel)
			replaced = true
		} else {
			result = append(result, args[i])
		}
	}
	if !replaced {
		result = append(result, modelFlag, newModel)
	}
	return result
}
