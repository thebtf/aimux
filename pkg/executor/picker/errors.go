// Package picker provides CLI selection routing for aimux task dispatch.
// When a caller does not specify a CLI, Picker.Pick selects the optimal one
// based on static capability scores, binary health, and config overrides.
package picker

import (
	"fmt"
	"strings"
)

// CLIFailureReason captures why a specific CLI was rejected during health filtering.
type CLIFailureReason struct {
	// CLI is the CLI name (e.g., "codex", "claude", "gemini").
	CLI string

	// Reason is a human-readable explanation of the failure.
	Reason string
}

// ErrNoHealthyCLI is returned by Picker.Pick when every active CLI fails the
// health check. It carries the per-CLI failure reasons to help the user diagnose
// and fix the root cause (install a missing binary, configure auth, etc.).
//
// Never silently fall back to an unhealthy CLI — surface this error as a
// structured MCP error so the user can act on it.
type ErrNoHealthyCLI struct {
	// Reasons lists each CLI that was checked and why it was rejected.
	Reasons []CLIFailureReason
}

// Error implements the error interface with a human-readable summary.
func (e *ErrNoHealthyCLI) Error() string {
	if len(e.Reasons) == 0 {
		return "no healthy CLI available: no CLIs configured"
	}

	parts := make([]string, 0, len(e.Reasons))
	for _, r := range e.Reasons {
		parts = append(parts, fmt.Sprintf("%s: %s", r.CLI, r.Reason))
	}
	return fmt.Sprintf("no healthy CLI available: %s", strings.Join(parts, "; "))
}
