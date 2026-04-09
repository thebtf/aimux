package executor

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sync"
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

	done    chan error // internal writable channel
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
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
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
func (pm *ProcessManager) IsAlive(h *ProcessHandle) bool {
	if h == nil {
		return false
	}
	select {
	case <-h.Done:
		return false
	default:
		return true
	}
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
