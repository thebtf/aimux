package resolve

import (
	"fmt"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/types"
)

// ProfileResolver implements types.CLIResolver using CLI profile data.
type ProfileResolver struct {
	profiles map[string]*config.CLIProfile
}

// NewProfileResolver creates a resolver with the given profile map.
func NewProfileResolver(profiles map[string]*config.CLIProfile) *ProfileResolver {
	return &ProfileResolver{profiles: profiles}
}

// ResolveSpawnArgs resolves complete SpawnArgs from CLI profile and prompt.
// Returns error if CLI profile is not found.
func (r *ProfileResolver) ResolveSpawnArgs(cli string, prompt string) (types.SpawnArgs, error) {
	return r.ResolveSpawnArgsWithOpts(cli, prompt, "", "")
}

// ResolveSpawnArgsWithOpts resolves SpawnArgs with optional model and effort overrides.
// When model or effort are non-empty they are passed to BuildPromptArgs so the
// appropriate CLI flags are included. Implements types.ModelledCLIResolver.
func (r *ProfileResolver) ResolveSpawnArgsWithOpts(cli string, prompt string, model string, effort string) (types.SpawnArgs, error) {
	profile, ok := r.profiles[cli]
	if !ok {
		return types.SpawnArgs{}, fmt.Errorf("CLI %q not configured", cli)
	}

	// Always pipe prompt via stdin to avoid Windows 8191 char command line limit,
	// shell escaping issues, and dual code paths. All supported CLIs accept stdin.
	// Build args without prompt; prompt goes in SpawnArgs.Stdin.
	args := BuildPromptArgs(profile, model, effort, false, "")

	// Codex CLI requires an explicit "-" sentinel as a positional argument to
	// signal that the prompt should be read from stdin. Without it, codex exec
	// ignores stdin and exits with an error. Other CLIs (Gemini, Aider, etc.)
	// read stdin directly and do not need the sentinel.
	if profile.StdinSentinel != "" {
		args = append(args, profile.StdinSentinel)
	}

	// Use resolved full path if available (found outside PATH by discovery)
	command := CommandBinary(profile.Command.Base)
	if profile.ResolvedPath != "" {
		command = profile.ResolvedPath
	}

	return types.SpawnArgs{
		CLI:               cli,
		Command:           command,
		Args:              args,
		Stdin:             prompt,
		CompletionPattern: profile.CompletionPattern,
	}, nil
}
