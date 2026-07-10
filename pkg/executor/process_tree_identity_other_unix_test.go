//go:build !windows && !linux

package executor_test

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Non-Linux Unix test targets do not expose a portable stable process handle.
// Keep cleanup non-destructive there instead of force-killing a potentially
// reused PID. Linux uses pidfd plus a /proc start-time fingerprint.
type processTreeIdentity struct {
	pid     int
	process *os.Process
}

func captureProcessTreeIdentity(pid int) (*processTreeIdentity, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("find process %d: %w", pid, err)
	}
	return &processTreeIdentity{pid: pid, process: process}, nil
}

func processTreeProcessAlive(identity *processTreeIdentity) bool {
	if identity == nil || identity.process == nil {
		return false
	}
	err := identity.process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func processTreeForceKill(identity *processTreeIdentity) error {
	if identity == nil {
		return nil
	}
	return fmt.Errorf("stable force-kill identity is unavailable for process %d on this Unix target", identity.pid)
}

func closeProcessTreeIdentity(identity *processTreeIdentity) {
	if identity == nil || identity.process == nil {
		return
	}
	_ = identity.process.Release()
	identity.process = nil
}
