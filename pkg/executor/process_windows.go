//go:build windows

package executor

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processTree owns the Job Object assigned to one process started by Spawn.
// releaseOnce makes natural exit, Kill, and Cleanup safe to race.
type processTree struct {
	job         windows.Handle
	releaseOnce sync.Once
}

type windowsThreadOperations struct {
	createSnapshot func() (windows.Handle, error)
	thread32First  func(windows.Handle, *windows.ThreadEntry32) error
	thread32Next   func(windows.Handle, *windows.ThreadEntry32) error
	openThread     func(uint32, bool, uint32) (windows.Handle, error)
	resumeThread   func(windows.Handle) (uint32, error)
	closeHandle    func(windows.Handle) error
}

var systemWindowsThreadOperations = windowsThreadOperations{
	createSnapshot: func() (windows.Handle, error) {
		return windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	},
	thread32First: windows.Thread32First,
	thread32Next:  windows.Thread32Next,
	openThread:    windows.OpenThread,
	resumeThread:  windows.ResumeThread,
	closeHandle:   windows.CloseHandle,
}

func prepareProcessTree(cmd *exec.Cmd) (*processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create Job Object: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("set Job Object kill-on-close: %w", err)
	}

	attributes := syscall.SysProcAttr{}
	if cmd.SysProcAttr != nil {
		attributes = *cmd.SysProcAttr
	}
	attributes.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.SysProcAttr = &attributes

	return &processTree{job: job}, nil
}

func (tree *processTree) attach(cmd *exec.Cmd) error {
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return fmt.Errorf("open process %d for Job Object assignment: %w", cmd.Process.Pid, err)
	}
	defer windows.CloseHandle(process)

	if err := windows.AssignProcessToJobObject(tree.job, process); err != nil {
		return fmt.Errorf("assign process %d to Job Object: %w", cmd.Process.Pid, err)
	}
	if err := resumePrimaryThread(uint32(cmd.Process.Pid)); err != nil {
		return fmt.Errorf("resume process %d after Job Object assignment: %w", cmd.Process.Pid, err)
	}
	return nil
}

func resumePrimaryThread(processID uint32) error {
	return processPrimaryThreadBatcher.resumeProcess(processID)
}

func snapshotThreadEntries() ([]windows.ThreadEntry32, error) {
	return snapshotThreadEntriesWithOperations(systemWindowsThreadOperations)
}

func snapshotThreadEntriesWithOperations(operations windowsThreadOperations) ([]windows.ThreadEntry32, error) {
	snapshot, err := operations.createSnapshot()
	if err != nil {
		return nil, fmt.Errorf("snapshot threads: %w", err)
	}
	defer operations.closeHandle(snapshot)

	entries := make([]windows.ThreadEntry32, 0, 1)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := operations.thread32First(snapshot, &entry); err != nil {
		if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return nil, fmt.Errorf("enumerate first thread: %w", err)
		}
	} else {
		for {
			entries = append(entries, entry)
			err := operations.thread32Next(snapshot, &entry)
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("enumerate next thread: %w", err)
			}
		}
	}
	return entries, nil
}

func resumeThread(threadID uint32) error {
	return resumeThreadWithOperations(threadID, systemWindowsThreadOperations)
}

func resumeThreadWithOperations(threadID uint32, operations windowsThreadOperations) error {
	thread, err := operations.openThread(
		windows.THREAD_SUSPEND_RESUME,
		false,
		threadID,
	)
	if err != nil {
		return fmt.Errorf("open primary thread %d: %w", threadID, err)
	}
	defer operations.closeHandle(thread)
	previousSuspendCount, resumeErr := operations.resumeThread(thread)
	if resumeErr != nil {
		return fmt.Errorf("resume primary thread %d: %w", threadID, resumeErr)
	}
	if previousSuspendCount != 1 {
		return fmt.Errorf(
			"primary thread %d had unexpected suspend count %d",
			threadID,
			previousSuspendCount,
		)
	}
	return nil
}

func selectSoleProcessThread(processID uint32, entries []windows.ThreadEntry32) (uint32, error) {
	var threadID uint32
	candidates := 0
	for _, entry := range entries {
		if entry.OwnerProcessID != processID {
			continue
		}
		threadID = entry.ThreadID
		candidates++
	}
	if candidates != 1 {
		return 0, fmt.Errorf(
			"process %d thread snapshot found %d candidates; expected exactly one",
			processID,
			candidates,
		)
	}
	return threadID, nil
}

func (tree *processTree) terminate() {
	if tree == nil {
		return
	}
	tree.releaseOnce.Do(func() {
		job := tree.job
		tree.job = 0
		if job == 0 {
			return
		}
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	})
}

func (tree *processTree) discard() {
	if tree == nil {
		return
	}
	tree.releaseOnce.Do(func() {
		job := tree.job
		tree.job = 0
		if job != 0 {
			_ = windows.CloseHandle(job)
		}
	})
}

func killRootProcess(h *ProcessHandle) {
	// Windows has no SIGTERM equivalent for this fallback handle.
	_ = h.Cmd.Process.Kill()
}
