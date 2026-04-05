// Package pipeline provides output processing decorators for executors.
package pipeline

import "regexp"

// ansiPattern matches ANSI escape sequences.
// Based on ccg-workflow's sanitizeOutput pattern (~50 LOC).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?\x07|\x1b\[.*?[@-~]`)

// StripANSI removes all ANSI escape sequences from text.
func StripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}
