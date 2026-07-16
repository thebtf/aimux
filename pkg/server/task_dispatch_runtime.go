package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/picker"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

var errTaskRuntimeClosed = errors.New("task runtime: server is shutting down")

func (s *Server) taskWorkerRuntime() (*workerruntime.WorkerRuntime, *swarm.Swarm, error) {
	if s == nil {
		return nil, nil, errors.New("task runtime: server is nil")
	}
	s.taskRuntimeMu.Lock()
	defer s.taskRuntimeMu.Unlock()
	if s.taskRuntimeClosed {
		return nil, nil, errTaskRuntimeClosed
	}
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

// taskBindingCoordinatorFor lazily constructs and caches the server-owned
// CR-003 binding coordinator bound to fabric/taskRuntime. It reuses
// taskRuntimeMu so the coordinator is always constructed alongside (and
// cached with) the Swarm fabric and WorkerRuntime it wraps.
func (s *Server) taskBindingCoordinatorFor(fabric *swarm.Swarm, taskRuntime *workerruntime.WorkerRuntime) (*taskBindingCoordinator, error) {
	if s == nil {
		return nil, errors.New("task runtime: server is nil")
	}
	engine := s.currentLoom()
	if engine == nil {
		return nil, errors.New("task binding: loom is unavailable")
	}
	s.taskRuntimeMu.Lock()
	defer s.taskRuntimeMu.Unlock()
	if s.taskBindingCoord != nil && s.taskBindingCoordLoom == engine {
		return s.taskBindingCoord, nil
	}
	log := s.log
	s.taskBindingCoord = newTaskBindingCoordinator(engine, fabric, taskRuntime, func(format string, args ...any) {
		if log != nil {
			log.Warn(format, args...)
		}
	})
	s.taskBindingCoordLoom = engine
	return s.taskBindingCoord, nil
}

// dispatchTaskRuntime dispatches one CLI call through the server-owned
// binding coordinator, Swarm execution fabric, and WorkerRuntime (CR-003).
// ident carries the durable task/tenant/project identity and deterministic
// profile/capability fingerprints the coordinator reserves against; it is
// derived from the durable loom.Task record, never from public request
// input, so no public task/review schema widens.
func (s *Server) dispatchTaskRuntime(ctx context.Context, cli string, spawnArgs, sessionLaunchArgs types.SpawnArgs, turnContent string, sink types.ExecutorEventSink, ident TaskBindingIdentity, session taskSessionRequest) (*types.Response, error) {
	taskRuntime, fabric, err := s.taskWorkerRuntime()
	if err != nil {
		return nil, err
	}
	coordinator, err := s.taskBindingCoordinatorFor(fabric, taskRuntime)
	if err != nil {
		return nil, err
	}
	scope, err := canonicalWorktreeRoot(spawnArgs.CWD)
	if err != nil {
		return nil, fmt.Errorf("task runtime: canonicalize worktree: %w", err)
	}
	spawnArgs.CWD = scope
	sessionLaunchArgs.CWD = scope
	if sink == nil {
		sink = types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })
	}
	msg := types.Message{
		Content: turnContent,
		Spawn:   &spawnArgs,
	}
	return coordinator.dispatch(ctx, cli, scope, ident, session, []swarm.GetOption{
		swarm.WithScope(scope),
		swarm.WithSessionArgs(sessionLaunchArgs),
	}, func(execCtx context.Context, binding swarm.LiveSessionBinding, executionID types.ExecutionID) (*types.Response, bool, error) {
		return taskRuntime.ExecuteSessionBinding(execCtx, binding, executionID, msg, sink)
	})
}

const taskWorkerSessionRequestMetadata = "worker_session_request"

type taskSessionRequestMetadata struct {
	Mode                  types.SessionBindingMode      `json:"mode"`
	WorkerSessionID       string                        `json:"worker_session_id"`
	ParentWorkerSessionID string                        `json:"parent_worker_session_id,omitempty"`
	Expected              *types.SessionBindingIdentity `json:"expected,omitempty"`
	Parent                *types.SessionBindingIdentity `json:"parent,omitempty"`
}

func (r taskSessionRequest) isZero() bool {
	return r.Mode == "" && r.WorkerSessionID == "" && r.ParentWorkerSessionID == "" && r.Expected == nil && r.Parent == nil
}

func (r taskSessionRequest) allowsRecipeReplay() bool {
	return r.Mode == "" || r.Mode == types.SessionBindingModeStateless
}

func (r taskSessionRequest) validateInternal() error {
	mode := r.Mode
	if mode == "" {
		mode = types.SessionBindingModeStateless
	}
	if _, err := r.runtimeMode(); err != nil {
		return err
	}
	if err := r.bindingRequest().Validate(); err != nil {
		return err
	}
	canonicalID := func(name, value string) error {
		if value == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s must be nonblank and canonical", name)
		}
		return nil
	}
	switch mode {
	case types.SessionBindingModeStateless:
		if r.WorkerSessionID != "" || r.ParentWorkerSessionID != "" {
			return fmt.Errorf("stateless session request must not include durable session IDs")
		}
	case types.SessionBindingModeNew, types.SessionBindingModeExactResume:
		if err := canonicalID("worker session ID", r.WorkerSessionID); err != nil {
			return err
		}
		if r.ParentWorkerSessionID != "" {
			return fmt.Errorf("%s session request must not include a parent Worker Session ID", mode)
		}
	case types.SessionBindingModeFork:
		if err := canonicalID("worker session ID", r.WorkerSessionID); err != nil {
			return err
		}
		if err := canonicalID("parent Worker Session ID", r.ParentWorkerSessionID); err != nil {
			return err
		}
		if r.WorkerSessionID == r.ParentWorkerSessionID {
			return fmt.Errorf("fork child and parent Worker Session IDs must differ")
		}
	}
	return nil
}

func taskSessionRequestMetadataValue(r taskSessionRequest) map[string]any {
	return map[string]any{
		"mode":                     r.Mode,
		"worker_session_id":        r.WorkerSessionID,
		"parent_worker_session_id": r.ParentWorkerSessionID,
		"expected":                 r.Expected,
		"parent":                   r.Parent,
	}
}

func taskSessionRequestFromMetadata(metadata map[string]any) (taskSessionRequest, error) {
	var zero taskSessionRequest
	raw, ok := metadata[taskWorkerSessionRequestMetadata]
	if !ok {
		return zero, nil
	}
	if raw == nil {
		return zero, fmt.Errorf("%s metadata must not be null", taskWorkerSessionRequestMetadata)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return zero, fmt.Errorf("encode %s metadata: %w", taskWorkerSessionRequestMetadata, err)
	}
	var payload taskSessionRequestMetadata
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return zero, fmt.Errorf("decode %s metadata: %w", taskWorkerSessionRequestMetadata, err)
	}
	if payload.Mode == "" {
		return zero, fmt.Errorf("%s metadata requires an explicit mode", taskWorkerSessionRequestMetadata)
	}
	request := taskSessionRequest{
		Mode:                  payload.Mode,
		WorkerSessionID:       payload.WorkerSessionID,
		ParentWorkerSessionID: payload.ParentWorkerSessionID,
		Expected:              payload.Expected,
		Parent:                payload.Parent,
	}
	if err := request.validateInternal(); err != nil {
		return zero, fmt.Errorf("invalid %s metadata: %w", taskWorkerSessionRequestMetadata, err)
	}
	return request, nil
}

func applyTaskSessionRequestToSpec(spec *picker.TaskSpec, request taskSessionRequest) {
	if spec == nil {
		return
	}
	spec.WorkerSessionID = request.WorkerSessionID
	spec.ParentWorkerSessionID = request.ParentWorkerSessionID
	spec.SessionBinding = types.SessionBindingRequest{
		Mode:     request.Mode,
		Expected: request.Expected,
		Parent:   request.Parent,
	}
}

func taskSessionRequestFromSpec(spec picker.TaskSpec) taskSessionRequest {
	return taskSessionRequest{
		Mode:                  spec.SessionBinding.Mode,
		WorkerSessionID:       spec.WorkerSessionID,
		ParentWorkerSessionID: spec.ParentWorkerSessionID,
		Expected:              spec.SessionBinding.Expected,
		Parent:                spec.SessionBinding.Parent,
	}
}
