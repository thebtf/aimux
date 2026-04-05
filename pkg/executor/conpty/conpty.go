// Package conpty implements the Windows ConPTY executor.
// Uses CreatePseudoConsole API for unbuffered text output from CLI processes.
// Constitution P4: ConPTY-first, JSON-fallback.
//
// ConPTY provides a pseudo-terminal on Windows, causing CLIs to produce
// line-buffered text output instead of block-buffered JSON. This eliminates
// the 3x JSON serialization overhead measured in v2 benchmarks.
package conpty

import (
	"context"
	"fmt"
	"runtime"

	"github.com/thebtf/aimux/pkg/types"
)

// Executor spawns CLI processes via Windows ConPTY for unbuffered text output.
type Executor struct {
	available bool
}

// New creates a ConPTY executor. Probes for ConPTY support on creation.
func New() *Executor {
	return &Executor{
		available: probeConPTY(),
	}
}

// Name returns the executor name.
func (e *Executor) Name() string { return "conpty" }

// Available returns true if ConPTY is supported on this platform.
// Requires Windows 10 1809+ (build 17763+).
func (e *Executor) Available() bool { return e.available }

// Run executes a single prompt via ConPTY and returns the result.
// The process sees a TTY, producing unbuffered line-oriented output.
func (e *Executor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	if !e.available {
		return nil, types.NewExecutorError("ConPTY not available on this platform", nil, "")
	}

	// TODO: Full ConPTY implementation via golang.org/x/sys/windows
	// CreatePseudoConsole → CreateProcess with PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE
	// → ReadFile on pty output handle → parse text output
	//
	// For now, this is a compile-time placeholder. The actual Windows syscall
	// implementation requires:
	// 1. CreatePipe for pty input/output
	// 2. CreatePseudoConsole with desired console size
	// 3. InitializeProcThreadAttributeList + UpdateProcThreadAttribute
	// 4. CreateProcessW with EXTENDED_STARTUPINFO_PRESENT
	// 5. Read loop on output pipe with ANSI stripping
	// 6. ClosePseudoConsole on cleanup
	//
	// See: https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session

	return nil, types.NewExecutorError(
		"ConPTY executor not yet fully implemented — use pipe executor as fallback",
		nil, "")
}

// Start begins a persistent ConPTY session.
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	if !e.available {
		return nil, types.NewExecutorError("ConPTY not available on this platform", nil, "")
	}

	return nil, fmt.Errorf("ConPTY persistent sessions not yet implemented")
}

// probeConPTY checks if the current platform supports ConPTY.
func probeConPTY() bool {
	if runtime.GOOS != "windows" {
		return false
	}

	// ConPTY requires Windows 10 version 1809 (build 17763) or later.
	// Full implementation would check via RtlGetVersion or
	// kernel32.dll CreatePseudoConsole availability.
	// For now, assume available on Windows (runtime detection deferred to Phase 8).
	return true
}
