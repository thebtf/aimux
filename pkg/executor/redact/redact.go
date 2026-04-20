package redact

// RedactSecrets replaces all known secret patterns in s with [REDACTED:<label>]
// placeholders. Returns s unchanged if s is empty or contains no matches.
// Safe to call on any string — non-secret content passes through unmodified.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	for _, p := range SecretPatterns {
		s = p.Regex.ReplaceAllString(s, "[REDACTED:"+p.Label+"]")
	}
	return s
}
