// Package resolve provides CLI command resolution from profile data.
// Extracted from pkg/server to be shared by both exec handler and orchestrator strategies.
package resolve

import (
	"strings"

	"github.com/thebtf/aimux/pkg/config"
)

// CommandBinary extracts the binary name from command.base (first word).
// For "testcli codex --json" returns "testcli".
// For "codex" returns "codex".
func CommandBinary(base string) string {
	if i := strings.IndexByte(base, ' '); i > 0 {
		return base[:i]
	}
	return base
}

// CommandBaseArgs extracts extra args from command.base (all words after the first).
// For "testcli codex --json" returns ["codex", "--json"].
// For "echo" returns nil.
func CommandBaseArgs(base string) []string {
	parts := strings.Fields(base)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

// BuildPromptArgs constructs CLI arguments for prompt delivery.
// Handles command.base args, headless mode, read-only flags, model, reasoning effort,
// and prompt flag resolution. This is the shared core used by both the exec handler
// and the orchestrator resolver.
func BuildPromptArgs(profile *config.CLIProfile, model, effort string, readOnly bool, prompt string) []string {
	baseArgs := CommandBaseArgs(profile.Command.Base)
	args := append([]string{}, baseArgs...)

	if profile.Features.Headless && profile.Name == "codex" {
		args = append(args, "--full-auto")
	}

	if readOnly && len(profile.ReadOnlyFlags) > 0 {
		args = append(args, profile.ReadOnlyFlags...)
	}

	if model != "" && profile.ModelFlag != "" {
		args = append(args, profile.ModelFlag, model)
	}

	if effort != "" && profile.Reasoning != nil {
		if profile.Reasoning.FlagValueTemplate != "" {
			val := strings.ReplaceAll(profile.Reasoning.FlagValueTemplate, "%s", effort)
			args = append(args, profile.Reasoning.Flag, val)
		} else {
			args = append(args, profile.Reasoning.Flag, effort)
		}
	}

	if prompt != "" {
		if profile.PromptFlag != "" {
			args = append(args, profile.PromptFlag, prompt)
		} else {
			args = append(args, prompt)
		}
	}

	return args
}
