// Package runtime provides the CLIRuntimeProfile abstraction for controlling
// the startup-state of spawned CLI processes. It captures all configuration
// surfaces a CLI reads on startup — home directory, environment, authentication,
// state persistence, instruction files, and MCP servers — and provides a
// systematic way to override them per task.
//
// Design invariants:
//   - CLIRuntimeProfile is immutable after construction. Mutations produce new
//     profiles via the ProfileBuilder With* functional-update API.
//   - Spawn() never mutates the input profile.
//   - DangerIsolated defaults to false (security-by-default: no accidental HOME override).
package runtime

// HomeOverrideMode controls how the CLI's home directory is set at spawn time.
type HomeOverrideMode int

const (
	// HomeOverrideNone inherits the real $HOME / $USERPROFILE. Default.
	HomeOverrideNone HomeOverrideMode = iota

	// HomeOverrideVirtual sets HOME/USERPROFILE (or CLIHomeEnvVar) to a
	// directory managed by aimux. Most effective for CLIs with a dedicated
	// home env var (e.g., CODEX_HOME). Requires DangerIsolated=true when
	// CLIHomeEnvVar is empty to apply HOME/USERPROFILE redirect.
	HomeOverrideVirtual

	// HomeOverrideSymlink creates a VirtualHomeDir containing symlinks to
	// selected real files only. Avoids copying large state files (e.g., the
	// 300 MB codex SQLite DB). Requires OS symlink privilege (Developer Mode
	// on Windows or SeCreateSymbolicLinkPrivilege).
	HomeOverrideSymlink
)

// AuthScope controls how authentication credentials reach the spawned CLI.
type AuthScope int

const (
	// AuthScopePassThrough makes real auth files available — either via
	// inheritance (HomeOverrideNone) or by copying/linking them into the
	// virtual home (HomeOverrideVirtual). Default.
	AuthScopePassThrough AuthScope = iota

	// AuthScopeIsolated injects credentials via EnvOverrides only (no auth
	// files linked). Practical for API-key CLIs (codex, aider, droid).
	// Breaks OAuth-only CLIs unless they also accept env var auth.
	AuthScopeIsolated

	// AuthScopeVaultKey fetches the API key from the aimux vault at spawn
	// time and injects it via EnvOverrides. Future: requires vault integration.
	AuthScopeVaultKey
)

// StateScope controls session/state persistence for the spawned CLI.
type StateScope int

const (
	// StateScopePassThrough lets the CLI use its real state directory.
	// Risk: cross-task contamination via session resume. Default.
	StateScopePassThrough StateScope = iota

	// StateScopeEphemeral gives the CLI a fresh temp directory for state;
	// the directory is removed after the process exits (via EphemeralCleanupHook).
	// Safe for one-shot tasks — no resume possible.
	StateScopeEphemeral

	// StateScopePersistent gives the CLI a named directory keyed by project ID.
	// Enables per-project session resume across aimux task runs.
	StateScopePersistent
)

// InstructionMode controls how instruction files interact with the CLI at spawn.
type InstructionMode int

const (
	// InstructionModePassThrough lets the CLI see its real instruction files
	// (global + CWD-level). Default.
	InstructionModePassThrough InstructionMode = iota

	// InstructionModeOverlayOnly writes VirtualInstructionFiles into the CWD or
	// virtual home before spawn. Real instruction files may also be visible —
	// the CLI merges them (e.g., CWD GEMINI.md overrides ~/.gemini/GEMINI.md).
	InstructionModeOverlayOnly

	// InstructionModeReplace suppresses real instruction files (via HomeOverride
	// or empty virtual files) and uses VirtualInstructionFiles exclusively.
	InstructionModeReplace
)

// MCPMode controls which MCP servers are visible to the spawned CLI.
type MCPMode int

const (
	// MCPModePassThrough lets the CLI see its real MCP configuration. Default.
	MCPModePassThrough MCPMode = iota

	// MCPModeOverlay adds MCPServers from the profile on top of the CLI's real ones.
	MCPModeOverlay

	// MCPModeReplace uses only MCPServers from the profile; real ones are suppressed.
	MCPModeReplace

	// MCPModeNone suppresses all MCP servers. Implementation is CLI-specific:
	// codex → empty config.toml; claude → --strict-mcp-config; gemini → --allowed-mcp-server-names (INFERRED).
	MCPModeNone
)

// MCPServerConfig describes a single MCP server to inject into the CLI.
type MCPServerConfig struct {
	// Name is the server identifier as it appears in the CLI's MCP config.
	Name string

	// Command is the executable to launch for this MCP server.
	Command string

	// Args are the arguments to pass to Command.
	Args []string

	// Env are additional environment variables for the MCP server process.
	Env map[string]string
}

// PreSpawnHook runs before the CLI process is spawned. It receives a copy of
// the resolved environment (mutable — modifications affect the spawned process).
// Typical uses: write VirtualInstructionFiles, create temp directories, copy auth files.
type PreSpawnHook func(profile CLIRuntimeProfile, env map[string]string) error

// PostExitHook runs after the CLI process exits. Typical uses: cleanup ephemeral
// directories, capture state for next run (session export), record metrics.
type PostExitHook func(profile CLIRuntimeProfile, exitCode int) error

// CLIRuntimeProfile captures all startup-state control points for one CLI process spawn.
//
// Invariant: CLIRuntimeProfile is immutable after construction. Use ProfileBuilder
// and its With* methods to produce modified copies.
type CLIRuntimeProfile struct {
	// CLIName matches a key in config/cli.d/ (e.g., "codex", "claude", "gemini").
	CLIName string

	// WorkDir is the working directory for the spawned process. Controls which
	// CWD instruction files the CLI discovers (AGENTS.md, CLAUDE.md, GEMINI.md, etc.).
	WorkDir string

	// HomeOverride controls how the CLI's home directory is set at spawn time.
	// See HomeOverrideMode constants for details.
	HomeOverride HomeOverrideMode

	// CLIHomeEnvVar is the CLI-specific env var that redirects the config home.
	// When set (e.g., "CODEX_HOME"), HomeOverrideVirtual sets this env var instead
	// of HOME/USERPROFILE — more precise and less disruptive.
	// Examples: "CODEX_HOME" (VERIFIED), "" for most other CLIs.
	CLIHomeEnvVar string

	// VirtualHomeDir is the path to use as the virtual home directory.
	// When empty and HomeOverride != HomeOverrideNone, callers must provide one
	// (or Spawn returns an error). Typically: $AIMUX_STATE/<project_id>/<cli>-home/
	VirtualHomeDir string

	// EnvOverrides are key=value pairs added to the process environment.
	// Applied after os.Environ() merge. Values here win over inherited env.
	EnvOverrides map[string]string

	// UnsetEnvVars are env var names to remove from the process environment.
	// Applied after EnvOverrides. Useful for clearing telemetry vars, proxy settings, etc.
	UnsetEnvVars []string

	// AuthScope controls how authentication credentials reach the CLI.
	// See AuthScope constants for details.
	AuthScope AuthScope

	// AuthFiles is the list of auth file names to copy/link into the virtual home
	// when AuthScope == AuthScopePassThrough and HomeOverride != HomeOverrideNone.
	// Paths are relative to the real CLI config subdirectory (e.g., ~/.codex/).
	// Example: ["auth.json"] for codex, ["oauth_creds.json"] for qwen.
	AuthFiles []string

	// StateScope controls session/state persistence.
	// See StateScope constants for details.
	StateScope StateScope

	// StateDir is the directory for persistent state when StateScope == StateScopePersistent.
	// Typically: $AIMUX_STATE/<project_id>/<cli_name>/
	StateDir string

	// VirtualInstructionFiles are files to write into the virtual home or CWD
	// before spawn. Key is a path relative to the virtual home or CWD; value is content.
	// Example: {"AGENTS.md": "You are a focused aimux subagent."}
	VirtualInstructionFiles map[string]string

	// InstructionMode controls how instruction files interact with the CLI at spawn.
	// See InstructionMode constants for details.
	InstructionMode InstructionMode

	// MCPMode controls which MCP servers are visible to the CLI.
	// See MCPMode constants for details.
	MCPMode MCPMode

	// MCPServers are the MCP server configurations to inject when
	// MCPMode == MCPModeOverlay or MCPModeReplace.
	MCPServers []MCPServerConfig

	// ExtraFlags are appended to the CLI command after all profile-derived flags.
	// Use as an escape hatch for CLI-specific flags not modeled by the profile.
	ExtraFlags []string

	// PreSpawnHooks run before the process is spawned. Hooks are called in order.
	PreSpawnHooks []PreSpawnHook

	// PostExitHooks run after the process exits. Hooks are called in order.
	PostExitHooks []PostExitHook

	// DangerIsolated enables HOME/USERPROFILE redirection for CLIs that lack a
	// dedicated home env var (gemini, qwen, goose, opencode). Defaults to false
	// (security-by-default). When false and CLIHomeEnvVar is empty, no HOME
	// override is applied even if HomeOverride == HomeOverrideVirtual.
	//
	// Set to true only when full HOME isolation is required AND you understand
	// the risks: OAuth token discovery may break, shell init scripts may fail.
	DangerIsolated bool
}
