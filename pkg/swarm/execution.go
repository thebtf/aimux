package swarm

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

var (
	ErrExecutionNotFound      = errors.New("swarm: execution not found")
	ErrExecutionActive        = errors.New("swarm: handle already has an active execution")
	ErrExecutionExists        = errors.New("swarm: execution already exists")
	ErrEventAdmissionRejected = errors.New("swarm: terminal event admission rejected")
)

const maxTerminalExecutions = 256

// ExecutionInspection is an in-memory, fenced snapshot. It intentionally
// carries no durable binding; CR-003 owns recovery and persistence.
type ExecutionInspection struct {
	ExecutionID types.ExecutionID
	Terminal    bool
	Cancelled   bool
}

type handleAuthority struct {
	tenantID   string
	scope      string
	generation uint64
	mode       SpawnMode
}

type executionKey struct {
	handle     *Handle
	generation uint64
	id         types.ExecutionID
}

type executionRecord struct {
	handle       *Handle
	id           types.ExecutionID
	tenantID     string
	scope        string
	generation   uint64
	executor     types.ExecutorV2
	sink         types.ExecutorEventSink
	cancel       context.CancelFunc
	cancellation types.CancellationEvidence
	terminal     bool
	cancelled    bool
	truncated    bool
	admitted     bool
	mu           sync.Mutex
}

// Execute admits one exact execution for a live handle generation. Swarm owns
// the terminal winner; executors may propose output but cannot publish a
// terminal. Cancellation starts process shutdown, while Execute keeps the
// admission window open until the executor has drained and returned.
func (s *Swarm) Execute(ctx context.Context, h *Handle, scope string, id types.ExecutionID, msg types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if h == nil {
		return nil, ErrExecutionNotFound
	}
	if !h.tryAcquireOperation() {
		if !s.accepting() {
			return nil, ErrSwarmShutdown
		}
		return nil, ErrExecutionActive
	}
	defer h.releaseOperation()
	s.lifecycleMu.RLock()
	if s.shuttingDown {
		s.lifecycleMu.RUnlock()
		return nil, ErrSwarmShutdown
	}
	authority, exec, err := s.executionAuthority(ctx, h, scope)
	if err != nil {
		s.lifecycleMu.RUnlock()
		return nil, err
	}
	if sink == nil {
		sink = types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })
	}
	execCtx, cancel := context.WithCancel(ctx)
	record := &executionRecord{
		handle:     h,
		id:         id,
		tenantID:   authority.tenantID,
		scope:      authority.scope,
		generation: authority.generation,
		executor:   exec,
		sink:       sink,
		cancel:     cancel,
	}
	key := executionKey{handle: h, generation: authority.generation, id: id}
	s.executionMu.Lock()
	if _, found := s.active[h]; found {
		s.executionMu.Unlock()
		s.lifecycleMu.RUnlock()
		cancel()
		return nil, ErrExecutionActive
	}
	if _, found := s.executions[key]; found {
		s.executionMu.Unlock()
		s.lifecycleMu.RUnlock()
		cancel()
		return nil, ErrExecutionExists
	}
	s.active[h] = record
	s.executions[key] = record
	s.executionMu.Unlock()
	s.lifecycleMu.RUnlock()
	defer cancel()

	h.mu.Lock()
	h.lastUsedAt = time.Now()
	h.mu.Unlock()

	admission := types.ExecutorEventSinkFunc(record.tryAdmit)
	var response *types.Response
	if native, ok := exec.(types.EventExecutor); ok {
		response, err = native.SendEvents(execCtx, id, msg, admission)
	} else {
		response, err = exec.SendStream(execCtx, msg, func(chunk types.Chunk) {
			if !chunk.Done {
				admission.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "text-only", Content: []byte(chunk.Content)})
			}
		})
	}
	if record.wasTruncated() {
		if response == nil {
			response = &types.Response{}
		}
		response.Partial = true
	}
	terminalAdmitted := record.finalizeTerminal(response, err)
	s.complete(record, key)
	h.mu.Lock()
	h.lastUsedAt = time.Now()
	h.mu.Unlock()
	if authority.mode == Stateless {
		_ = s.closeHandleLocked(h, "stateless-after-execution")
	}
	if !terminalAdmitted && err == nil {
		err = ErrEventAdmissionRejected
	}
	return response, err
}

// Cancel affects only the exact execution, scope, and current handle
// generation. Context cancellation is the common process-control path; native
// acknowledgement is additional evidence.
func (s *Swarm) Cancel(ctx context.Context, h *Handle, scope string, id types.ExecutionID, reason string) (types.CancellationEvidence, error) {
	record, err := s.execution(ctx, h, scope, id)
	if err != nil {
		return types.CancellationEvidence{}, err
	}
	return s.cancelRecord(ctx, record, reason)
}

func (s *Swarm) cancelRecord(ctx context.Context, record *executionRecord, reason string) (types.CancellationEvidence, error) {
	record.mu.Lock()
	if record.terminal {
		evidence := record.cancellation
		if evidence.ExecutionID == "" {
			evidence.ExecutionID = record.id
		}
		record.mu.Unlock()
		return evidence, nil
	}
	if record.cancelled {
		evidence := record.cancellation
		record.mu.Unlock()
		return evidence, nil
	}
	record.cancelled = true
	record.cancellation = types.CancellationEvidence{ExecutionID: record.id}
	cancel := record.cancel
	exec := record.executor
	record.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	evidence := types.CancellationEvidence{ExecutionID: record.id}
	var cancelErr error
	if canceller, ok := exec.(types.ExecutionCanceller); ok {
		evidence, cancelErr = canceller.CancelExecution(ctx, record.id, reason)
		if evidence.ExecutionID == "" {
			evidence.ExecutionID = record.id
		}
	}
	record.mu.Lock()
	record.cancellation = evidence
	record.mu.Unlock()
	if cancelErr != nil {
		return evidence, cancelErr
	}
	return evidence, nil
}

// Inspect returns the bounded, fenced in-memory execution snapshot without
// spawning or enumerating durable state.
func (s *Swarm) Inspect(ctx context.Context, h *Handle, scope string, id types.ExecutionID) (ExecutionInspection, error) {
	record, err := s.execution(ctx, h, scope, id)
	if err != nil {
		return ExecutionInspection{}, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	return ExecutionInspection{ExecutionID: id, Terminal: record.terminal, Cancelled: record.cancelled}, nil
}

func (s *Swarm) executionAuthority(ctx context.Context, h *Handle, scope string) (handleAuthority, types.ExecutorV2, error) {
	if h == nil {
		return handleAuthority{}, nil, ErrExecutionNotFound
	}
	s.mu.RLock()
	authority, found := s.live[h]
	s.mu.RUnlock()
	if !found || authority.tenantID != tenantIDFromContext(ctx) || authority.scope != scope {
		return handleAuthority{}, nil, ErrExecutionNotFound
	}
	h.mu.Lock()
	exec := h.executor
	generation := h.generation
	h.mu.Unlock()
	if exec == nil || generation != authority.generation {
		return handleAuthority{}, nil, ErrExecutionNotFound
	}
	return authority, exec, nil
}

func (s *Swarm) execution(ctx context.Context, h *Handle, scope string, id types.ExecutionID) (*executionRecord, error) {
	if h == nil {
		return nil, ErrExecutionNotFound
	}
	h.mu.Lock()
	generation := h.generation
	h.mu.Unlock()
	key := executionKey{handle: h, generation: generation, id: id}
	s.executionMu.Lock()
	record := s.executions[key]
	s.executionMu.Unlock()
	if record == nil || record.tenantID != tenantIDFromContext(ctx) || record.scope != scope {
		return nil, ErrExecutionNotFound
	}
	return record, nil
}

func (record *executionRecord) tryAdmit(event types.ExecutorEvent) bool {
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.terminal {
		return false
	}
	if event.Terminal {
		return true
	}
	accepted := record.sink.TryAdmit(event)
	if !accepted {
		record.truncated = true
	}
	return accepted
}

func (record *executionRecord) finalizeTerminal(response *types.Response, runErr error) bool {
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.terminal {
		return record.admitted
	}
	if response != nil && response.Partial {
		record.truncated = true
	}
	eventType := "completed"
	if record.cancelled {
		eventType = "cancelled"
	} else if response != nil && response.Error != nil && response.Error.Type == types.ErrorTypeTimeout {
		eventType = "timeout"
	} else if runErr != nil || response != nil && response.ExitCode != 0 {
		eventType = "failed"
	}
	record.terminal = true
	record.admitted = record.sink.TryAdmit(types.ExecutorEvent{
		Channel:   "terminal",
		Type:      eventType,
		Terminal:  true,
		Truncated: record.truncated,
	})
	return record.admitted
}

func (record *executionRecord) wasTruncated() bool {
	record.mu.Lock()
	defer record.mu.Unlock()
	return record.truncated
}

func (s *Swarm) complete(record *executionRecord, key executionKey) {
	s.executionMu.Lock()
	if activeRecord, ok := s.active[record.handle]; ok && activeRecord == record {
		delete(s.active, record.handle)
	}
	s.terminalOrder = append(s.terminalOrder, key)
	for len(s.terminalOrder) > maxTerminalExecutions {
		oldest := s.terminalOrder[0]
		s.terminalOrder = s.terminalOrder[1:]
		delete(s.executions, oldest)
	}
	s.executionMu.Unlock()
}
