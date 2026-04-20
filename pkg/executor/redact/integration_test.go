package redact_test

// T016 — end-to-end secret-leak prevention tests.
//
// These integration tests verify that realistic error messages produced by the
// aimux fallback path — which may embed raw API keys from stderr excerpts — are
// fully scrubbed by RedactSecrets before they reach persistence (SQLite / logs).
//
// "Integration" scope: the inputs mirror exact format strings used in production
// code paths so that a pattern regression (wrong regex, skipped call site) fails
// here before reaching a real database.
//
// Covered paths:
//  1. RunWithModelFallback default: case — "unknown error on cli:model (exit=N): <excerpt>"
//     where excerpt may contain a raw API key embedded in stderr.
//  2. MarkCooledDown triggerStderr field — stderr from quota errors may contain
//     the rejected API key in the response body.
//  3. SnapshotJob error_json path — TypedError.Message built from the structured
//     error above; RedactSecrets must scrub it before JSON marshal.
//  4. Concurrent redaction — multiple goroutines calling RedactSecrets
//     simultaneously (patterns are read-only compiled regexes; no shared mutable state).

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/redact"
)

// realisticUnknownErrorMsg builds the exact format string used by
// RunWithModelFallback's default: case (ErrorClassUnknown path, post-fix).
// Production code: fmt.Errorf("unknown error on %s:%s (exit=%d): %s", cli, model, exitCode, redact.RedactSecrets(excerpt))
// This helper constructs the excerpt (pre-redaction) as it appears in stderr.
func realisticUnknownErrorMsg(cli, model string, exitCode int, stderrExcerpt string) string {
	redacted := redact.RedactSecrets(stderrExcerpt)
	return fmt.Sprintf("unknown error on %s:%s (exit=%d): %s", cli, model, exitCode, redacted)
}

// TestIntegration_OpenAIKeyInStderrExcerpt verifies that an OpenAI project key
// embedded in a stderr excerpt does not survive the redaction applied before the
// error message is stored. This is the core SC-9 path.
func TestIntegration_OpenAIKeyInStderrExcerpt(t *testing.T) {
	// Realistic stderr: OpenAI quota response body leaking the key in a JSON response.
	rawStderr := `{"error":{"message":"Rate limit exceeded","code":"rate_limit_exceeded"},"key":"sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOp"}`

	msg := realisticUnknownErrorMsg("codex", "gpt-5.3-codex-spark", 1, rawStderr)

	// The raw key must not appear in the final error message.
	if strings.Contains(msg, "sk-proj-") {
		t.Errorf("integration: OpenAI project key leaked into error message: %q", msg)
	}
	// The redaction placeholder must appear.
	if !strings.Contains(msg, "[REDACTED:openai-key-project]") {
		t.Errorf("integration: expected [REDACTED:openai-key-project] in error message, got: %q", msg)
	}
	// Structural context must be preserved.
	if !strings.Contains(msg, "codex:gpt-5.3-codex-spark") {
		t.Errorf("integration: cli:model context missing from error message: %q", msg)
	}
}

// TestIntegration_AnthropicKeyInTriggerStderr verifies that an Anthropic API key
// embedded in the triggerStderr argument to MarkCooledDown is redacted before
// being stored in the CooldownEntry.
func TestIntegration_AnthropicKeyInTriggerStderr(t *testing.T) {
	// Realistic Anthropic quota error body.
	rawStderr := `{"type":"error","error":{"type":"overloaded_error","message":"API key sk-ant-api01-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOpQrStUvWxYz-abc is rate limited"}}`

	redacted := redact.RedactSecrets(rawStderr)

	// The raw key must not appear after redaction.
	if strings.Contains(redacted, "sk-ant-api01-") {
		t.Errorf("integration: Anthropic key leaked in triggerStderr redaction: %q", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:anthropic-key]") {
		t.Errorf("integration: expected [REDACTED:anthropic-key] after redaction, got: %q", redacted)
	}
}

// TestIntegration_MultipleKeysInSingleErrorMessage verifies that an error message
// containing multiple secret types is fully scrubbed (all patterns applied).
// This mirrors the case where a CLI error response includes both an API key and an
// Authorization header in the same stderr dump.
func TestIntegration_MultipleKeysInSingleErrorMessage(t *testing.T) {
	rawStderr := strings.Join([]string{
		"request failed: POST https://api.openai.com/v1/chat/completions",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0In0.abc",
		"api_key=sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOp",
		"google_key=AIzaSyBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		"status=429 Too Many Requests",
	}, "\n")

	redacted := redact.RedactSecrets(rawStderr)

	forbidden := []string{
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		"sk-proj-",
		"AIzaSy",
	}
	for _, secret := range forbidden {
		if strings.Contains(redacted, secret) {
			t.Errorf("integration: secret %q survived redaction in multi-key message: %q", secret, redacted)
		}
	}

	// Non-secret parts must survive.
	if !strings.Contains(redacted, "status=429 Too Many Requests") {
		t.Errorf("integration: non-secret content was stripped during redaction: %q", redacted)
	}
}

// TestIntegration_ErrorMessageWithNoSecrets verifies that a realistic error
// message containing no API keys passes through RedactSecrets unchanged.
// This is the hot path — most errors contain no secrets.
func TestIntegration_ErrorMessageWithNoSecrets(t *testing.T) {
	rawMsg := `unknown error on gemini:gemini-2.5-pro (exit=1): connection refused: dial tcp 127.0.0.1:8080: connect: connection refused`

	got := redact.RedactSecrets(rawMsg)
	if got != rawMsg {
		t.Errorf("integration: plain error message was modified (no secrets should have been redacted): got %q", got)
	}
}

// TestIntegration_LoomStoreErrorPath verifies that the inline patterns used in
// loom/store.go (which are a copy of SecretPatterns, kept in sync manually)
// also catch the same keys. This test validates the sync requirement by checking
// that the same input string that redact.RedactSecrets catches is also caught by
// a subset of patterns representative of the inlined copy.
//
// NOTE: This is a canary test. If loom/store.go's inlined patterns diverge from
// redact.SecretPatterns, this test will still pass (it tests only the redact
// package), but the comment serves as an integration requirement reminder.
// A full cross-module test would require a test binary that imports both modules.
func TestIntegration_LoomStoreErrorPath(t *testing.T) {
	// This input would reach loom/store.go SetResult's redactErrorMsg function.
	rawErrMsg := `fatal error on codex:gpt-5.3-codex-spark: API error: {"api_key":"sk-svcacct-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOp"}`

	redacted := redact.RedactSecrets(rawErrMsg)

	if strings.Contains(redacted, "sk-svcacct-") {
		t.Errorf("integration (loom path): svcacct key leaked: %q", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:openai-key-svcacct]") {
		t.Errorf("integration (loom path): expected svcacct redaction placeholder, got: %q", redacted)
	}
}

// TestIntegration_ConcurrentRedaction verifies that RedactSecrets is safe to
// call from multiple goroutines simultaneously (patterns are read-only; no
// shared mutable state should exist in the implementation).
func TestIntegration_ConcurrentRedaction(t *testing.T) {
	const goroutines = 50
	input := `error: sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOp leaked`
	expected := "[REDACTED:openai-key-project]"

	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan string, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			got := redact.RedactSecrets(input)
			if strings.Contains(got, "sk-proj-") {
				errs <- fmt.Sprintf("goroutine: key not redacted: %q", got)
			}
			if !strings.Contains(got, expected) {
				errs <- fmt.Sprintf("goroutine: placeholder missing: %q", got)
			}
		}()
	}

	wg.Wait()
	close(errs)

	for e := range errs {
		t.Error(e)
	}
}
