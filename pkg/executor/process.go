package executor

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// ProcessHandle represents a managed process.
type ProcessHandle struct {
	PID       int
	Cmd       *exec.Cmd
	Stdout    io.ReadCloser
	Stderr    io.ReadCloser
	Done      <-chan error // receives exit error (nil on clean exit) then closes
	ExitCode  int
	StartedAt time.Time

	done    chan error    // internal writable channel
	exited  atomic.Bool  // set to true before Done is signalled; safe for concurrent reads
	mu      sync.Mutex
	cleaned bool
}

// ProcessManager tracks and manages spawned processes.
type ProcessManager struct {
	handles sync.Map // PID -> *ProcessHandle
}

// NewProcessManager creates a ProcessManager.
func NewProcessManager() *ProcessManager {
	return &ProcessManager{}
}

// SharedPM is the global ProcessManager tracking all spawned processes.
// Used by executors for one-shot Run() calls so processes are tracked for
// server shutdown cleanup. Session processes use pipe.SessionProcessManager().
var SharedPM = NewProcessManager()

// Spawn starts a process, sets up stdout/stderr pipes, and begins tracking it.
// The provided cmd must not have Stdout/Stderr set — Spawn sets up the pipes itself.
// Returns a ProcessHandle with PID > 0 on success.
func (pm *ProcessManager) Spawn(cmd *exec.Cmd) (*ProcessHandle, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close() // prevent fd leak
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdout.Close() // prevent fd leak
		stderr.Close() // prevent fd leak
		return nil, fmt.Errorf("start process: %w", err)
	}

	done := make(chan error, 1)
	h := &ProcessHandle{
		PID:       cmd.Process.Pid,
		Cmd:       cmd,
		Stdout:    stdout,
		Stderr:    stderr,
		Done:      done,
		StartedAt: time.Now(),
		done:      done,
	}

	pm.handles.Store(h.PID, h)

	go func() {
		waitErr := cmd.Wait()
		h.mu.Lock()
		if cmd.ProcessState != nil {
			h.ExitCode = cmd.ProcessState.ExitCode()
		} else if waitErr != nil {
			h.ExitCode = -1
		}
		h.mu.Unlock()
		// Mark exited BEFORE signalling Done so that IsAlive() observes the
		// post-exit state as soon as <-h.Done unblocks (happens-before guarantee).
		h.exited.Store(true)
		done <- waitErr
		close(done)
	}()

	return h, nil
}

// Kill terminates a process.
// On Windows: immediately kills the process.
// On Unix: sends SIGTERM then waits up to 5s before sending SIGKILL.
func (pm *ProcessManager) Kill(h *ProcessHandle) {
	if h == nil || h.Cmd == nil || h.Cmd.Process == nil {
		return
	}

	if runtime.GOOS == "windows" {
		// Windows does not support SIGTERM; kill immediately.
		_ = h.Cmd.Process.Kill()
	} else {
		killUnix(h)
	}

	// Drain the done channel to unblock the Wait goroutine.
	select {
	case <-h.Done:
	case <-time.After(10 * time.Second):
	}
}

// IsAlive returns true if the process has not yet exited.
// It reads h.exited, which is set atomically before Done is signalled,
// guaranteeing that IsAlive returns false as soon as <-h.Done unblocks.
func (pm *ProcessManager) IsAlive(h *ProcessHandle) bool {
	if h == nil {
		return false
	}
	return !h.exited.Load()
}

// MarkExited atomically marks the handle as exited. Used by external
// reap goroutines (e.g. ConPTY's wrapper around upconpty.ConPty.Wait —
// AIMUX-16 CR-004) that own their child-process lifecycle but plug their
// synthetic ProcessHandle into BaseSession via the same IsAlive contract.
// Without this, IsAlive would always return true for ConPTY-owned handles
// because the standard Spawn() reap goroutine never runs for them.
//
// MarkExited is idempotent — repeated calls are no-ops.
func (h *ProcessHandle) MarkExited() {
	h.exited.Store(true)
}

// Cleanup removes a handle from tracking and marks it as cleaned up.
func (pm *ProcessManager) Cleanup(h *ProcessHandle) {
	if h == nil {
		return
	}
	pm.handles.Delete(h.PID)
	h.mu.Lock()
	h.cleaned = true
	h.mu.Unlock()
}

// Shutdown kills all tracked processes and removes them from tracking.
func (pm *ProcessManager) Shutdown() {
	pm.handles.Range(func(_, value any) bool {
		h, ok := value.(*ProcessHandle)
		if ok {
			pm.Kill(h)
			pm.Cleanup(h)
		}
		return true
	})
}

// GracefulShutdown waits up to timeout for all tracked processes to finish naturally.
// After timeout, remaining processes are killed. Returns the number of processes
// that finished gracefully (vs killed).
func (pm *ProcessManager) GracefulShutdown(timeout time.Duration) int {
	// Collect all live handles
	var handles []*ProcessHandle
	pm.handles.Range(func(_, value any) bool {
		if h, ok := value.(*ProcessHandle); ok && pm.IsAlive(h) {
			handles = append(handles, h)
		}
		return true
	})

	if len(handles) == 0 {
		return 0
	}

	// Wait for processes to finish naturally, up to timeout
	graceful := 0
	deadline := time.After(timeout)
	remaining := make([]*ProcessHandle, len(handles))
	copy(remaining, handles)

	for len(remaining) > 0 {
		select {
		case <-deadline:
			// Timeout — kill remaining
			for _, h := range remaining {
				pm.Kill(h)
				pm.Cleanup(h)
			}
			return graceful
		default:
			// Check which processes finished
			var stillAlive []*ProcessHandle
			for _, h := range remaining {
				if pm.IsAlive(h) {
					stillAlive = append(stillAlive, h)
				} else {
					graceful++
					pm.Cleanup(h)
				}
			}
			remaining = stillAlive
			if len(remaining) > 0 {
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	return graceful
}
