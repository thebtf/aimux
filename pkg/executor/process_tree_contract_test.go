package executor_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
)

const (
	processTreeRoleEnv      = "AIMUX_PROCESS_TREE_CONTRACT_ROLE"
	processTreeHandshakeEnv = "AIMUX_PROCESS_TREE_CONTRACT_HANDSHAKE"
	processTreeCleanupEnv   = "AIMUX_PROCESS_TREE_CONTRACT_CLEANUP"
	processTreeMarkerEnv    = "AIMUX_PROCESS_TREE_CONTRACT_MARKER"
)

// TestProcessTreeContractHelper is re-executed in isolated subprocesses. The
// root owns one descendant and waits for it; the descendant stays alive until
// the test's cleanup marker appears.
func TestProcessTreeContractHelper(t *testing.T) {
	role := os.Getenv(processTreeRoleEnv)
	if role == "" {
		return
	}
	if role == "marker" {
		markerPath := os.Getenv(processTreeMarkerEnv)
		if markerPath == "" {
			t.Fatal("process-tree helper marker path is blank")
		}
		if err := os.WriteFile(markerPath, []byte("started"), 0o600); err != nil {
			t.Fatalf("write process-tree marker: %v", err)
		}
		return
	}

	cleanupPath := os.Getenv(processTreeCleanupEnv)
	if cleanupPath == "" {
		t.Fatal("process-tree helper cleanup path is blank")
	}

	switch role {
	case "root", "root-exit":
		handshakePath := os.Getenv(processTreeHandshakeEnv)
		if handshakePath == "" {
			t.Fatal("process-tree helper handshake path is blank")
		}

		child := exec.Command(os.Args[0], "-test.run=^TestProcessTreeContractHelper$", "-test.count=1")
		child.Env = append(
			os.Environ(),
			processTreeRoleEnv+"=descendant",
			processTreeCleanupEnv+"="+cleanupPath,
		)
		if err := child.Start(); err != nil {
			t.Fatalf("start descendant: %v", err)
		}
		if err := writeProcessTreePIDHandshake(handshakePath, child.Process.Pid); err != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatalf("write descendant PID handshake: %v", err)
		}
		if role == "root-exit" {
			if err := child.Process.Release(); err != nil {
				t.Fatalf("release descendant process handle: %v", err)
			}
			return
		}
		if err := child.Wait(); err != nil {
			t.Fatalf("wait descendant: %v", err)
		}
	case "descendant":
		for {
			if _, err := os.Stat(cleanupPath); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	default:
		t.Fatalf("unknown process-tree helper role %q", role)
	}
}

func TestProcessManager_KillStopsOwnedDescendants(t *testing.T) {
	processManager := executor.NewProcessManager()
	fixtureDir := t.TempDir()
	handshakePath := filepath.Join(fixtureDir, "descendant.pid")
	cleanupPath := filepath.Join(fixtureDir, "cleanup")

	root := exec.Command(os.Args[0], "-test.run=^TestProcessTreeContractHelper$", "-test.count=1")
	root.Env = append(
		os.Environ(),
		processTreeRoleEnv+"=root",
		processTreeHandshakeEnv+"="+handshakePath,
		processTreeCleanupEnv+"="+cleanupPath,
	)
	handle, err := processManager.Spawn(root)
	if err != nil {
		t.Fatalf("spawn process-tree root: %v", err)
	}

	var descendantIdentity *processTreeIdentity
	defer func() {
		cleanupProcessTreeFixture(processManager, handle, descendantIdentity, cleanupPath)
	}()

	descendantPID := waitForProcessTreePID(t, handshakePath, 5*time.Second)
	descendantIdentity, err = captureProcessTreeIdentity(descendantPID)
	if err != nil {
		t.Fatalf("capture process-tree descendant identity: %v", err)
	}
	if descendantIdentity == nil {
		t.Fatal("process-tree descendant exited before identity capture")
	}
	if !processTreeProcessAlive(descendantIdentity) {
		t.Fatal("process-tree descendant exited before cancellation")
	}

	processManager.Kill(handle)
	if waitForProcessTreeExit(descendantIdentity, 5*time.Second) {
		return
	}
	t.Fatal("descendant still alive")
}

func TestProcessManager_NaturalRootExitStopsOwnedDescendants(t *testing.T) {
	processManager := executor.NewProcessManager()
	handle, descendantIdentity, cleanupPath := spawnProcessTreeContractRoot(t, processManager, "root-exit")
	defer cleanupProcessTreeFixture(processManager, handle, descendantIdentity, cleanupPath)

	select {
	case <-handle.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("process-tree root did not exit naturally")
	}
	if !waitForProcessTreeExit(descendantIdentity, 5*time.Second) {
		t.Fatal("descendant still alive after natural root exit")
	}
}

func TestProcessManager_CleanupStopsOwnedDescendants(t *testing.T) {
	processManager := executor.NewProcessManager()
	handle, descendantIdentity, cleanupPath := spawnProcessTreeContractRoot(t, processManager, "root")
	defer cleanupProcessTreeFixture(processManager, handle, descendantIdentity, cleanupPath)

	processManager.Cleanup(handle)
	if !waitForProcessTreeExit(descendantIdentity, 5*time.Second) {
		t.Fatal("descendant still alive after cleanup")
	}
	select {
	case <-handle.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("root still alive after cleanup")
	}
}

func TestProcessManager_ConcurrentKillAndCleanupStopOwnedDescendants(t *testing.T) {
	processManager := executor.NewProcessManager()
	handle, descendantIdentity, cleanupPath := spawnProcessTreeContractRoot(t, processManager, "root")
	defer cleanupProcessTreeFixture(processManager, handle, descendantIdentity, cleanupPath)

	done := make(chan struct{}, 2)
	go func() {
		processManager.Kill(handle)
		done <- struct{}{}
	}()
	go func() {
		processManager.Cleanup(handle)
		done <- struct{}{}
	}()
	for range 2 {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent kill/cleanup timed out")
		}
	}
	if !waitForProcessTreeExit(descendantIdentity, 5*time.Second) {
		t.Fatal("descendant still alive after concurrent kill/cleanup")
	}
}

func spawnProcessTreeContractRoot(
	t *testing.T,
	processManager *executor.ProcessManager,
	rootRole string,
) (*executor.ProcessHandle, *processTreeIdentity, string) {
	t.Helper()
	fixtureDir := t.TempDir()
	handshakePath := filepath.Join(fixtureDir, "descendant.pid")
	cleanupPath := filepath.Join(fixtureDir, "cleanup")
	root := exec.Command(os.Args[0], "-test.run=^TestProcessTreeContractHelper$", "-test.count=1")
	root.Env = append(
		os.Environ(),
		processTreeRoleEnv+"="+rootRole,
		processTreeHandshakeEnv+"="+handshakePath,
		processTreeCleanupEnv+"="+cleanupPath,
	)
	handle, err := processManager.Spawn(root)
	if err != nil {
		t.Fatalf("spawn process-tree root: %v", err)
	}
	descendantPID := waitForProcessTreePID(t, handshakePath, 5*time.Second)
	descendantIdentity, err := captureProcessTreeIdentity(descendantPID)
	if err != nil {
		cleanupProcessTreeFixture(processManager, handle, nil, cleanupPath)
		t.Fatalf("capture process-tree descendant identity: %v", err)
	}
	if rootRole != "root-exit" && descendantIdentity == nil {
		cleanupProcessTreeFixture(processManager, handle, nil, cleanupPath)
		t.Fatal("process-tree descendant exited before identity capture")
	}
	if rootRole != "root-exit" && !processTreeProcessAlive(descendantIdentity) {
		cleanupProcessTreeFixture(processManager, handle, descendantIdentity, cleanupPath)
		t.Fatal("process-tree descendant exited before lifecycle action")
	}
	return handle, descendantIdentity, cleanupPath
}

func writeProcessTreePIDHandshake(path string, pid int) error {
	temporaryPath := path + ".tmp-" + strconv.Itoa(os.Getpid())
	defer os.Remove(temporaryPath)
	if err := os.WriteFile(temporaryPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func waitForProcessTreePID(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("process-tree descendant PID handshake timed out")
	return 0
}

func waitForProcessTreeExit(identity *processTreeIdentity, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !processTreeProcessAlive(identity) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func cleanupProcessTreeFixture(
	processManager *executor.ProcessManager,
	handle *executor.ProcessHandle,
	descendantIdentity *processTreeIdentity,
	cleanupPath string,
) {
	defer closeProcessTreeIdentity(descendantIdentity)
	_ = os.WriteFile(cleanupPath, []byte("stop"), 0o600)
	if descendantIdentity != nil && !waitForProcessTreeExit(descendantIdentity, time.Second) {
		_ = processTreeForceKill(descendantIdentity)
		_ = waitForProcessTreeExit(descendantIdentity, 2*time.Second)
	}
	if processManager.IsAlive(handle) {
		processManager.Kill(handle)
	}
	processManager.Cleanup(handle)
}
