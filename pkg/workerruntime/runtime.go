package workerruntime

import (
	"context"
	"errors"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

// WorkerRuntime is the generic live-execution facade. It owns no process,
// executor, storage, or provider dependency: Swarm remains the sole authority.
type WorkerRuntime struct{ swarm *swarm.Swarm }

func New(s *swarm.Swarm) (*WorkerRuntime, error) {
	if s == nil {
		return nil, errors.New("worker runtime: swarm must not be nil")
	}
	return &WorkerRuntime{swarm: s}, nil
}

func (r *WorkerRuntime) Execute(ctx context.Context, h *swarm.Handle, id types.ExecutionID, msg types.Message, emit func(ExecutionEnvelope)) (*types.Response, error) {
	return r.swarm.Execute(ctx, h, id, msg, func(event types.ExecutorEvent) {
		emit(ExecutionEnvelope{ExecutionID: id, Event: event})
	})
}

func (r *WorkerRuntime) Cancel(ctx context.Context, h *swarm.Handle, id types.ExecutionID, reason string) (types.CancellationEvidence, error) {
	return r.swarm.Cancel(ctx, h, id, reason)
}

func (r *WorkerRuntime) Inspect(ctx context.Context, h *swarm.Handle, id types.ExecutionID) (swarm.ExecutionInspection, error) {
	return r.swarm.Inspect(ctx, h, id)
}
