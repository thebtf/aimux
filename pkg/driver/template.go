package driver

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/config"
)

// TemplateParams holds values for command template resolution.
type TemplateParams struct {
	Prompt          string
	Model           string
	ReasoningEffort string
	SessionID       string
	Headless        bool
	ReadOnly        bool
	SessionResume   bool
	JSON            bool
}

// ResolveCommand builds the final command + args from a CLI profile and params.
// This replaces Go text/template with simpler, safer string building.
func ResolveCommand(profile *config.CLIProfile, params TemplateParams) (string, []string) {
	command := profile.Command.Base
	var args []string

	// Feature flags based on profile capabilities and params
	if params.Headless && profile.Features.Headless {
		// Codex-specific: --full-auto
		if profile.Name == "codex" {
			args = append(args, "--full-auto")
		}
	}

	if params.ReadOnly && len(profile.ReadOnlyFlags) > 0 {
		args = append(args, profile.ReadOnlyFlags...)
	}

	if params.Model != "" && profile.ModelFlag != "" {
		args = append(args, profile.ModelFlag, params.Model)
	} else if profile.DefaultModel != "" && profile.ModelFlag != "" {
		args = append(args, profile.ModelFlag, profile.DefaultModel)
	}

	if params.ReasoningEffort != "" && profile.Reasoning != nil {
		if profile.Reasoning.FlagValueTemplate != "" {
			val := strings.ReplaceAll(profile.Reasoning.FlagValueTemplate, "{{.Level}}", params.ReasoningEffort)
			args = append(args, profile.Reasoning.Flag, val)
		} else {
			args = append(args, profile.Reasoning.Flag, params.ReasoningEffort)
		}
	}

	if params.SessionResume && params.SessionID != "" && profile.Features.SessionResume {
		// Codex: exec resume <session_id>
		if profile.Name == "codex" {
			args = append(args, "exec", "resume", params.SessionID)
		} else if profile.Name == "claude" {
			args = append(args, "--continue", params.SessionID)
		}
	}

	// Output format flags
	if params.JSON && profile.Features.JSON {
		args = append(args, "--output-format", "json")
	}

	// Prompt via flag
	if params.Prompt != "" {
		args = append(args, profile.PromptFlag, params.Prompt)
	}

	return command, args
}

// ShouldUseStdin returns true if the prompt exceeds the CLI's stdin threshold.
func ShouldUseStdin(profile *config.CLIProfile, prompt string) bool {
	return profile.StdinThreshold > 0 && len(prompt) > profile.StdinThreshold
}

// ValidateReasoningEffort checks if a reasoning effort level is valid for a CLI.
func ValidateReasoningEffort(profile *config.CLIProfile, level string) error {
	if profile.Reasoning == nil {
		return fmt.Errorf("CLI %q does not support reasoning effort", profile.Name)
	}

	for _, l := range profile.Reasoning.Levels {
		if l == level {
			return nil
		}
	}

	return fmt.Errorf("CLI %q does not support reasoning effort %q (valid: %v)",
		profile.Name, level, profile.Reasoning.Levels)
}
