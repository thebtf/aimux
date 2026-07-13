//go:build !windows

package executor_test

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
)

const processTreeTermReadyEnv = "AIMUX_PROCESS_TREE_CONTRACT_TERM_READY"

func TestProcessTreeTermEscalationHelper(t *testing.T) {
	role := os.Getenv(processTreeRoleEnv)
	if role != "term-resistant-root" && role != "term-resistant-descendant" {
		return
	}

	ackPath := os.Getenv(processTreeMarkerEnv)
	cleanupPath := os.Getenv(processTreeCleanupEnv)
	readyPath := os.Getenv(processTreeTermReadyEnv)
	if ackPath == "" || cleanupPath == "" || readyPath == "" {
		t.Fatal("process-tree TERM escalation helper paths are blank")
	}
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	defer signal.Stop(term)
	go func() {
		<-term
		_ = os.WriteFile(ackPath, []byte("TERM"), 0o600)
	}()
	if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
		t.Fatalf("write TERM escalation readiness: %v", err)
	}

	if role == "term-resistant-root" {
		handshakePath := os.Getenv(processTreeHandshakeEnv)
		descendantAckPath := os.Getenv("AIMUX_PROCESS_TREE_CONTRACT_DESCENDANT_TERM_ACK")
		descendantReadyPath := os.Getenv("AIMUX_PROCESS_TREE_CONTRACT_DESCENDANT_TERM_READY")
		if handshakePath == "" || descendantAckPath == "" || descendantReadyPath == "" {
			t.Fatal("process-tree TERM escalation root paths are blank")
		}
		child := exec.Command(os.Args[0], "-test.run=^TestProcessTreeTermEscalationHelper$", "-test.count=1")
		child.Env = append(
			os.Environ(),
			processTreeRoleEnv+"=term-resistant-descendant",
			processTreeMarkerEnv+"="+descendantAckPath,
			processTreeCleanupEnv+"="+cleanupPath,
			processTreeTermReadyEnv+"="+descendantReadyPath,
		)
		if err := child.Start(); err != nil {
			t.Fatalf("start TERM-resistant descendant: %v", err)
		}
		if err := writeProcessTreePIDHandshake(handshakePath, child.Process.Pid); err != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatalf("write TERM-resistant descendant PID handshake: %v", err)
		}
		if err := child.Wait(); err != nil {
			t.Fatalf("wait TERM-resistant descendant: %v", err)
		}
		return
	}

	for {
		if _, err := os.Stat(cleanupPath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

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

func TestProcessManager_KillEscalatesTermResistantOwnedGroup(t *testing.T) {
	processManager := executor.NewProcessManager()
	fixtureDir := t.TempDir()
	handshakePath := filepath.Join(fixtureDir, "descendant.pid")
	cleanupPath := filepath.Join(fixtureDir, "cleanup")
	rootAckPath := filepath.Join(fixtureDir, "root.term")
	descendantAckPath := filepath.Join(fixtureDir, "descendant.term")
	rootReadyPath := filepath.Join(fixtureDir, "root.ready")
	descendantReadyPath := filepath.Join(fixtureDir, "descendant.ready")

	root := exec.Command(os.Args[0], "-test.run=^TestProcessTreeTermEscalationHelper$", "-test.count=1")
	root.Env = append(
		os.Environ(),
		processTreeRoleEnv+"=term-resistant-root",
		processTreeHandshakeEnv+"="+handshakePath,
		processTreeMarkerEnv+"="+rootAckPath,
		processTreeCleanupEnv+"="+cleanupPath,
		processTreeTermReadyEnv+"="+rootReadyPath,
		"AIMUX_PROCESS_TREE_CONTRACT_DESCENDANT_TERM_ACK="+descendantAckPath,
		"AIMUX_PROCESS_TREE_CONTRACT_DESCENDANT_TERM_READY="+descendantReadyPath,
	)
	handle, err := processManager.Spawn(root)
	if err != nil {
		t.Fatalf("spawn TERM-resistant process tree: %v", err)
	}
	pgid, err := syscall.Getpgid(handle.PID)
	if err != nil {
		cleanupProcessTreeFixture(processManager, handle, nil, cleanupPath)
		t.Fatalf("read owned process group: %v", err)
	}
	if pgid != handle.PID || pgid == syscall.Getpgrp() {
		cleanupProcessTreeFixture(processManager, handle, nil, cleanupPath)
		t.Fatalf("owned process group = %d, root = %d, caller group = %d", pgid, handle.PID, syscall.Getpgrp())
	}
	descendantPID := waitForProcessTreePID(t, handshakePath, 5*time.Second)
	descendantIdentity, err := captureProcessTreeIdentity(descendantPID)
	if err != nil || descendantIdentity == nil {
		cleanupProcessTreeFixture(processManager, handle, descendantIdentity, cleanupPath)
		t.Fatalf("capture TERM-resistant descendant identity: %v", err)
	}
	defer cleanupProcessTreeFixture(processManager, handle, descendantIdentity, cleanupPath)
	waitForProcessTreeFile(t, rootReadyPath, 5*time.Second)
	waitForProcessTreeFile(t, descendantReadyPath, 5*time.Second)

	processManager.Kill(handle)
	waitForProcessTreeFile(t, rootAckPath, 5*time.Second)
	waitForProcessTreeFile(t, descendantAckPath, 5*time.Second)
	if !waitForProcessTreeExit(descendantIdentity, 5*time.Second) {
		t.Fatal("TERM-resistant descendant survived TERM-to-KILL escalation")
	}
}

func waitForProcessTreeFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process-tree acknowledgement %q timed out", path)
}
