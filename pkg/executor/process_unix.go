//go:build !windows

package executor

import (
	"os"
	"syscall"
	"time"
)

// killUnix sends SIGTERM to the process, then waits up to 5s before sending SIGKILL.
func killUnix(h *ProcessHandle) {
	_ = h.Cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-h.Done:
		return
	case <-time.After(5 * time.Second):
		_ = h.Cmd.Process.Signal(os.Kill)
	}
}
