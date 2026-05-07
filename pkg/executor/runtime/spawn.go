package runtime

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/thebtf/aimux/pkg/types"
)

// Spawn translates a CLIRuntimeProfile and a base SpawnArgs into a new SpawnArgs
// ready for the executor. The input profile and base args are never mutated.
//
// Spawn applies transformations in this order:
//  1. Copy base SpawnArgs into a new value.
//  2. Set CWD from profile.WorkDir (if non-empty).
//  3. Build the resolved environment (copy of os.Environ equivalent stored in base.Env).
//  4. Run all PreSpawnHooks with the resolved env map (hooks may add/modify env vars).
//  5. Apply profile.EnvOverrides on top of the resolved env map.
//  6. Remove profile.UnsetEnvVars from the env map.
//  7. Apply HomeOverride: set CLIHomeEnvVar (or HOME/USERPROFILE if DangerIsolated).
//  8. Append profile.ExtraFlags to Args.
//  9. Return the new SpawnArgs.
//
// Spawn does NOT run PostExitHooks — callers must invoke them after the process exits.
//
// Errors from PreSpawnHooks abort spawn and are returned immediately.
// The returned SpawnArgs.Env map is non-nil when any env transforms were applied.
func Spawn(profile CLIRuntimeProfile, base types.SpawnArgs) (types.SpawnArgs, error) {
	out := base // shallow copy; slices/maps below are replaced, not mutated

	if profile.WorkDir != "" {
		out.CWD = profile.WorkDir
	}

	// Build a mutable env map from base.Env and base.EnvList (base is not mutated).
	env := make(map[string]string, len(base.Env)+len(base.EnvList)+len(profile.EnvOverrides)+4)
	for k, v := range base.Env {
		env[k] = v
	}
	for _, kv := range base.EnvList {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		env[k] = v
	}

	// Run PreSpawnHooks with the mutable env map.
	for i, hook := range profile.PreSpawnHooks {
		if hook == nil {
			continue
		}
		if err := hook(profile, env); err != nil {
			return types.SpawnArgs{}, fmt.Errorf("runtime.Spawn: PreSpawnHook[%d] failed: %w", i, err)
		}
	}

	// Apply EnvOverrides (win over both base.Env and hook additions).
	for k, v := range profile.EnvOverrides {
		env[k] = v
	}

	// Remove UnsetEnvVars.
	for _, key := range profile.UnsetEnvVars {
		delete(env, key)
	}

	// Apply HomeOverride.
	if profile.HomeOverride == HomeOverrideVirtual || profile.HomeOverride == HomeOverrideSymlink {
		if profile.CLIHomeEnvVar != "" {
			// CLI-specific home redirect (most precise — e.g., CODEX_HOME for codex).
			if profile.VirtualHomeDir == "" {
				return types.SpawnArgs{}, fmt.Errorf(
					"runtime.Spawn: HomeOverride requires VirtualHomeDir when CLIHomeEnvVar=%q for CLI %q",
					profile.CLIHomeEnvVar, profile.CLIName,
				)
			}
			env[profile.CLIHomeEnvVar] = profile.VirtualHomeDir
		} else if profile.DangerIsolated {
			// Broad HOME/USERPROFILE redirect. Only applied when explicitly opted in.
			// Risk: OAuth token discovery, shell init scripts may break.
			if profile.VirtualHomeDir == "" {
				return types.SpawnArgs{}, fmt.Errorf(
					"runtime.Spawn: DangerIsolated=true requires VirtualHomeDir to be set for CLI %q",
					profile.CLIName)
			}
			if runtime.GOOS == "windows" {
				env["USERPROFILE"] = profile.VirtualHomeDir
				env["HOMEPATH"] = profile.VirtualHomeDir
			} else {
				env["HOME"] = profile.VirtualHomeDir
			}
		}
		// If CLIHomeEnvVar=="" and DangerIsolated==false: no HOME override. Documented degradation.
	}

	// Replace out.Env with the resolved map (does not mutate base.Env or base.EnvList).
	// Trigger when any env source was populated or any transform was applied so that
	// the executor uses the Env map path (not a stale EnvList).
	envTransformed := len(profile.PreSpawnHooks) > 0 ||
		len(profile.EnvOverrides) > 0 ||
		len(profile.UnsetEnvVars) > 0 ||
		profile.HomeOverride != HomeOverrideNone
	if len(base.Env) > 0 || len(base.EnvList) > 0 || envTransformed {
		out.Env = env
		out.EnvList = nil // clear EnvList so executor uses Env map path
	}

	// Append ExtraFlags to Args (copy to avoid mutating base.Args slice).
	if len(profile.ExtraFlags) > 0 {
		merged := make([]string, len(base.Args)+len(profile.ExtraFlags))
		copy(merged, base.Args)
		copy(merged[len(base.Args):], profile.ExtraFlags)
		out.Args = merged
	}

	return out, nil
}
