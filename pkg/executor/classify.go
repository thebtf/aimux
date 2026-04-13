package executor

import "strings"

// ErrorClass indicates the category of a CLI error and drives retry strategy.
type ErrorClass int

const (
	ErrorClassNone      ErrorClass = iota // success or unrecognised error
	ErrorClassQuota                       // rate limit — model fallback + cooldown
	ErrorClassTransient                   // network — retry same model
	ErrorClassFatal                       // auth/config — skip CLI entirely
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
var fatalPatterns = []string{
	"authentication",
	"invalid api key",
	"model not found",
	"unauthorized",
}

// ClassifyError determines the retry strategy for a CLI error.
// It checks both stdout content and stderr for known error patterns.
// Exit code 0 always returns ErrorClassNone regardless of message content.
// When both quota and transient patterns match, quota takes priority.
func ClassifyError(content, stderr string, exitCode int) ErrorClass {
	if exitCode == 0 {
		return ErrorClassNone
	}

	lowerContent := strings.ToLower(content)
	lowerStderr := strings.ToLower(stderr)

	hasQuota := matchesAny(lowerContent, quotaPatterns) || matchesAny(lowerStderr, quotaPatterns)
	hasTransient := matchesAny(lowerContent, transientPatterns) || matchesAny(lowerStderr, transientPatterns)
	hasFatal := matchesAny(lowerContent, fatalPatterns) || matchesAny(lowerStderr, fatalPatterns)

	// Quota takes priority over transient.
	if hasQuota {
		return ErrorClassQuota
	}
	if hasTransient {
		return ErrorClassTransient
	}
	if hasFatal {
		return ErrorClassFatal
	}

	return ErrorClassNone
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
