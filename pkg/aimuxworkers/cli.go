package aimuxworkers

import (
	"context"
	"fmt"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/types"
)

// CLIWorker adapts the CLI executor to the loom.Worker interface.
type CLIWorker struct {
	executor types.Executor
	resolver types.CLIResolver
}

// NewCLIWorker creates a CLI worker.
func NewCLIWorker(exec types.Executor, resolver types.CLIResolver) *CLIWorker {
	return &CLIWorker{executor: exec, resolver: resolver}
}

func (w *CLIWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }

func (w *CLIWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	if w.executor == nil || w.resolver == nil {
		return nil, fmt.Errorf("cli worker: executor or resolver not configured")
	}

	start := time.Now()

	// Resolve CLI profile to spawn args.
	var args types.SpawnArgs
	var err error

	// Use modelled resolver if model/effort overrides present.
	if task.Model != "" || task.Effort != "" {
		if mr, ok := w.resolver.(types.ModelledCLIResolver); ok {
			args, err = mr.ResolveSpawnArgsWithOpts(task.CLI, task.Prompt, task.Model, task.Effort)
		} else {
			args, err = w.resolver.ResolveSpawnArgs(task.CLI, task.Prompt)
		}
	} else {
		args, err = w.resolver.ResolveSpawnArgs(task.CLI, task.Prompt)
	}
	if err != nil {
		return nil, fmt.Errorf("cli worker: resolve args: %w", err)
	}

	// Override CWD and Env from task (these come from the request/ProjectContext).
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

	result, err := w.executor.Run(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("cli worker: run: %w", err)
	}
	// Executor may return err == nil with result.Error set (partial execution error).
	if result.Error != nil {
		return nil, fmt.Errorf("cli worker: run: %w", result.Error)
	}

	duration := time.Since(start).Milliseconds()

	meta := map[string]any{
		"exit_code": result.ExitCode,
		"cli":       task.CLI,
	}
	if result.DurationMS > 0 {
		meta["cli_duration_ms"] = result.DurationMS
	}

	return &loom.WorkerResult{
		Content:    result.Content,
		Metadata:   meta,
		DurationMS: duration,
	}, nil
}
