package executor

import (
	"github.com/thebtf/aimux/pkg/types"
)

// Selector chooses the best executor for the current platform.
type Selector struct {
	executors []types.Executor
}

// NewSelector creates an executor selector with the given implementations.
// Order matters: first available executor wins (ConPTY > PTY > Pipe).
func NewSelector(executors ...types.Executor) *Selector {
	return &Selector{executors: executors}
}

// Select returns the best available executor for the current platform.
//
// Deprecated: Use SelectV2(). Will be removed in v6.0.0.
func (s *Selector) Select() types.Executor {
	for _, exec := range s.executors {
		if exec.Available() {
			return exec
		}
	}
	return nil
}

// SelectV2 returns the best available executor wrapped as an ExecutorV2.
// Uses the same priority order as Select (ConPTY > PTY > Pipe) but returns
// the unified v5 interface. Returns nil if no executor is available.
func (s *Selector) SelectV2() types.ExecutorV2 {
	selected := s.Select()
	if selected == nil {
		return nil
	}

	// Map by name to the correct adapter with appropriate capabilities.
	switch selected.Name() {
	case "conpty":
		return NewCLIConPTYAdapter(selected)
	case "pty":
		return NewCLIPTYAdapter(selected)
	default:
		// Pipe is the fallback — also used for any future executor types.
		return NewCLIPipeAdapter(selected)
	}
}

