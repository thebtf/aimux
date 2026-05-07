// Strategy B — Degraded Mode Default for gemini.
//
// Gemini's config home (~/.gemini/) is HARDCODED relative to HOME. There is no
// GEMINI_HOME env var. This was explicitly requested in upstream issue #8440
// (Sep 2025, closed as "not planned"). As a result, full config virtualization
// requires HOME/USERPROFILE redirect, which is risky (OAuth token discovery,
// shell init scripts) and is gated behind DangerIsolated=true.
//
// Strategy B (accepted 2026-05-07): degraded mode is the default.
//   - Write task-specific GEMINI.md into the CWD (InstructionModeOverlayOnly).
//   - Inject GEMINI_API_KEY via EnvOverrides (env var auth, bypasses keychain).
//   - Attempt MCP suppression via --allowed-mcp-server-names (behavior INFERRED).
//   - No HOME/USERPROFILE override (DangerIsolated=false).
//
// Known limitation: ~/.gemini/settings.json MCP servers may still load unless
// --allowed-mcp-server-names successfully suppresses them. This will be verified
// empirically in Phase 4.
package runtime

// DefaultGeminiProfile returns a CLIRuntimeProfile for transparent gemini execution.
// No home override — auth passes through via ~/.gemini/settings.json (OAuth or API key).
// DangerIsolated is false: HOME redirect is not applied.
func DefaultGeminiProfile(workDir string) CLIRuntimeProfile {
	return New("gemini", workDir).
		WithHomeOverride(HomeOverrideNone). // no GEMINI_HOME; HOME redirect is DangerIsolated
		WithAuthScope(AuthScopePassThrough).
		WithStateScope(StateScopePassThrough).
		WithMCPMode(MCPModePassThrough).
		Build()
	// DangerIsolated defaults to false.
}

// DegradedGeminiProfile returns a CLIRuntimeProfile implementing Strategy B:
// best-effort isolation without HOME/USERPROFILE redirect (DangerIsolated=false).
//
// Strategy B components:
//  1. Instruction control: InstructionModeOverlayOnly — write GEMINI.md into the
//     CWD so the CLI discovers it instead of (or in addition to) ~/.gemini/GEMINI.md.
//  2. Auth: AuthScopeIsolated — callers must inject GEMINI_API_KEY via EnvOverrides.
//     This bypasses keychain-based auth (INFERRED: GEMINI_API_KEY env var takes priority).
//  3. MCP suppression: MCPModeNone with --allowed-mcp-server-names flag stub.
//     NOTE: The actual behavior of --allowed-mcp-server-names with an empty server list
//     is INFERRED — it may not suppress all servers. Empirical verification is Phase 4.
//
// DangerIsolated=false means ~/.gemini/settings.json is always loaded. The MCP
// servers configured there may load despite MCPModeNone unless the flag works.
// This is the documented degradation per Strategy B.
//
// For full isolation (HOME redirect), set DangerIsolated=true explicitly:
//
//	profile := From(DegradedGeminiProfile(workDir)).
//	    WithDangerIsolated(true).
//	    WithVirtualHomeDir("/path/to/aimux-state/gemini-home/")
func DegradedGeminiProfile(workDir string) CLIRuntimeProfile {
	return From(DefaultGeminiProfile(workDir)).
		WithInstructionMode(InstructionModeOverlayOnly).
		// Write GEMINI.md into CWD via VirtualInstructionFiles: {"GEMINI.md": "<task prompt>"}
		WithAuthScope(AuthScopeIsolated).
		// Callers must add GEMINI_API_KEY to EnvOverrides.
		WithMCPMode(MCPModeNone).
		WithExtraFlags([]string{
			// --allowed-mcp-server-names: INFERRED behavior — passing no names may suppress
			// all MCP servers. Actual behavior to be verified empirically in Phase 4.
			// If gemini requires a value after this flag, a space-separated empty list
			// or a placeholder may be needed. Left as stub pending Phase 4 probe.
			"--allowed-mcp-server-names", "",
		}).
		Build()
	// DangerIsolated=false (default) — no HOME/USERPROFILE redirect.
}
