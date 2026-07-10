//go:build !windows

package executor_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
)

func TestProcessManager_SpawnRejectsCallerOwnedProcessGroup(t *testing.T) {
	processManager := executor.NewProcessManager()
	markerPath := filepath.Join(t.TempDir(), "started")
	command := exec.Command(os.Args[0], "-test.run=^TestProcessTreeContractHelper$", "-test.count=1")
	command.Env = append(
		os.Environ(),
		processTreeRoleEnv+"=marker",
		processTreeMarkerEnv+"="+markerPath,
	)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: syscall.Getpgrp()}

	handle, err := processManager.Spawn(command)
	if err == nil {
		select {
		case <-handle.Done:
		case <-time.After(5 * time.Second):
			processManager.Kill(handle)
		}
		processManager.Cleanup(handle)
		t.Fatal("Spawn joined a caller-owned process group")
	}
	if !strings.Contains(err.Error(), "caller-owned process group") {
		t.Fatalf("Spawn error = %q, expected caller-owned process group rejection", err)
	}
	if _, statErr := os.Stat(markerPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected process started; marker stat error = %v", statErr)
	}
}
