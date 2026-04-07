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
	profile, ok := r.profiles[cli]
	if !ok {
		return types.SpawnArgs{}, fmt.Errorf("CLI %q not configured", cli)
	}

	args := BuildPromptArgs(profile, "", "", false, prompt)

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
		sa.Args = BuildPromptArgs(profile, "", "", false, "")
	}

	return sa, nil
}
