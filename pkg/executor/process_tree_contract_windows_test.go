//go:build windows

package executor_test

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

type processTreeIdentity struct {
	pid    int
	handle windows.Handle
}

func captureProcessTreeIdentity(pid int) (*processTreeIdentity, error) {
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
	return &processTreeIdentity{pid: pid, handle: handle}, nil
}

func processTreeProcessAlive(identity *processTreeIdentity) bool {
	if identity == nil || identity.handle == 0 {
		return false
	}
	status, err := windows.WaitForSingleObject(identity.handle, 0)
	return err == nil && status == uint32(windows.WAIT_TIMEOUT)
}

func processTreeForceKill(identity *processTreeIdentity) error {
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

func closeProcessTreeIdentity(identity *processTreeIdentity) {
	if identity == nil || identity.handle == 0 {
		return
	}
	_ = windows.CloseHandle(identity.handle)
	identity.handle = 0
}
