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
// the terminal winner; executors may propose output but cannot publish a second
// terminal or emit after cancellation.
func (s *Swarm) Execute(ctx context.Context, h *Handle, scope string, id types.ExecutionID, msg types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	authority, exec, err := s.executionAuthority(ctx, h, scope)
	if err != nil {
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
		cancel()
		return nil, ErrExecutionActive
	}
	if _, found := s.executions[key]; found {
		s.executionMu.Unlock()
		cancel()
		return nil, ErrExecutionExists
	}
	s.active[h] = id
	s.executions[key] = record
	s.executionMu.Unlock()
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
	terminal := record.terminalEvent(response, err)
	terminalAdmitted := record.publishTerminal(terminal)
	s.complete(record, key)
	if authority.mode == Stateless {
		_ = s.closeHandle(h, "stateless-after-execution")
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
	record.mu.Lock()
	if record.terminal {
		evidence := record.cancellation
		if evidence.ExecutionID == "" {
			evidence.ExecutionID = id
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
	record.cancellation = types.CancellationEvidence{ExecutionID: id}
	cancel := record.cancel
	exec := record.executor
	record.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	evidence := types.CancellationEvidence{ExecutionID: id}
	var cancelErr error
	if canceller, ok := exec.(types.ExecutionCanceller); ok {
		evidence, cancelErr = canceller.CancelExecution(ctx, id, reason)
		if evidence.ExecutionID == "" {
			evidence.ExecutionID = id
		}
	}
	record.mu.Lock()
	record.cancellation = evidence
	record.mu.Unlock()
	admitted := record.publishTerminal(types.ExecutorEvent{Channel: "terminal", Type: "cancelled", Terminal: true})
	if cancelErr != nil {
		return evidence, cancelErr
	}
	if !admitted {
		return evidence, ErrEventAdmissionRejected
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

func (record *executionRecord) terminalEvent(response *types.Response, runErr error) types.ExecutorEvent {
	record.mu.Lock()
	cancelled := record.cancelled
	truncated := record.truncated
	record.mu.Unlock()
	eventType := "completed"
	if cancelled {
		eventType = "cancelled"
	} else if response != nil && response.Error != nil && response.Error.Type == types.ErrorTypeTimeout {
		eventType = "timeout"
	} else if runErr != nil || response != nil && response.ExitCode != 0 {
		eventType = "failed"
	}
	if response != nil && response.Partial {
		truncated = true
	}
	return types.ExecutorEvent{Channel: "terminal", Type: eventType, Terminal: true, Truncated: truncated}
}

func (record *executionRecord) publishTerminal(event types.ExecutorEvent) bool {
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.terminal {
		return record.admitted
	}
	record.terminal = true
	event.Terminal = true
	event.Truncated = event.Truncated || record.truncated
	record.admitted = record.sink.TryAdmit(event)
	return record.admitted
}

func (s *Swarm) complete(record *executionRecord, key executionKey) {
	s.executionMu.Lock()
	if activeID, ok := s.active[record.handle]; ok && activeID == record.id {
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
