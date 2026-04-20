package redact_test

import (
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/redact"
)

// --- Positive tests: one per pattern ---

func TestRedactSecrets_OpenAIProjectKey(t *testing.T) {
	input := "OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ0123"
	got := redact.RedactSecrets(input)
	if strings.Contains(got, "sk-proj-") {
		t.Errorf("OpenAI project key not redacted; got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:openai-key-project]") {
		t.Errorf("expected [REDACTED:openai-key-project] in output; got: %q", got)
	}
}

func TestRedactSecrets_OpenAISvcAcctKey(t *testing.T) {
	input := "key=sk-svcacct-abcdefghijklmnopqrstuvwxyz0123456789"
	got := redact.RedactSecrets(input)
	if strings.Contains(got, "sk-svcacct-") {
		t.Errorf("OpenAI svcacct key not redacted; got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:openai-key-svcacct]") {
		t.Errorf("expected [REDACTED:openai-key-svcacct] in output; got: %q", got)
	}
}

func TestRedactSecrets_OpenAILegacyKey(t *testing.T) {
	input := "OPENAI_API_KEY=sk-ABCDEFGHIJKLMNOPQRSTUV"
	got := redact.RedactSecrets(input)
	if strings.Contains(got, "sk-ABCDEFGHIJKLMNOPQRSTUV") {
		t.Errorf("OpenAI legacy key not redacted; got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:openai-key-legacy]") {
		t.Errorf("expected [REDACTED:openai-key-legacy] in output; got: %q", got)
	}
}

func TestRedactSecrets_AnthropicKey(t *testing.T) {
	input := "sk-ant-api01-abcdefghijklmnopqrstuvwxyz0123456789-abc"
	got := redact.RedactSecrets(input)
	if strings.Contains(got, "sk-ant-api01-") {
		t.Errorf("Anthropic key not redacted; got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:anthropic-key]") {
		t.Errorf("expected [REDACTED:anthropic-key] in output; got: %q", got)
	}
}

func TestRedactSecrets_GoogleAIKey(t *testing.T) {
	input := "GOOGLE_API_KEY=AIzaSyBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	got := redact.RedactSecrets(input)
	if strings.Contains(got, "AIzaSy") {
		t.Errorf("Google AI key not redacted; got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:google-ai-key]") {
		t.Errorf("expected [REDACTED:google-ai-key] in output; got: %q", got)
	}
}

func TestRedactSecrets_BearerToken(t *testing.T) {
	// Bare Bearer token (no "Authorization:" prefix) — exercises bearer-token regex
	// directly. The "Authorization: Bearer ..." combination is intentionally covered
	// by the auth-header superset pattern; see TestRedactSecrets_AuthorizationHeader.
	input := "Error from upstream: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc is invalid"
	got := redact.RedactSecrets(input)
	if strings.Contains(got, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9") {
		t.Errorf("Bearer token not redacted; got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:bearer-token]") {
		t.Errorf("expected [REDACTED:bearer-token] in output; got: %q", got)
	}
}

func TestRedactSecrets_AuthorizationHeader(t *testing.T) {
	// The auth-header pattern requires a single non-space token of length ≥20 after
	// "Authorization:" — a deliberate design choice so that short schemes like
	// "Authorization: Bearer xxx" fall through to the more specific bearer-token
	// pattern. This test uses a single-token scheme such as an opaque API key.
	input := "Authorization: ApiKey-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	got := redact.RedactSecrets(input)
	if strings.Contains(got, "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG") {
		t.Errorf("auth header value not redacted; got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:auth-header]") {
		t.Errorf("expected [REDACTED:auth-header] in output; got: %q", got)
	}
}

// --- Negative tests: no secrets → unchanged ---

func TestRedactSecrets_EmptyString(t *testing.T) {
	got := redact.RedactSecrets("")
	if got != "" {
		t.Errorf("empty input should return empty; got: %q", got)
	}
}

func TestRedactSecrets_PlainText_NoSecrets(t *testing.T) {
	input := "connection refused: dial tcp 127.0.0.1:8080"
	got := redact.RedactSecrets(input)
	if got != input {
		t.Errorf("plain text without secrets should be unchanged; got: %q", got)
	}
}

func TestRedactSecrets_ShortPrefixNoMatch(t *testing.T) {
	// "sk-" with fewer than 20 chars — should NOT match
	input := "some message sk-short error occurred"
	got := redact.RedactSecrets(input)
	if got != input {
		t.Errorf("short sk- prefix should not be redacted; got: %q", got)
	}
}

func TestRedactSecrets_UnrelatedBase64_NoMatch(t *testing.T) {
	// A base64 string that doesn't start with a known secret prefix
	input := "content-hash: dGVzdGluZ3RoaXNiYXNlNjRzdHJpbmc="
	got := redact.RedactSecrets(input)
	if got != input {
		t.Errorf("unrelated base64 should not be redacted; got: %q", got)
	}
}

// --- Multiple redactions in one string ---

func TestRedactSecrets_MultipleSecrets(t *testing.T) {
	input := "key1=sk-proj-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ key2=sk-ant-api01-abcdefghijklmnopqrstuvwxyz0123456789-xyz"
	got := redact.RedactSecrets(input)
	if strings.Contains(got, "sk-proj-") {
		t.Errorf("project key not redacted in multi-secret input")
	}
	if strings.Contains(got, "sk-ant-api01-") {
		t.Errorf("anthropic key not redacted in multi-secret input")
	}
	if !strings.Contains(got, "[REDACTED:openai-key-project]") {
		t.Errorf("expected openai-key-project placeholder")
	}
	if !strings.Contains(got, "[REDACTED:anthropic-key]") {
		t.Errorf("expected anthropic-key placeholder")
	}
}

// --- Pattern version ---

func TestPatternVersion(t *testing.T) {
	if redact.PatternVersion == "" {
		t.Error("PatternVersion must be non-empty")
	}
	// Format check: YYYY-MM-DD
	if len(redact.PatternVersion) != 10 {
		t.Errorf("PatternVersion should be YYYY-MM-DD format, got: %q", redact.PatternVersion)
	}
}

func TestSecretPatterns_AllCompile(t *testing.T) {
	if len(redact.SecretPatterns) < 7 {
		t.Errorf("expected at least 7 patterns, got %d", len(redact.SecretPatterns))
	}
	for i, p := range redact.SecretPatterns {
		if p.Label == "" {
			t.Errorf("pattern[%d] has empty label", i)
		}
		if p.Regex == nil {
			t.Errorf("pattern[%d] %q has nil regex", i, p.Label)
		}
	}
}

// --- Benchmark ---

func BenchmarkRedactSecrets_2KB(b *testing.B) {
	// 2KB input with one embedded secret — typical error excerpt size.
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ0123"
	prefix := strings.Repeat("x", 500)
	suffix := strings.Repeat("y", 1400)
	input := prefix + " key=" + secret + " " + suffix

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = redact.RedactSecrets(input)
	}
	// Target: <100µs per op
}
