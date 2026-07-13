package server

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/thebtf/aimux/pkg/executor"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

func (s *Server) taskWorkerRuntime() (*workerruntime.WorkerRuntime, *swarm.Swarm, error) {
	if s == nil {
		return nil, nil, errors.New("task runtime: server is nil")
	}
	s.taskRuntimeMu.Lock()
	defer s.taskRuntimeMu.Unlock()
	if s.taskRuntime != nil && s.taskSwarm != nil {
		return s.taskRuntime, s.taskSwarm, nil
	}
	fabric := swarm.New(func(string) (types.ExecutorV2, error) {
		return executor.NewCLIPipeAdapter(pipeExec.New()), nil
	}, s.auditLog, swarm.WithStatefulTTL(0))
	taskRuntime, err := workerruntime.New(fabric)
	if err != nil {
		return nil, nil, err
	}
	s.taskSwarm = fabric
	s.taskRuntime = taskRuntime
	return taskRuntime, fabric, nil
}

func (s *Server) dispatchTaskRuntime(ctx context.Context, cli string, spawnArgs types.SpawnArgs, sink types.ExecutorEventSink) (*types.Response, error) {
	taskRuntime, fabric, err := s.taskWorkerRuntime()
	if err != nil {
		return nil, err
	}
	scope := spawnArgs.CWD
	handle, err := fabric.Get(ctx, cli, swarm.Stateless, swarm.WithScope(scope))
	if err != nil {
		return nil, err
	}
	if sink == nil {
		sink = types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })
	}
	executionID := types.ExecutionID(uuid.NewString())
	return taskRuntime.Execute(ctx, handle, scope, executionID, types.Message{
		Content: spawnArgs.Stdin,
		Spawn:   &spawnArgs,
	}, sink)
}
