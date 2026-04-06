package executor

import (
	"testing"
)

func TestValidateTurnContent_ValidOutput(t *testing.T) {
	result := ValidateTurnContent("Hello, this is a valid response from the CLI.", "", 0)
	if !result.Valid {
		t.Fatal("expected valid")
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", result.Warnings)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %v", result.Errors)
	}
}

func TestValidateTurnContent_EmptyContentExitZero(t *testing.T) {
	result := ValidateTurnContent("", "", 0)
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if len(result.Errors) != 1 || result.Errors[0] != "empty output from CLI" {
		t.Fatalf("expected 'empty output from CLI' error, got %v", result.Errors)
	}
}

func TestValidateTurnContent_WhitespaceOnlyExitZero(t *testing.T) {
	result := ValidateTurnContent("   \n\t  ", "", 0)
	if result.Valid {
		t.Fatal("expected invalid for whitespace-only content")
	}
	if len(result.Errors) != 1 || result.Errors[0] != "empty output from CLI" {
		t.Fatalf("expected 'empty output from CLI' error, got %v", result.Errors)
	}
}

func TestValidateTurnContent_EmptyContentNonZeroExit(t *testing.T) {
	result := ValidateTurnContent("", "", 1)
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if len(result.Errors) != 1 || result.Errors[0] != "CLI exited with code 1" {
		t.Fatalf("expected 'CLI exited with code 1' error, got %v", result.Errors)
	}
}

func TestValidateTurnContent_RateLimitInStderr(t *testing.T) {
	result := ValidateTurnContent("some output", "Error: Rate Limit reached", 0)
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if len(result.Errors) != 1 || result.Errors[0] != "rate limit exceeded" {
		t.Fatalf("expected 'rate limit exceeded' error, got %v", result.Errors)
	}
}

func TestValidateTurnContent_429InStderr(t *testing.T) {
	result := ValidateTurnContent("some output", "HTTP 429 Too Many Requests", 0)
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if len(result.Errors) != 1 || result.Errors[0] != "HTTP 429 rate limit" {
		t.Fatalf("expected 'HTTP 429 rate limit' error, got %v", result.Errors)
	}
}

func TestValidateTurnContent_AuthFailureInStderr(t *testing.T) {
	result := ValidateTurnContent("some output", "Authentication failed: invalid token", 0)
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if len(result.Errors) != 1 || result.Errors[0] != "authentication failure" {
		t.Fatalf("expected 'authentication failure' error, got %v", result.Errors)
	}
}

func TestValidateTurnContent_ECONNREFUSEDInStderr(t *testing.T) {
	result := ValidateTurnContent("some output", "Error: ECONNREFUSED 127.0.0.1:8080", 0)
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if len(result.Errors) != 1 || result.Errors[0] != "connection refused" {
		t.Fatalf("expected 'connection refused' error, got %v", result.Errors)
	}
}

func TestValidateTurnContent_ShortContent(t *testing.T) {
	result := ValidateTurnContent("hello", "", 0)
	if !result.Valid {
		t.Fatal("expected valid")
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %v", result.Warnings)
	}
	if result.Warnings[0] != "very short response (5 chars)" {
		t.Fatalf("expected 'very short response (5 chars)' warning, got %q", result.Warnings[0])
	}
}

func TestValidateTurnContent_RefusalDetected(t *testing.T) {
	result := ValidateTurnContent("I cannot do that because it violates policy.", "", 0)
	if !result.Valid {
		t.Fatal("expected valid with warning")
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "possible refusal detected" {
		t.Fatalf("expected 'possible refusal detected' warning, got %v", result.Warnings)
	}
}

func TestValidateTurnContent_NonZeroExitWithContent(t *testing.T) {
	result := ValidateTurnContent("Here is some output despite the error.", "", 1)
	if !result.Valid {
		t.Fatal("expected valid with warning")
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "CLI exited with non-zero code 1 but produced output" {
		t.Fatalf("expected non-zero exit warning, got %v", result.Warnings)
	}
}

func TestValidateTurnContent_StderrOverridesContent(t *testing.T) {
	result := ValidateTurnContent("perfectly valid content here", "rate_limit exceeded", 0)
	if result.Valid {
		t.Fatal("expected invalid when stderr has error pattern even with valid content")
	}
	if len(result.Errors) != 1 || result.Errors[0] != "rate limit exceeded" {
		t.Fatalf("expected 'rate limit exceeded' error, got %v", result.Errors)
	}
}

func TestValidateTurnContent_MultipleWarnings(t *testing.T) {
	// Short content (< 10 chars) + refusal pattern
	result := ValidateTurnContent("I cannot", "", 0)
	if !result.Valid {
		t.Fatal("expected valid with warnings")
	}
	if len(result.Warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(result.Warnings), result.Warnings)
	}
	hasShort := false
	hasRefusal := false
	for _, w := range result.Warnings {
		if w == "very short response (8 chars)" {
			hasShort = true
		}
		if w == "possible refusal detected" {
			hasRefusal = true
		}
	}
	if !hasShort {
		t.Fatal("expected 'very short response' warning")
	}
	if !hasRefusal {
		t.Fatal("expected 'possible refusal detected' warning")
	}
}
