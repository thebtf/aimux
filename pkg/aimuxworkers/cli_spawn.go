package aimuxworkers

import (
	"context"
	"fmt"

	workers "github.com/thebtf/aimux/loom/workers"
	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/types"
)

// metaKeySpawnArgs is the key used in SubprocessSpawn.Meta to pass the full
// resolved types.SpawnArgs to cliRunner without losing any fields.
const metaKeySpawnArgs = "spawn_args"

// cliSpawnResolver implements workers.SpawnResolver.
// It resolves task fields through the aimux types.CLIResolver to produce a
// SubprocessSpawn carrying the full types.SpawnArgs in Meta for cliRunner.
type cliSpawnResolver struct {
	executor types.Executor
	resolver types.CLIResolver
}

func (r *cliSpawnResolver) Resolve(ctx context.Context, task *loom.Task) (workers.SubprocessSpawn, error) {
	if r.executor == nil || r.resolver == nil {
		return workers.SubprocessSpawn{}, fmt.Errorf("cli worker: executor or resolver not configured")
	}

	// CLI profile resolution.
	var args types.SpawnArgs
	var err error
	if task.Model != "" || task.Effort != "" {
		if mr, ok := r.resolver.(types.ModelledCLIResolver); ok {
			args, err = mr.ResolveSpawnArgsWithOpts(task.CLI, task.Prompt, task.Model, task.Effort)
		} else {
			args, err = r.resolver.ResolveSpawnArgs(task.CLI, task.Prompt)
		}
	} else {
		args, err = r.resolver.ResolveSpawnArgs(task.CLI, task.Prompt)
	}
	if err != nil {
		return workers.SubprocessSpawn{}, fmt.Errorf("cli worker: resolve args: %w", err)
	}

	// Overrides from task (ProjectContext CWD, Env, Timeout).
	if task.CWD != "" {
		args.CWD = task.CWD
	}
	if len(task.Env) > 0 {
		if args.Env == nil {
			args.Env = make(map[string]string)
		}
		for k, v := range task.Env {
			args.Env[k] = v
		}
	}
	if task.Timeout > 0 {
		args.TimeoutSeconds = task.Timeout
	}

	// Carry the full resolved args in Meta so cliRunner does not need to
	// reconstruct fields like CompletionPattern, InactivitySeconds, Stdin, OnOutput.
	return workers.SubprocessSpawn{
		Command: args.Command,
		Args:    args.Args,
		CWD:     args.CWD,
		Env:     args.Env,
		Stdin:   args.Stdin,
		Meta:    map[string]any{metaKeySpawnArgs: args},
	}, nil
}

// cliRunner implements workers.SubprocessRunner by delegating to types.Executor.Run.
// It retrieves the full types.SpawnArgs from SubprocessSpawn.Meta (placed there by
// cliSpawnResolver) to preserve all profile fields (CompletionPattern, etc.).
type cliRunner struct {
	executor types.Executor
}

func (r *cliRunner) Run(ctx context.Context, spawn workers.SubprocessSpawn) (string, int, error) {
	// Retrieve the full SpawnArgs from Meta to avoid losing profile-resolved fields.
	var args types.SpawnArgs
	if spawn.Meta != nil {
		if sa, ok := spawn.Meta[metaKeySpawnArgs].(types.SpawnArgs); ok {
			args = sa
		}
	}
	// Fallback: reconstruct from SubprocessSpawn if Meta is absent (e.g. in tests).
	if args.Command == "" {
		args = types.SpawnArgs{
			Command: spawn.Command,
			Args:    spawn.Args,
			CWD:     spawn.CWD,
			Env:     spawn.Env,
			Stdin:   spawn.Stdin,
		}
	}

	result, err := r.executor.Run(ctx, args)
	if err != nil {
		return "", 0, err
	}
	if result.Error != nil {
		return result.Content, result.ExitCode, result.Error
	}
	return result.Content, result.ExitCode, nil
}
