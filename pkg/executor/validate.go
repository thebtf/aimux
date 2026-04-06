package executor

import (
	"fmt"
	"strings"
)

// TurnValidation holds the result of post-execution content quality checks.
type TurnValidation struct {
	Valid    bool     `json:"valid"`
	Warnings []string `json:"warnings,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

// ValidateTurnContent performs post-execution content quality checks on CLI output.
// It checks for empty output, stderr error patterns, short content, refusal patterns,
// and non-zero exit codes. Multiple warnings can accumulate; first error short-circuits to invalid.
func ValidateTurnContent(content, stderr string, exitCode int) TurnValidation {
	trimmed := strings.TrimSpace(content)

	// 1. Empty output with exit code 0.
	if exitCode == 0 && trimmed == "" {
		return TurnValidation{
			Valid:  false,
			Errors: []string{"empty output from CLI"},
		}
	}

	// 2. Non-zero exit without content.
	if exitCode != 0 && trimmed == "" {
		return TurnValidation{
			Valid:  false,
			Errors: []string{fmt.Sprintf("CLI exited with code %d", exitCode)},
		}
	}

	// 3. Stderr error patterns (case-insensitive).
	if err := checkStderrPatterns(stderr); err != "" {
		return TurnValidation{
			Valid:  false,
			Errors: []string{err},
		}
	}

	// Accumulate warnings for remaining checks.
	var warnings []string

	// 4. Short content.
	if len(trimmed) < 10 {
		warnings = append(warnings, fmt.Sprintf("very short response (%d chars)", len(trimmed)))
	}

	// 5. Refusal detection (first 200 chars, lowercased).
	if refusal := detectRefusal(trimmed); refusal != "" {
		warnings = append(warnings, refusal)
	}

	// 6. Non-zero exit with content.
	if exitCode != 0 {
		warnings = append(warnings, fmt.Sprintf("CLI exited with non-zero code %d but produced output", exitCode))
	}

	return TurnValidation{
		Valid:    true,
		Warnings: warnings,
	}
}

// checkStderrPatterns inspects stderr for known error patterns (case-insensitive).
// Returns the error description if found, or empty string otherwise.
func checkStderrPatterns(stderr string) string {
	if stderr == "" {
		return ""
	}

	lower := strings.ToLower(stderr)

	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "rate_limit") {
		return "rate limit exceeded"
	}
	if strings.Contains(lower, "quota exceeded") || strings.Contains(lower, "quota_exceeded") {
		return "quota exceeded"
	}
	if strings.Contains(lower, "429") {
		return "HTTP 429 rate limit"
	}
	if strings.Contains(lower, "authentication") && (strings.Contains(lower, "failed") || strings.Contains(lower, "error")) {
		return "authentication failure"
	}
	if strings.Contains(lower, "econnrefused") {
		return "connection refused"
	}
	if strings.Contains(lower, "etimedout") {
		return "connection timeout"
	}
	if strings.Contains(lower, "enotfound") {
		return "DNS resolution failed"
	}
	if strings.Contains(lower, "connection refused") {
		return "connection refused"
	}

	return ""
}

// detectRefusal checks the first 200 characters of content for refusal patterns.
// Returns a warning string if a refusal is detected, or empty string otherwise.
func detectRefusal(content string) string {
	snippet := content
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	lower := strings.ToLower(snippet)

	refusalPatterns := []string{
		"i cannot",
		"i can't",
		"i am unable",
		"i don't have access",
	}

	for _, p := range refusalPatterns {
		if strings.Contains(lower, p) {
			return "possible refusal detected"
		}
	}

	return ""
}
