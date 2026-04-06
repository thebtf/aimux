package orchestrator

import "github.com/thebtf/aimux/pkg/types"

// resolveOrFallback resolves SpawnArgs from CLI profile when a resolver is available.
// Falls back to legacy behavior (Command=cli, Args=["-p", prompt]) when resolver is nil
// or returns an error, ensuring backward compatibility with tests using mock executors.
func resolveOrFallback(resolver types.CLIResolver, cli, prompt, cwd string, timeout int) types.SpawnArgs {
	if resolver != nil {
		args, err := resolver.ResolveSpawnArgs(cli, prompt)
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
