//go:build !windows && !linux

package e2e

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// t016ProcessIdentity is a test-only stable process handle used to prove
// actual process death after T016 generic-worker cancel/timeout scenarios.
// Non-Linux Unix targets do not expose a portable stable process handle, so
// cleanup here stays non-destructive instead of force-killing a potentially
// reused PID; Linux uses pidfd plus a /proc start-time fingerprint.
type t016ProcessIdentity struct {
	pid     int
	process *os.Process
}

func captureT016ProcessIdentity(pid int) (*t016ProcessIdentity, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("find process %d: %w", pid, err)
	}
	return &t016ProcessIdentity{pid: pid, process: process}, nil
}

func t016ProcessAlive(identity *t016ProcessIdentity) bool {
	if identity == nil || identity.process == nil {
		return false
	}
	err := identity.process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// t016ForceKillProcess never sends a real signal on this target: a
// destructive signal risks hitting an unrelated process that has since
// reused this PID. Instead it waits past the fixture's own known self-exit
// bound using only the liveness poll (a signal-0 existence probe never
// mutates process state); still alive after that bound is a genuine
// cleanup failure surfaced to the caller, not something to paper over by
// guessing at a kill.
func t016ForceKillProcess(identity *t016ProcessIdentity) error {
	if identity == nil {
		return nil
	}
	if waitForT016ProcessExit(identity, t016TreeFixtureSelfExitBound) {
		return nil
	}
	return fmt.Errorf("process %d did not self-exit within %s; force-kill by signal is unavailable on this Unix target because the PID may have been reused", identity.pid, t016TreeFixtureSelfExitBound)
}

func closeT016ProcessIdentity(identity *t016ProcessIdentity) {
	if identity == nil || identity.process == nil {
		return
	}
	_ = identity.process.Release()
	identity.process = nil
}
