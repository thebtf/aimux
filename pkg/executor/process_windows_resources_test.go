//go:build windows

package executor

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/types"
	"golang.org/x/sys/windows"
)

func TestSnapshotThreadEntriesWithOperations_ClosesAcquiredSnapshotExactlyOnce(t *testing.T) {
	wantFirstErr := errors.New("first failed")
	wantNextErr := errors.New("next failed")
	wantCreateErr := errors.New("create failed")
	const snapshotHandle = windows.Handle(101)

	tests := []struct {
		name        string
		createErr   error
		firstErr    error
		nextErr     error
		wantErr     error
		wantEntries int
		wantCloses  int
	}{
		{name: "success", wantEntries: 1, wantCloses: 1},
		{name: "empty snapshot", firstErr: windows.ERROR_NO_MORE_FILES, wantCloses: 1},
		{name: "create failure", createErr: wantCreateErr, wantErr: wantCreateErr},
		{name: "first failure", firstErr: wantFirstErr, wantErr: wantFirstErr, wantCloses: 1},
		{name: "next failure", nextErr: wantNextErr, wantErr: wantNextErr, wantCloses: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			closeCalls := 0
			operations := windowsThreadOperations{
				createSnapshot: func() (windows.Handle, error) {
					if test.createErr != nil {
						return 0, test.createErr
					}
					return snapshotHandle, nil
				},
				thread32First: func(handle windows.Handle, entry *windows.ThreadEntry32) error {
					if handle != snapshotHandle {
						t.Fatalf("first snapshot handle = %d, want %d", handle, snapshotHandle)
					}
					if test.firstErr != nil {
						return test.firstErr
					}
					entry.ThreadID = 201
					entry.OwnerProcessID = 301
					return nil
				},
				thread32Next: func(handle windows.Handle, _ *windows.ThreadEntry32) error {
					if handle != snapshotHandle {
						t.Fatalf("next snapshot handle = %d, want %d", handle, snapshotHandle)
					}
					if test.nextErr != nil {
						return test.nextErr
					}
					return windows.ERROR_NO_MORE_FILES
				},
				closeHandle: func(handle windows.Handle) error {
					if handle != snapshotHandle {
						t.Fatalf("closed snapshot handle = %d, want %d", handle, snapshotHandle)
					}
					closeCalls++
					return nil
				},
			}

			entries, err := snapshotThreadEntriesWithOperations(operations)
			if test.wantErr == nil && err != nil {
				t.Fatalf("snapshotThreadEntriesWithOperations failed: %v", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("snapshot error = %v, want %v", err, test.wantErr)
			}
			if len(entries) != test.wantEntries {
				t.Fatalf("entries = %d, want %d", len(entries), test.wantEntries)
			}
			if closeCalls != test.wantCloses {
				t.Fatalf("snapshot close calls = %d, want %d", closeCalls, test.wantCloses)
			}
		})
	}
}

func TestProcessTreeTerminateRequiresObservedJobCompletion(t *testing.T) {
	const job = windows.Handle(101)
	for _, test := range []struct {
		name    string
		status  uint32
		waitErr error
		want    bool
	}{
		{name: "termination request with timeout remains unconfirmed", status: uint32(windows.WAIT_TIMEOUT)},
		{name: "termination request with wait failure remains unconfirmed", waitErr: errors.New("wait failed")},
		{name: "signalled exact job confirms job boundary", status: uint32(windows.WAIT_OBJECT_0), want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			terminated, waited, closed := 0, 0, 0
			tree := &processTree{job: job, jobOps: windowsJobOperations{
				terminate: func(got windows.Handle, code uint32) error {
					if got != job || code != 1 {
						t.Fatalf("TerminateJobObject(%d, %d)", got, code)
					}
					terminated++
					return nil
				},
				wait: func(got windows.Handle, timeout uint32) (uint32, error) {
					if got != job || timeout != processTreeJobWaitTimeout {
						t.Fatalf("WaitForSingleObject(%d, %d)", got, timeout)
					}
					waited++
					return test.status, test.waitErr
				},
				close: func(got windows.Handle) error {
					if got != job {
						t.Fatalf("CloseHandle(%d)", got)
					}
					closed++
					return nil
				},
			}}
			if got := (&ProcessHandle{processTree: tree}).TreeOwnershipBoundary(); got != types.ProcessOwnershipBoundaryJobObject {
				t.Fatalf("ownership boundary = %q, want %q", got, types.ProcessOwnershipBoundaryJobObject)
			}
			if got := tree.terminate(); got != test.want || tree.stopped() != test.want {
				t.Fatalf("terminate/stopped = %t/%t, want %t", got, tree.stopped(), test.want)
			}
			if terminated != 1 || waited != 1 || closed != 1 {
				t.Fatalf("operations terminate=%d wait=%d close=%d, want 1/1/1", terminated, waited, closed)
			}
		})
	}
}

func TestProcessTreeTerminateDefaultsPartialJobOperations(t *testing.T) {
	const job = windows.Handle(101)
	terminated := 0
	tree := &processTree{job: job, jobOps: windowsJobOperations{
		terminate: func(got windows.Handle, code uint32) error {
			if got != job || code != 1 {
				t.Fatalf("TerminateJobObject(%d, %d)", got, code)
			}
			terminated++
			return errors.New("terminate failed")
		},
	}}
	if tree.terminate() {
		t.Fatal("failed termination was reported as observed")
	}
	if terminated != 1 {
		t.Fatalf("custom terminate calls = %d, want 1", terminated)
	}
}

func TestResumeThreadWithOperations_ClosesAcquiredThreadExactlyOnce(t *testing.T) {
	wantOpenErr := errors.New("open failed")
	wantResumeErr := errors.New("resume failed")
	const (
		threadID     = uint32(401)
		threadHandle = windows.Handle(501)
	)

	tests := []struct {
		name              string
		openErr           error
		resumeErr         error
		previousSuspend   uint32
		wantErr           error
		wantUnexpectedErr bool
		wantCloses        int
	}{
		{name: "success", previousSuspend: 1, wantCloses: 1},
		{name: "open failure", openErr: wantOpenErr, wantErr: wantOpenErr},
		{name: "resume failure", resumeErr: wantResumeErr, wantErr: wantResumeErr, wantCloses: 1},
		{name: "unexpected suspend count", previousSuspend: 2, wantUnexpectedErr: true, wantCloses: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			closeCalls := 0
			operations := windowsThreadOperations{
				openThread: func(access uint32, inherit bool, gotThreadID uint32) (windows.Handle, error) {
					if access != windows.THREAD_SUSPEND_RESUME {
						t.Fatalf("thread access = %d, want THREAD_SUSPEND_RESUME", access)
					}
					if inherit {
						t.Fatal("thread handle unexpectedly inheritable")
					}
					if gotThreadID != threadID {
						t.Fatalf("thread ID = %d, want %d", gotThreadID, threadID)
					}
					if test.openErr != nil {
						return 0, test.openErr
					}
					return threadHandle, nil
				},
				resumeThread: func(handle windows.Handle) (uint32, error) {
					if handle != threadHandle {
						t.Fatalf("resumed thread handle = %d, want %d", handle, threadHandle)
					}
					return test.previousSuspend, test.resumeErr
				},
				closeHandle: func(handle windows.Handle) error {
					if handle != threadHandle {
						t.Fatalf("closed thread handle = %d, want %d", handle, threadHandle)
					}
					closeCalls++
					return nil
				},
			}

			err := resumeThreadWithOperations(threadID, operations)
			if test.wantErr == nil && !test.wantUnexpectedErr && err != nil {
				t.Fatalf("resumeThreadWithOperations failed: %v", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("resume error = %v, want %v", err, test.wantErr)
			}
			if test.wantUnexpectedErr {
				want := fmt.Sprintf("primary thread %d had unexpected suspend count %d", threadID, test.previousSuspend)
				if err == nil || err.Error() != want {
					t.Fatalf("unexpected-count error = %v, want %q", err, want)
				}
			}
			if closeCalls != test.wantCloses {
				t.Fatalf("thread close calls = %d, want %d", closeCalls, test.wantCloses)
			}
		})
	}
}

func TestProcessManager_ConcurrentOwnedStartsReleaseAllResources(t *testing.T) {
	const cohortSize = 8
	type outcome struct {
		index  int
		handle *ProcessHandle
		err    error
	}

	processManager := NewProcessManager()
	startGate := make(chan struct{})
	outcomes := make(chan outcome, cohortSize)
	var workers sync.WaitGroup
	workers.Add(cohortSize)
	for i := range cohortSize {
		go func(index int) {
			defer workers.Done()
			<-startGate
			handle, err := processManager.Spawn(exec.Command("cmd", "/c", "exit", "0"))
			outcomes <- outcome{index: index, handle: handle, err: err}
		}(i)
	}
	close(startGate)
	workers.Wait()
	close(outcomes)

	handles := make([]*ProcessHandle, 0, cohortSize)
	defer func() {
		for _, handle := range handles {
			processManager.Kill(handle)
			processManager.Cleanup(handle)
		}
	}()
	firstErrorIndex := -1
	var firstError error
	for result := range outcomes {
		if result.err != nil {
			if firstError == nil {
				firstErrorIndex = result.index
				firstError = result.err
			}
			continue
		}
		handles = append(handles, result.handle)
	}
	if firstError != nil {
		t.Fatalf("spawn owned exit helper %d: %v", firstErrorIndex, firstError)
	}

	for i, handle := range handles {
		select {
		case waitErr := <-handle.Done:
			if waitErr != nil && firstError == nil {
				firstErrorIndex = i
				firstError = waitErr
			}
		case <-time.After(10 * time.Second):
			processManager.Kill(handle)
			if firstError == nil {
				firstErrorIndex = i
				firstError = errors.New("timed out waiting for owned exit helper")
			}
		}
		processManager.Cleanup(handle)
	}
	if firstError != nil {
		t.Fatalf("owned exit helper %d failed: %v", firstErrorIndex, firstError)
	}

	for i, handle := range handles {
		if handle.processTree == nil {
			t.Errorf("handle %d has no owned process tree", i)
		} else if handle.processTree.job != 0 {
			t.Errorf("handle %d retained Job handle %d after Done/Cleanup", i, handle.processTree.job)
		}
		handle.mu.Lock()
		cleaned := handle.cleaned
		handle.mu.Unlock()
		if !cleaned {
			t.Errorf("handle %d was not marked cleaned", i)
		}
	}

	remaining := 0
	processManager.handles.Range(func(_, _ any) bool {
		remaining++
		return true
	})
	if remaining != 0 {
		t.Fatalf("process manager retained %d handles after cohort cleanup", remaining)
	}
}
