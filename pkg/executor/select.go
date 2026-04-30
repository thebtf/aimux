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

// SelectAvailableCLIs is a pre-select gate that filters a list of CLIs by
// CircuitBreaker state per AIMUX-16 FR-2 / EC-2.1. CLIs whose breakers are
// Open are excluded; CLIs whose breakers are Closed or HalfOpen-with-budget
// are included. The relative order of the input slice is preserved.
//
// If breaker is nil, clis is returned unchanged — keeping the gate optional
// for callers that have not yet adopted breaker tracking.
//
// EC-2.1: when every breaker is Open the result is an empty slice; the caller
// is expected to surface a structured "all CLIs unavailable" error rather
// than fall through to a silent failure.
//
// EC-2.2: AvailableCLIs uses CanAllow() under the hood, which is read-only
// (does not advance HalfOpen probe counters), so this gate never races with
// a live HalfOpen probe initiated elsewhere.
func SelectAvailableCLIs(clis []string, breaker *BreakerRegistry) []string {
	if breaker == nil || len(clis) == 0 {
		return clis
	}
	return breaker.AvailableCLIs(clis)
}

