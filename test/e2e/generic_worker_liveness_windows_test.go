//go:build windows

package e2e

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// t016ProcessIdentity is a test-only stable OS process handle used to prove
// actual process death after T016 generic-worker cancel/timeout scenarios.
// A completed Wait on the immediate child process is not tree-death proof;
// each captured identity must be independently checked after the fact.
type t016ProcessIdentity struct {
	pid    int
	handle windows.Handle
}

func captureT016ProcessIdentity(pid int) (*t016ProcessIdentity, error) {
	handle, err := windows.OpenProcess(
		windows.SYNCHRONIZE|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND) {
			return nil, nil
		}
		return nil, fmt.Errorf("open process %d for stable test identity: %w", pid, err)
	}
	return &t016ProcessIdentity{pid: pid, handle: handle}, nil
}

func t016ProcessAlive(identity *t016ProcessIdentity) bool {
	if identity == nil || identity.handle == 0 {
		return false
	}
	status, err := windows.WaitForSingleObject(identity.handle, 0)
	return err == nil && status == uint32(windows.WAIT_TIMEOUT)
}

func t016ForceKillProcess(identity *t016ProcessIdentity) error {
	if identity == nil || identity.handle == 0 {
		return nil
	}
	status, err := windows.WaitForSingleObject(identity.handle, 0)
	if err != nil {
		return fmt.Errorf("check process %d before force kill: %w", identity.pid, err)
	}
	if status != uint32(windows.WAIT_TIMEOUT) {
		return nil
	}
	if err := windows.TerminateProcess(identity.handle, 1); err != nil {
		status, waitErr := windows.WaitForSingleObject(identity.handle, 0)
		if waitErr == nil && status != uint32(windows.WAIT_TIMEOUT) {
			return nil
		}
		return fmt.Errorf("terminate process %d through stable handle: %w", identity.pid, err)
	}
	return nil
}

func closeT016ProcessIdentity(identity *t016ProcessIdentity) {
	if identity == nil || identity.handle == 0 {
		return
	}
	_ = windows.CloseHandle(identity.handle)
	identity.handle = 0
}
