// Package pty implements the Unix PTY executor for Linux and macOS.
// Uses creack/pty for pseudo-terminal allocation, providing unbuffered
// text output similar to ConPTY on Windows.
package pty

import (
	"context"
	"fmt"
	"runtime"

	"github.com/thebtf/aimux/pkg/types"
)

// Executor spawns CLI processes via Unix PTY for unbuffered text output.
type Executor struct {
	available bool
}

// New creates a PTY executor. Checks platform support on creation.
func New() *Executor {
	return &Executor{
		available: runtime.GOOS == "linux" || runtime.GOOS == "darwin",
	}
}

// Name returns the executor name.
func (e *Executor) Name() string { return "pty" }

// Available returns true on Linux and macOS.
func (e *Executor) Available() bool { return e.available }

// Run executes a single prompt via PTY and returns the result.
func (e *Executor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	if !e.available {
		return nil, types.NewExecutorError("PTY not available on this platform", nil, "")
	}

	// TODO: Full PTY implementation via github.com/creack/pty
	// pty.Start(cmd) → read from pty file descriptor → parse text output
	//
	// Implementation requires:
	// 1. exec.Command with args
	// 2. pty.Start(cmd) to allocate PTY and start process
	// 3. Read loop on PTY fd with ANSI stripping
	// 4. Timeout/cancel handling via context
	// 5. PTY close on cleanup

	return nil, types.NewExecutorError(
		"PTY executor not yet fully implemented — use pipe executor as fallback",
		nil, "")
}

// Start begins a persistent PTY session.
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	if !e.available {
		return nil, types.NewExecutorError("PTY not available on this platform", nil, "")
	}

	return nil, fmt.Errorf("PTY persistent sessions not yet implemented")
}
