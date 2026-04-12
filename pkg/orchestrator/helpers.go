package orchestrator

import "github.com/thebtf/aimux/pkg/types"

// resolveOrFallback resolves SpawnArgs from CLI profile when a resolver is available.
// Falls back to legacy behavior (Command=cli, Args=["-p", prompt]) when resolver is nil
// or returns an error, ensuring backward compatibility with tests using mock executors.
func resolveOrFallback(resolver types.CLIResolver, cli, prompt, cwd string, timeout int) types.SpawnArgs {
	return resolveOrFallbackWithOpts(resolver, cli, prompt, cwd, timeout, "", "")
}

// resolveOrFallbackWithOpts resolves SpawnArgs with optional model/effort overrides.
// When model or effort is non-empty AND the resolver implements ModelledCLIResolver,
// the modelled path is used so per-strategy AIMUX_ROLE_* overrides reach the CLI
// profile. Otherwise falls back to the base CLIResolver path, preserving backward
// compatibility with legacy resolvers and mock executors.
func resolveOrFallbackWithOpts(resolver types.CLIResolver, cli, prompt, cwd string, timeout int, model, effort string) types.SpawnArgs {
	if resolver != nil {
		var (
			args types.SpawnArgs
			err  error
		)
		if mr, ok := resolver.(types.ModelledCLIResolver); ok && (model != "" || effort != "") {
			args, err = mr.ResolveSpawnArgsWithOpts(cli, prompt, model, effort)
		} else {
			args, err = resolver.ResolveSpawnArgs(cli, prompt)
		}
		if err == nil {
			args.CWD = cwd
			args.TimeoutSeconds = timeout
			return args
		}
	}
	return types.SpawnArgs{
		CLI:            cli,
		Command:        cli,
		Args:           []string{"-p", prompt},
		CWD:            cwd,
		TimeoutSeconds: timeout,
	}
}
