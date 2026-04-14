package workers

import (
	"context"
	"fmt"
	"time"

	"github.com/thebtf/aimux/pkg/loom"
	"github.com/thebtf/aimux/pkg/types"
)

// InvestigatorWorker dispatches investigate "auto" actions via CLI executor.
// Non-auto actions (start, finding, assess, report) are handled in-process
// by the server handler and do NOT go through Loom.
type InvestigatorWorker struct {
	executor types.Executor
	resolver types.CLIResolver
}

// NewInvestigatorWorker creates an InvestigatorWorker.
func NewInvestigatorWorker(exec types.Executor, resolver types.CLIResolver) *InvestigatorWorker {
	return &InvestigatorWorker{executor: exec, resolver: resolver}
}

func (w *InvestigatorWorker) Type() loom.WorkerType { return loom.WorkerTypeInvestigator }

func (w *InvestigatorWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	start := time.Now()

	cli := task.CLI
	if cli == "" {
		cli = "codex" // default investigate CLI
		if c, ok := task.Metadata["cli"].(string); ok && c != "" {
			cli = c
		}
	}

	args, err := w.resolver.ResolveSpawnArgs(cli, task.Prompt)
	if err != nil {
		return nil, fmt.Errorf("investigator worker: resolve args: %w", err)
	}

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

	result, err := w.executor.Run(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("investigator worker: run: %w", err)
	}

	duration := time.Since(start).Milliseconds()

	return &loom.WorkerResult{
		Content:    result.Content,
		Metadata:   map[string]any{"exit_code": result.ExitCode, "cli": cli},
		DurationMS: duration,
	}, nil
}
