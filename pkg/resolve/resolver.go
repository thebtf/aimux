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

	args := BuildPromptArgs(profile, model, effort, false, prompt)

	// Use resolved full path if available (found outside PATH by discovery)
	command := CommandBinary(profile.Command.Base)
	if profile.ResolvedPath != "" {
		command = profile.ResolvedPath
	}

	sa := types.SpawnArgs{
		CLI:               cli,
		Command:           command,
		Args:              args,
		CompletionPattern: profile.CompletionPattern,
	}

	// Stdin piping for long prompts (Windows 8191 char limit)
	if profile.StdinThreshold > 0 && len(prompt) > profile.StdinThreshold {
		sa.Stdin = prompt
		sa.Args = BuildPromptArgs(profile, model, effort, false, "")
	}

	return sa, nil
}
