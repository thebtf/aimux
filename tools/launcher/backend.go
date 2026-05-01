// Package main implements the launcher debug tool for aimux executors.
package main

import (
	"fmt"
	"time"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/conpty"
	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/types"
)

// buildCLIBackend constructs a CLI ExecutorV2 and resolved SpawnArgs from the
// given parameters. It loads CLI profiles from configDir (the aimux config/
// directory that contains default.yaml and cli.d/), runs binary discovery via
// driver.Registry.Probe() so that SpawnArgs.Command holds the full resolved
// path, then delegates argument resolution to the profile resolver.
//
// If cwd is non-empty it is applied to the returned SpawnArgs after resolution.
//
// executorChoice selects the legacy backend wrapped by the adapter:
//
//	"pipe"   — stdin/stdout pipes (default; deterministic for headless CLIs)
//	"conpty" — Windows pseudo-terminal (Win10 1809+); for TUI-style CLIs
//	"pty"    — POSIX pseudo-terminal (Linux/macOS); for TUI-style CLIs
//	"auto"   — best-available via Selector (ConPTY > PTY > Pipe)
//
// "pipe" is the default because ConPTY/PTY emulate a terminal — many headless
// CLIs (codex --json, gemini -p) detect the TTY and either buffer differently,
// expect a controlling terminal handshake, or hang waiting for input that pipe
// mode delivers via stdin. "auto" reproduces the production aimux selector
// path; use it when validating that a profile works under the same backend the
// MCP server picks.
func buildCLIBackend(configDir, cli, prompt, model, effort, cwd, executorChoice string) (types.ExecutorV2, types.SpawnArgs, error) {
	cfg, err := config.Load(configDir)
	if err != nil {
		return nil, types.SpawnArgs{}, fmt.Errorf("load config from %q: %w", configDir, err)
	}

	// Probe discovers binary paths (sets profile.ResolvedPath).
	// Without Probe the resolver falls back to the base binary name and the
	// process spawn may fail on machines where the binary is not in PATH.
	reg := driver.NewRegistry(cfg.CLIProfiles)
	reg.Probe()

	// Validate the requested CLI is configured.
	if _, err := reg.Get(cli); err != nil {
		return nil, types.SpawnArgs{}, fmt.Errorf("CLI %q: %w", cli, err)
	}

	resolver := resolve.NewProfileResolver(cfg.CLIProfiles)
	spawnArgs, err := resolver.ResolveSpawnArgsWithOpts(cli, prompt, model, effort)
	if err != nil {
		return nil, types.SpawnArgs{}, fmt.Errorf("resolve spawn args for %q: %w", cli, err)
	}

	if cwd != "" {
		spawnArgs.CWD = cwd
	}

	exec, err := buildLegacyExecutor(executorChoice)
	if err != nil {
		return nil, types.SpawnArgs{}, err
	}

	return exec, spawnArgs, nil
}

// buildLegacyExecutor constructs a CLI ExecutorV2 for the requested backend
// choice. Returns an error when the requested backend is not available on the
// current platform (e.g., "pty" on Windows, "conpty" on Linux).
func buildLegacyExecutor(choice string) (types.ExecutorV2, error) {
	switch choice {
	case "pipe":
		return executor.NewCLIPipeAdapter(pipe.New()), nil
	case "conpty":
		c := conpty.New()
		if !c.Available() {
			return nil, fmt.Errorf("conpty backend not available on this platform")
		}
		return executor.NewCLIConPTYAdapter(c), nil
	case "pty":
		p := pty.New()
		if !p.Available() {
			return nil, fmt.Errorf("pty backend not available on this platform")
		}
		return executor.NewCLIPTYAdapter(p), nil
	case "auto":
		exec := executor.NewSelector(conpty.New(), pty.New(), pipe.New()).SelectV2()
		if exec == nil {
			return nil, fmt.Errorf("no executor available on this platform")
		}
		return exec, nil
	default:
		return nil, fmt.Errorf("unknown executor choice %q (want pipe|conpty|pty|auto)", choice)
	}
}

// spawnArgsToMetadata converts a SpawnArgs into the Metadata map that
// messageToSpawnArgs (pkg/executor/adapter_common.go) understands.
//
// Key mapping (mirrors adapter_common.go messageToSpawnArgs):
//
//	"command"            → SpawnArgs.Command
//	"args"               → SpawnArgs.Args
//	"cwd"                → SpawnArgs.CWD
//	"stdin"              → SpawnArgs.Stdin  (prompt text)
//	"timeout"            → SpawnArgs.TimeoutSeconds (int, seconds)
//	"completion_pattern" → SpawnArgs.CompletionPattern
//	"env"                → SpawnArgs.Env
//
// Note: adapter_common.go reads "timeout" as int/int64/float64 seconds, so we
// store TimeoutSeconds directly as int. EnvList is a pre-built []string used by
// the resolver when it calls resolve.BuildEnv; it is not a recognized Metadata
// key in adapter_common.go, so we fall back to the Env map for the adapter path.
func spawnArgsToMetadata(args types.SpawnArgs) map[string]any {
	meta := make(map[string]any, 6)

	if args.Command != "" {
		meta["command"] = args.Command
	}
	if len(args.Args) > 0 {
		meta["args"] = args.Args
	}
	if args.CWD != "" {
		meta["cwd"] = args.CWD
	}
	if args.Stdin != "" {
		meta["stdin"] = args.Stdin
	}
	if args.TimeoutSeconds > 0 {
		meta["timeout"] = args.TimeoutSeconds
	}
	if args.CompletionPattern != "" {
		meta["completion_pattern"] = args.CompletionPattern
	}
	if len(args.Env) > 0 {
		meta["env"] = args.Env
	}

	return meta
}

// defaultTimeout is the per-request timeout applied when SpawnArgs carries no
// explicit TimeoutSeconds. Five minutes matches api/executor.go DefaultTimeout.
const defaultTimeout = 5 * time.Minute
