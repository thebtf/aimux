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

func (r *WorkerRuntime) Execute(ctx context.Context, h *swarm.Handle, scope string, id types.ExecutionID, msg types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	response, err := r.swarm.Execute(ctx, h, scope, id, msg, sink)
	if source, ok := sink.(interface{ Err() error }); ok {
		if sinkErr := source.Err(); sinkErr != nil {
			return response, sinkErr
		}
	}
	return response, err
}

func (r *WorkerRuntime) Cancel(ctx context.Context, h *swarm.Handle, scope string, id types.ExecutionID, reason string) (types.CancellationEvidence, error) {
	return r.swarm.Cancel(ctx, h, scope, id, reason)
}

func (r *WorkerRuntime) Inspect(ctx context.Context, h *swarm.Handle, scope string, id types.ExecutionID) (swarm.ExecutionInspection, error) {
	return r.swarm.Inspect(ctx, h, scope, id)
}

// InspectSuppliedEvidence classifies explicit process evidence without
// discovering or executing work.
func (r *WorkerRuntime) InspectSuppliedEvidence(evidence *swarm.SuppliedProcessEvidence) swarm.SuppliedEvidenceInspection {
	return r.swarm.InspectSuppliedEvidence(evidence)
}
