package executor

import (
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const preExitDrainTimeout = 2 * time.Second

// ProcessHandle represents a managed process.
type ProcessHandle struct {
	PID       int
	Cmd       *exec.Cmd
	Stdout    io.ReadCloser
	Stderr    io.ReadCloser
	Done      <-chan error // receives exit error (nil on clean exit) then closes
	ExitCode  int
	StartedAt time.Time

	done                 chan error  // internal writable channel
	exited               atomic.Bool // set to true before Done is signalled; safe for concurrent reads
	mu                   sync.Mutex
	cleaned              bool
	drainDone            chan struct{} // optional: closed when stdout/stderr readers are fully drained
	drained              bool
	drainBeforeTerminate bool
	preExitDrainTimedOut atomic.Bool
	processTree          *processTree // non-nil only for processes started by Spawn
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
	return pm.spawn(cmd, false)
}

// SpawnWithDrain starts a process whose natural-exit path gives transport
// readers one bounded window to drain before descendant-tree termination.
// ProcessManager remains the sole Wait/process-tree owner.
func (pm *ProcessManager) SpawnWithDrain(cmd *exec.Cmd) (*ProcessHandle, error) {
	return pm.spawn(cmd, true)
}

func (pm *ProcessManager) spawn(cmd *exec.Cmd, drainBeforeTerminate bool) (*ProcessHandle, error) {
	tree, err := prepareProcessTree(cmd)
	if err != nil {
		return nil, fmt.Errorf("prepare process tree: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		tree.discard()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close() // prevent fd leak
		tree.discard()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdout.Close() // prevent fd leak
		stderr.Close() // prevent fd leak
		tree.discard()
		return nil, fmt.Errorf("start process: %w", err)
	}
	if err := tree.attach(cmd); err != nil {
		// Assignment failure is a spawn failure: terminate anything that may
		// have been attached, then kill and reap the root as a fallback.
		tree.terminate()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("attach process tree: %w", err)
	}

	done := make(chan error, 1)
	h := &ProcessHandle{
		PID:                  cmd.Process.Pid,
		Cmd:                  cmd,
		Stdout:               stdout,
		Stderr:               stderr,
		Done:                 done,
		StartedAt:            time.Now(),
		done:                 done,
		processTree:          tree,
		drainBeforeTerminate: drainBeforeTerminate,
	}
	if drainBeforeTerminate {
		h.drainDone = make(chan struct{})
	}

	pm.handles.Store(h.PID, h)

	go func() {
		state, waitErr := cmd.Process.Wait()
		if state != nil {
			cmd.ProcessState = state
			if state.ExitCode() != 0 && waitErr == nil {
				waitErr = &exec.ExitError{ProcessState: state}
			}
		}

		h.mu.Lock()
		if cmd.ProcessState != nil {
			h.ExitCode = cmd.ProcessState.ExitCode()
		} else if waitErr != nil {
			h.ExitCode = -1
		}
		drainDone := h.drainDone
		waitForDrain := h.drainBeforeTerminate
		h.mu.Unlock()
		if waitForDrain && drainDone != nil {
			select {
			case <-drainDone:
			case <-time.After(preExitDrainTimeout):
				h.preExitDrainTimedOut.Store(true)
			}
		}
		// Done represents the managed lifetime, so release the owned tree
		// before publishing root completion. On Windows, closing the Job Object
		// kills remaining descendants; on Unix, the process group is drained.
		h.processTree.terminate()
		// Mark exited BEFORE signalling Done so that IsAlive() observes the
		// post-exit state as soon as <-h.Done unblocks (happens-before guarantee).
		h.exited.Store(true)
		done <- waitErr
		close(done)
	}()

	return h, nil
}

// Kill terminates a process and every descendant owned by Spawn. Synthetic
// handles that were not created by Spawn retain the direct-process fallback.
func (pm *ProcessManager) Kill(h *ProcessHandle) {
	if h == nil || h.Cmd == nil || h.Cmd.Process == nil {
		return
	}

	if h.processTree != nil {
		h.processTree.terminate()
	} else {
		killRootProcess(h)
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

// TreeStopped reports the ProcessManager-owned termination observation for the
// exact tree started with h. Root exit alone is not whole-tree evidence.
func (h *ProcessHandle) TreeStopped() bool {
	return h != nil && h.processTree != nil && h.processTree.stopped()
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

// ArmDrainWait makes Cleanup wait briefly for the handle's stdout/stderr
// reader owner to finish draining before Cleanup closes those readers.
func (h *ProcessHandle) ArmDrainWait() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.drainDone == nil && !h.drained {
		h.drainDone = make(chan struct{})
	}
}

// MarkDrained releases Cleanup once the handle's stdout/stderr readers have
// finished draining. It is safe to call multiple times.
func (h *ProcessHandle) MarkDrained() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.drained {
		return
	}
	h.drained = true
	if h.drainDone != nil {
		close(h.drainDone)
	}
}

// PreExitDrainTimedOut reports that the root exited while a descendant still
// retained stdout/stderr past the bounded drain window. The process tree is
// terminated in that case, so callers must report the captured output as
// partial even when the root itself exited successfully.
func (h *ProcessHandle) PreExitDrainTimedOut() bool {
	return h != nil && h.preExitDrainTimedOut.Load()
}

// Cleanup removes a handle from tracking and marks it as cleaned up.
func (pm *ProcessManager) Cleanup(h *ProcessHandle) {
	if h == nil {
		return
	}

	h.mu.Lock()
	if h.cleaned {
		h.mu.Unlock()
		return
	}
	h.cleaned = true
	drainDone := h.drainDone
	h.mu.Unlock()

	// Releasing an owned tree is part of cleanup. This is idempotent with Kill
	// and the natural-exit reap path, and is intentionally absent for synthetic
	// externally-owned handles.
	if h.processTree != nil {
		h.processTree.terminate()
	}

	if drainDone != nil {
		select {
		case <-drainDone:
		case <-time.After(10 * time.Second):
		}
	}

	pm.handles.Delete(h.PID)
	if h.Stdout != nil {
		_ = h.Stdout.Close()
	}
	if h.Stderr != nil {
		_ = h.Stderr.Close()
	}
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
