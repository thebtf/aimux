package runtime

// DefaultClaudeProfile returns a CLIRuntimeProfile for transparent claude execution.
// No home override is applied — there is no CLAUDE_HOME env var, and HOME redirect
// behavior under claude is UNKNOWN. Authentication passes through (OAuth or API key
// via real ~/.claude/settings.json).
//
// Claude stores its Electron app data in %APPDATA%\claude\ (not home-relative),
// so HOME override would not affect keychain reads. Use IsolatedClaudeProfile
// when MCP suppression or instruction control is needed.
func DefaultClaudeProfile(workDir string) CLIRuntimeProfile {
	return New("claude", workDir).
		WithHomeOverride(HomeOverrideNone). // no CLAUDE_HOME; HOME redirect UNKNOWN
		WithAuthScope(AuthScopePassThrough).
		WithStateScope(StateScopePassThrough).
		WithMCPMode(MCPModePassThrough).
		Build()
	// DangerIsolated defaults to false.
}

// IsolatedClaudeProfile returns a CLIRuntimeProfile for isolated claude execution:
// no MCP servers, no session persistence, minimal config reads via --bare.
//
// Key flags applied (VERIFIED from claude 2.1.123 --help):
//   - --bare:                  disables hooks, LSP, plugins, CLAUDE.md auto-discovery,
//                              and keychain reads. Auth MUST be via ANTHROPIC_API_KEY.
//   - --strict-mcp-config:     ignores all MCP configs outside --mcp-config sources.
//   - --mcp-config <path>:     path to MCP config file (use an empty JSON object "{}").
//                              PreSpawnHook must write this file before spawn (Phase 3).
//   - --no-session-persistence: disables session saves (only effective with --print).
//
// CAUTION: --bare forces API-key-only auth. If the user depends on OAuth, callers
// must inject ANTHROPIC_API_KEY via EnvOverrides before spawning.
//
// The --mcp-config flag value is left as an empty string placeholder. A PreSpawnHook
// must fill it with the path to an empty JSON file. This is wired in Phase 3.
func IsolatedClaudeProfile(workDir string) CLIRuntimeProfile {
	return From(DefaultClaudeProfile(workDir)).
		WithMCPMode(MCPModeNone).
		WithStateScope(StateScopeEphemeral).
		WithInstructionMode(InstructionModeOverlayOnly).
		// CWD CLAUDE.md is the only instruction source when --bare is active.
		// Write task instructions via VirtualInstructionFiles: {"CLAUDE.md": "..."}.
		WithExtraFlags([]string{
			"--bare",                  // VERIFIED: disables hooks/LSP/plugins/keychain
			"--strict-mcp-config",    // VERIFIED: ignore all MCP outside --mcp-config
			"--mcp-config", "",       // path placeholder — filled by PreSpawnHook (Phase 3)
			"--no-session-persistence", // VERIFIED: disable session saves
		}).
		Build()
}
