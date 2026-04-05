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
func (s *Selector) Select() types.Executor {
	for _, exec := range s.executors {
		if exec.Available() {
			return exec
		}
	}
	return nil
}

