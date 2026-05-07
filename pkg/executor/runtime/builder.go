package runtime

// ProfileBuilder constructs CLIRuntimeProfile values via a fluent functional-update API.
// All With* methods return a new ProfileBuilder (the internal profile is copied on each step).
// The original builder is never mutated. Call Build() to get the final CLIRuntimeProfile.
type ProfileBuilder struct {
	p CLIRuntimeProfile
}

// New creates a ProfileBuilder with CLIName and WorkDir set.
// All other fields default to their zero values (PassThrough modes, DangerIsolated=false).
func New(cliName, workDir string) ProfileBuilder {
	return ProfileBuilder{
		p: CLIRuntimeProfile{
			CLIName: cliName,
			WorkDir: workDir,
		},
	}
}

// From creates a ProfileBuilder from an existing CLIRuntimeProfile.
// Use to derive a modified profile without mutating the original.
func From(profile CLIRuntimeProfile) ProfileBuilder {
	return ProfileBuilder{p: profile}
}

// Build returns the constructed CLIRuntimeProfile.
func (b ProfileBuilder) Build() CLIRuntimeProfile {
	return b.p
}

// WithWorkDir returns a new ProfileBuilder with WorkDir set to dir.
func (b ProfileBuilder) WithWorkDir(dir string) ProfileBuilder {
	p := b.p
	p.WorkDir = dir
	return ProfileBuilder{p: p}
}

// WithHomeOverride returns a new ProfileBuilder with HomeOverride set to mode.
func (b ProfileBuilder) WithHomeOverride(mode HomeOverrideMode) ProfileBuilder {
	p := b.p
	p.HomeOverride = mode
	return ProfileBuilder{p: p}
}

// WithCLIHomeEnvVar returns a new ProfileBuilder with CLIHomeEnvVar set to envVar.
func (b ProfileBuilder) WithCLIHomeEnvVar(envVar string) ProfileBuilder {
	p := b.p
	p.CLIHomeEnvVar = envVar
	return ProfileBuilder{p: p}
}

// WithVirtualHomeDir returns a new ProfileBuilder with VirtualHomeDir set to dir.
func (b ProfileBuilder) WithVirtualHomeDir(dir string) ProfileBuilder {
	p := b.p
	p.VirtualHomeDir = dir
	return ProfileBuilder{p: p}
}

// WithEnvOverrides returns a new ProfileBuilder with EnvOverrides set to a copy of overrides.
// The original map is copied — mutations to overrides after this call do not affect the profile.
func (b ProfileBuilder) WithEnvOverrides(overrides map[string]string) ProfileBuilder {
	p := b.p
	if len(overrides) == 0 {
		p.EnvOverrides = nil
		return ProfileBuilder{p: p}
	}
	m := make(map[string]string, len(overrides))
	for k, v := range overrides {
		m[k] = v
	}
	p.EnvOverrides = m
	return ProfileBuilder{p: p}
}

// WithUnsetEnvVars returns a new ProfileBuilder with UnsetEnvVars set to a copy of vars.
func (b ProfileBuilder) WithUnsetEnvVars(vars []string) ProfileBuilder {
	p := b.p
	if len(vars) == 0 {
		p.UnsetEnvVars = nil
		return ProfileBuilder{p: p}
	}
	v := make([]string, len(vars))
	copy(v, vars)
	p.UnsetEnvVars = v
	return ProfileBuilder{p: p}
}

// WithAuthScope returns a new ProfileBuilder with AuthScope set to scope.
func (b ProfileBuilder) WithAuthScope(scope AuthScope) ProfileBuilder {
	p := b.p
	p.AuthScope = scope
	return ProfileBuilder{p: p}
}

// WithAuthFiles returns a new ProfileBuilder with AuthFiles set to a copy of files.
func (b ProfileBuilder) WithAuthFiles(files []string) ProfileBuilder {
	p := b.p
	if len(files) == 0 {
		p.AuthFiles = nil
		return ProfileBuilder{p: p}
	}
	f := make([]string, len(files))
	copy(f, files)
	p.AuthFiles = f
	return ProfileBuilder{p: p}
}

// WithStateScope returns a new ProfileBuilder with StateScope set to scope.
func (b ProfileBuilder) WithStateScope(scope StateScope) ProfileBuilder {
	p := b.p
	p.StateScope = scope
	return ProfileBuilder{p: p}
}

// WithStateDir returns a new ProfileBuilder with StateDir set to dir.
func (b ProfileBuilder) WithStateDir(dir string) ProfileBuilder {
	p := b.p
	p.StateDir = dir
	return ProfileBuilder{p: p}
}

// WithVirtualInstructionFiles returns a new ProfileBuilder with VirtualInstructionFiles
// set to a copy of files. The original map is copied.
func (b ProfileBuilder) WithVirtualInstructionFiles(files map[string]string) ProfileBuilder {
	p := b.p
	if len(files) == 0 {
		p.VirtualInstructionFiles = nil
		return ProfileBuilder{p: p}
	}
	m := make(map[string]string, len(files))
	for k, v := range files {
		m[k] = v
	}
	p.VirtualInstructionFiles = m
	return ProfileBuilder{p: p}
}

// WithInstructionMode returns a new ProfileBuilder with InstructionMode set to mode.
func (b ProfileBuilder) WithInstructionMode(mode InstructionMode) ProfileBuilder {
	p := b.p
	p.InstructionMode = mode
	return ProfileBuilder{p: p}
}

// WithMCPMode returns a new ProfileBuilder with MCPMode set to mode.
func (b ProfileBuilder) WithMCPMode(mode MCPMode) ProfileBuilder {
	p := b.p
	p.MCPMode = mode
	return ProfileBuilder{p: p}
}

// WithMCPServers returns a new ProfileBuilder with MCPServers set to a copy of servers.
func (b ProfileBuilder) WithMCPServers(servers []MCPServerConfig) ProfileBuilder {
	p := b.p
	if len(servers) == 0 {
		p.MCPServers = nil
		return ProfileBuilder{p: p}
	}
	s := make([]MCPServerConfig, len(servers))
	copy(s, servers)
	p.MCPServers = s
	return ProfileBuilder{p: p}
}

// WithExtraFlags returns a new ProfileBuilder with ExtraFlags set to a copy of flags.
func (b ProfileBuilder) WithExtraFlags(flags []string) ProfileBuilder {
	p := b.p
	if len(flags) == 0 {
		p.ExtraFlags = nil
		return ProfileBuilder{p: p}
	}
	f := make([]string, len(flags))
	copy(f, flags)
	p.ExtraFlags = f
	return ProfileBuilder{p: p}
}

// WithPreSpawnHook returns a new ProfileBuilder with hook appended to PreSpawnHooks.
// Hooks are called in order.
func (b ProfileBuilder) WithPreSpawnHook(hook PreSpawnHook) ProfileBuilder {
	p := b.p
	hooks := make([]PreSpawnHook, len(p.PreSpawnHooks)+1)
	copy(hooks, p.PreSpawnHooks)
	hooks[len(p.PreSpawnHooks)] = hook
	p.PreSpawnHooks = hooks
	return ProfileBuilder{p: p}
}

// WithPostExitHook returns a new ProfileBuilder with hook appended to PostExitHooks.
func (b ProfileBuilder) WithPostExitHook(hook PostExitHook) ProfileBuilder {
	p := b.p
	hooks := make([]PostExitHook, len(p.PostExitHooks)+1)
	copy(hooks, p.PostExitHooks)
	hooks[len(p.PostExitHooks)] = hook
	p.PostExitHooks = hooks
	return ProfileBuilder{p: p}
}

// WithDangerIsolated returns a new ProfileBuilder with DangerIsolated set to v.
// When true, HOME/USERPROFILE redirection is enabled for CLIs without a dedicated
// home env var. See CLIRuntimeProfile.DangerIsolated for risks.
func (b ProfileBuilder) WithDangerIsolated(v bool) ProfileBuilder {
	p := b.p
	p.DangerIsolated = v
	return ProfileBuilder{p: p}
}
