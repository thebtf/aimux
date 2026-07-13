package swarm

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

var (
	ErrExecutionNotFound = errors.New("swarm: execution not found")
	ErrExecutionActive   = errors.New("swarm: handle already has an active execution")
)

// ExecutionInspection is an in-memory, fenced snapshot. It intentionally
// carries no durable binding; CR-003 owns recovery and persistence.
type ExecutionInspection struct {
	ExecutionID types.ExecutionID
	Terminal    bool
	Cancelled   bool
}

type executionRecord struct {
	handle    *Handle
	id        types.ExecutionID
	terminal  bool
	cancelled bool
	mu        sync.Mutex
}

// Execute admits one exact execution for a handle generation and streams it
// through the optional native event capability. A terminal state has one winner.
func (s *Swarm) Execute(ctx context.Context, h *Handle, id types.ExecutionID, msg types.Message, emit func(types.ExecutorEvent)) (*types.Response, error) {
	if err := s.checkTenant(ctx, h); err != nil {
		return nil, err
	}
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if err := s.ensureAlive(h); err != nil {
		return nil, ErrExecutionNotFound
	}
	record := &executionRecord{handle: h, id: id}
	s.executionMu.Lock()
	if _, found := s.active[h.ID]; found {
		s.executionMu.Unlock()
		return nil, ErrExecutionActive
	}
	key := executionKey(h, id)
	s.active[h.ID] = id
	s.executions[key] = record
	s.executionMu.Unlock()

	h.mu.Lock()
	exec := h.executor
	h.lastUsedAt = timeNow()
	h.mu.Unlock()
	var resp *types.Response
	var err error
	if native, ok := exec.(types.EventExecutor); ok {
		resp, err = native.SendEvents(ctx, id, msg, emit)
	} else {
		resp, err = exec.SendStream(ctx, msg, func(chunk types.Chunk) {
			if !chunk.Done {
				emit(types.ExecutorEvent{Channel: "stdout", Type: "text-only", Content: []byte(chunk.Content)})
			}
		})
		emit(types.ExecutorEvent{Channel: "terminal", Type: "terminal", Terminal: true})
	}
	s.finish(record)
	if h.Mode == Stateless {
		_ = s.closeHandle(h, "stateless-after-execution")
	}
	return resp, err
}

// Cancel affects only the exact currently active execution on the supplied
// handle. Mismatches intentionally look indistinguishable from not-found.
func (s *Swarm) Cancel(ctx context.Context, h *Handle, id types.ExecutionID, reason string) (types.CancellationEvidence, error) {
	if err := s.checkTenant(ctx, h); err != nil {
		return types.CancellationEvidence{}, err
	}
	record, err := s.execution(ctx, h, id)
	if err != nil {
		return types.CancellationEvidence{}, err
	}
	record.mu.Lock()
	if record.terminal {
		record.mu.Unlock()
		return types.CancellationEvidence{ExecutionID: id}, nil
	}
	record.cancelled, record.terminal = true, true
	record.mu.Unlock()
	h.mu.Lock()
	exec := h.executor
	h.mu.Unlock()
	evidence := types.CancellationEvidence{ExecutionID: id}
	if canceller, ok := exec.(types.ExecutionCanceller); ok {
		var cancelErr error
		evidence, cancelErr = canceller.CancelExecution(ctx, id, reason)
		if cancelErr != nil {
			return evidence, cancelErr
		}
	}
	s.executionMu.Lock()
	delete(s.active, h.ID)
	s.executionMu.Unlock()
	return evidence, nil
}

// Inspect returns the fenced in-memory execution snapshot without spawning or
// enumerating any durable state.
func (s *Swarm) Inspect(ctx context.Context, h *Handle, id types.ExecutionID) (ExecutionInspection, error) {
	if err := s.checkTenant(ctx, h); err != nil {
		return ExecutionInspection{}, err
	}
	record, err := s.execution(ctx, h, id)
	if err != nil {
		return ExecutionInspection{}, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	return ExecutionInspection{ExecutionID: id, Terminal: record.terminal, Cancelled: record.cancelled}, nil
}

func (s *Swarm) execution(_ context.Context, h *Handle, id types.ExecutionID) (*executionRecord, error) {
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	record := s.executions[executionKey(h, id)]
	if record == nil || record.handle != h {
		return nil, ErrExecutionNotFound
	}
	return record, nil
}

func (s *Swarm) finish(record *executionRecord) {
	record.mu.Lock()
	if !record.terminal {
		record.terminal = true
	}
	record.mu.Unlock()
	s.executionMu.Lock()
	delete(s.active, record.handle.ID)
	s.executionMu.Unlock()
}

func executionKey(h *Handle, id types.ExecutionID) string { return h.ID + "\x00" + string(id) }

// timeNow is a seam for compact deterministic tests without introducing clocks
// into the public Swarm API.
var timeNow = func() time.Time { return time.Now() }
