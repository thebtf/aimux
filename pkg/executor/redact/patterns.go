// Package redact provides secret-pattern redaction for aimux error diagnostics.
// Patterns are applied before storing any stderr excerpt to the database or logs,
// preventing API keys and bearer tokens from appearing in persisted error fields.
package redact

import "regexp"

// PatternVersion is a date stamp identifying the current set of patterns.
// Increment when patterns change so audit tools can detect stale redaction.
const PatternVersion = "2026-04-20"

// SecretPattern pairs a human-readable label with a compiled regex.
// Label is used as the replacement placeholder: [REDACTED:<label>].
type SecretPattern struct {
	Label string
	Regex *regexp.Regexp
}

// SecretPatterns is the ordered list of patterns applied by RedactSecrets.
// More specific patterns (project/svcacct) are listed before the generic
// legacy pattern to prevent partial matches swallowing the label suffix.
//
// NOTE: Key format currency as of PatternVersion = "2026-04-20".
// Before shipping, verify current formats at:
//   - platform.openai.com/docs/api-reference/authentication
//   - docs.anthropic.com/claude/reference/authentication
//   - console.cloud.google.com/apis/credentials
var SecretPatterns = []SecretPattern{
	// Specific prefixes MUST precede the generic legacy `sk-...` pattern —
	// the legacy regex matches any `sk-[A-Za-z0-9_\-]{20,}` substring and
	// would otherwise swallow anthropic/project/svcacct keys under the
	// wrong label. Order is load-bearing.

	// OpenAI project key (sk-proj-<base62>)
	{Label: "openai-key-project", Regex: regexp.MustCompile(`sk-proj-[A-Za-z0-9_\-]{20,}`)},
	// OpenAI service-account key (sk-svcacct-<base62>)
	{Label: "openai-key-svcacct", Regex: regexp.MustCompile(`sk-svcacct-[A-Za-z0-9_\-]{20,}`)},
	// Anthropic key (sk-ant-api<NN>-<base62>) — before legacy to prevent
	// `sk-ant-api03-<token>` from being tagged `openai-key-legacy`.
	{Label: "anthropic-key", Regex: regexp.MustCompile(`sk-ant-api\d{2}-[A-Za-z0-9_\-]{20,}`)},
	// OpenAI legacy key (sk-<base64url>) — LAST of the sk-* family.
	// Includes underscores and hyphens per base64url encoding (OpenAI docs, 2026-04-20).
	{Label: "openai-key-legacy", Regex: regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`)},
	// Google AI / Gemini key (AIza<base62>)
	{Label: "google-ai-key", Regex: regexp.MustCompile(`AIza[A-Za-z0-9_\-]{35,}`)},
	// Generic Bearer token (Authorization: Bearer <token>)
	{Label: "bearer-token", Regex: regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_\-\.=]{20,}`)},
	// Generic Authorization header value
	{Label: "auth-header", Regex: regexp.MustCompile(`(?i)Authorization:\s*[^\s]{20,}`)},
}
