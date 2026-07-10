//go:build windows

package executor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const processTreeSuspendedMarkerEnv = "AIMUX_PROCESS_TREE_SUSPENDED_MARKER"

func TestSelectSoleProcessThread(t *testing.T) {
	tests := []struct {
		name       string
		processID  uint32
		entries    []windows.ThreadEntry32
		wantID     uint32
		wantErrSub string
	}{
		{
			name:      "zero candidates",
			processID: 42,
			entries: []windows.ThreadEntry32{
				{ThreadID: 7, OwnerProcessID: 99},
			},
			wantErrSub: "found 0",
		},
		{
			name:      "one candidate",
			processID: 42,
			entries: []windows.ThreadEntry32{
				{ThreadID: 7, OwnerProcessID: 42},
				{ThreadID: 8, OwnerProcessID: 99},
			},
			wantID: 7,
		},
		{
			name:      "multiple candidates",
			processID: 42,
			entries: []windows.ThreadEntry32{
				{ThreadID: 7, OwnerProcessID: 42},
				{ThreadID: 8, OwnerProcessID: 42},
			},
			wantErrSub: "found 2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectSoleProcessThread(test.processID, test.entries)
			if test.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrSub) {
					t.Fatalf("selectSoleProcessThread error = %v, want substring %q", err, test.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectSoleProcessThread: %v", err)
			}
			if got != test.wantID {
				t.Fatalf("selectSoleProcessThread = %d, want %d", got, test.wantID)
			}
		})
	}
}

func TestProcessTreeSuspendedLaunchHelper(t *testing.T) {
	markerPath := os.Getenv(processTreeSuspendedMarkerEnv)
	if markerPath == "" {
		return
	}
	if err := os.WriteFile(markerPath, []byte("executed"), 0o600); err != nil {
		t.Fatalf("write suspended-launch marker: %v", err)
	}
}

func TestPrepareProcessTree_SuspendsUntilJobAssignment(t *testing.T) {
	markerPath := filepath.Join(t.TempDir(), "executed")
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestProcessTreeSuspendedLaunchHelper$",
		"-test.count=1",
	)
	command.Env = append(os.Environ(), processTreeSuspendedMarkerEnv+"="+markerPath)
	originalAttributes := &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
	command.SysProcAttr = originalAttributes

	tree, err := prepareProcessTree(command)
	if err != nil {
		t.Fatalf("prepare process tree: %v", err)
	}
	if err := command.Start(); err != nil {
		tree.discard()
		t.Fatalf("start suspended process: %v", err)
	}
	defer func() {
		tree.terminate()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	}()

	time.Sleep(250 * time.Millisecond)
	if _, statErr := os.Stat(markerPath); statErr == nil {
		t.Fatal("process executed before Job Object assignment and resume")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stat pre-assignment marker: %v", statErr)
	}

	if originalAttributes.CreationFlags != windows.CREATE_NEW_PROCESS_GROUP {
		t.Fatal("prepareProcessTree mutated caller-owned SysProcAttr")
	}
	if command.SysProcAttr == originalAttributes {
		t.Fatal("prepareProcessTree did not clone caller-owned SysProcAttr")
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("prepareProcessTree dropped an existing creation flag")
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED == 0 {
		t.Fatal("prepareProcessTree did not request CREATE_SUSPENDED")
	}
	if !command.SysProcAttr.HideWindow {
		t.Fatal("prepareProcessTree dropped HideWindow")
	}

	if err := tree.attach(command); err != nil {
		t.Fatalf("assign Job Object and resume: %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait resumed process: %v", err)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("resumed process did not write marker: %v", err)
	}
}
