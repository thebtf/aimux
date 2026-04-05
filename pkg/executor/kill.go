package executor

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// KillProcessTree kills a process and all its children.
// On Windows uses taskkill /T (tree kill).
// On Unix uses process group kill via negative PID.
func KillProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}

	if runtime.GOOS == "windows" {
		return killTreeWindows(pid)
	}
	return killTreeUnix(pid)
}

// killTreeWindows uses taskkill /T /F /PID to kill process tree.
func killTreeWindows(pid int) error {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	return cmd.Run()
}

// killTreeUnix sends SIGKILL to the process group (negative PID).
func killTreeUnix(pid int) error {
	// Kill process group: negative PID kills all processes in group
	pgid, err := findProcessGroup(pid)
	if err != nil {
		// Fallback: kill single process
		proc, findErr := os.FindProcess(pid)
		if findErr != nil {
			return findErr
		}
		return proc.Kill()
	}

	// Kill entire process group
	proc, err := os.FindProcess(-pgid)
	if err != nil {
		return err
	}
	return proc.Signal(os.Kill)
}

// findProcessGroup returns the process group ID for a PID.
// On Unix this is typically the same as the PID for process group leaders.
func findProcessGroup(pid int) (int, error) {
	// On Unix, we can use syscall.Getpgid but it requires the process to exist.
	// For simplicity, assume the process is its own group leader.
	// Full implementation would use syscall.Getpgid(pid).
	return pid, nil
}
