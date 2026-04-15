package aimuxworkers

import (
	"context"

	"github.com/thebtf/aimux/loom"
	workers "github.com/thebtf/aimux/loom/workers"
	"github.com/thebtf/aimux/pkg/types"
)

// CLIWorker adapts the CLI executor to the loom.Worker interface via loom/workers.SubprocessBase.
type CLIWorker struct{ base *workers.SubprocessBase }

// NewCLIWorker creates a CLI worker composing SubprocessBase with an aimux spawn resolver.
func NewCLIWorker(exec types.Executor, resolver types.CLIResolver) *CLIWorker {
	return &CLIWorker{base: &workers.SubprocessBase{
		Resolver: &cliSpawnResolver{executor: exec, resolver: resolver},
		Runner:   &cliRunner{executor: exec},
	}}
}

// Type returns loom.WorkerTypeCLI.
func (w *CLIWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }

// Execute delegates to SubprocessBase which calls cliSpawnResolver.Resolve then cliRunner.Run.
func (w *CLIWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	return w.base.Run(ctx, task)
}
