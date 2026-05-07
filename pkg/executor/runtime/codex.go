package runtime

// DefaultCodexProfile returns a CLIRuntimeProfile for transparent codex execution.
// The profile uses CODEX_HOME env var (VERIFIED: redirects all config reads) to point
// codex at a virtual home directory, while inheriting auth and state from the real home.
//
// CODEX_HOME isolation is the highest-confidence mechanism in the CLI set — fully
// verified on Windows 11 with codex 0.128.0. The virtual home must NOT be a temp
// directory: codex emits "Refusing to create helper binaries under temporary dir" and
// the sandbox binary (~/.codex/.sandbox-bin/codex.exe, 142 MB) will be unavailable.
// Use a stable path under $AIMUX_STATE/<project_id>/codex-home/.
func DefaultCodexProfile(workDir string) CLIRuntimeProfile {
	return New("codex", workDir).
		WithHomeOverride(HomeOverrideVirtual).
		WithCLIHomeEnvVar("CODEX_HOME"). // VERIFIED: redirects entire config dir
		WithAuthScope(AuthScopePassThrough).
		WithAuthFiles([]string{"auth.json"}). // copy real ~/.codex/auth.json into virtual home
		WithStateScope(StateScopePassThrough).
		WithMCPMode(MCPModePassThrough).
		Build()
	// DangerIsolated defaults to false — CODEX_HOME covers the isolation need.
}

// IsolatedCodexProfile returns a CLIRuntimeProfile for fully isolated codex execution:
// no MCP servers, no session resume, task-specific instruction injection.
//
// Callers must set VirtualHomeDir to a stable (non-temp) path before spawning.
// The --ephemeral flag prevents codex from writing session files to the virtual home,
// making cleanup trivial.
//
// To inject task-specific instructions, use From(IsolatedCodexProfile(...)) and
// WithVirtualInstructionFiles({"AGENTS.md": "<task prompt>"}).
func IsolatedCodexProfile(workDir string) CLIRuntimeProfile {
	return From(DefaultCodexProfile(workDir)).
		WithMCPMode(MCPModeNone).
		// MCPModeNone implementation: write a virtual config.toml with no [mcp_servers.*]
		// sections into VirtualHomeDir. This is handled by a PreSpawnHook in Phase 2.
		WithStateScope(StateScopeEphemeral).
		WithInstructionMode(InstructionModeReplace).
		WithExtraFlags([]string{"--ephemeral"}).
		// --ephemeral: codex does not persist session files to disk (VERIFIED flag).
		Build()
}
