//go:build linux

package executor_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pipeexecutor "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
	"golang.org/x/sys/unix"
)

const processTreeEscapedSessionRoleEnv = "AIMUX_PROCESS_TREE_ESCAPED_SESSION_ROLE"

func TestProcessTreeEscapedSessionHelper(t *testing.T) {
	role := os.Getenv(processTreeEscapedSessionRoleEnv)
	if role == "" {
		return
	}
	handshakePath := os.Getenv(processTreeHandshakeEnv)
	cleanupPath := os.Getenv(processTreeCleanupEnv)
	if handshakePath == "" || cleanupPath == "" {
		t.Fatal("escaped-session helper paths are blank")
	}
	switch role {
	case "root":
		child := exec.Command(os.Args[0], "-test.run=^TestProcessTreeEscapedSessionHelper$", "-test.count=1")
		child.Env = append(
			os.Environ(),
			processTreeEscapedSessionRoleEnv+"=escaped",
			processTreeHandshakeEnv+"="+handshakePath,
			processTreeCleanupEnv+"="+cleanupPath,
		)
		if err := child.Start(); err != nil {
			t.Fatalf("start escaped-session descendant: %v", err)
		}
		if err := child.Process.Release(); err != nil {
			t.Fatalf("release escaped-session descendant: %v", err)
		}
		select {}
	case "escaped":
		if _, err := unix.Setsid(); err != nil {
			t.Fatalf("setsid: %v", err)
		}
		if err := writeProcessTreePIDHandshake(handshakePath, os.Getpid()); err != nil {
			t.Fatalf("write escaped-session PID handshake: %v", err)
		}
		for {
			if _, err := os.Stat(cleanupPath); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	default:
		t.Fatalf("unknown escaped-session helper role %q", role)
	}
}

func TestPipeProcessEvidenceNamesGroupBoundaryWhenDescendantEscapesSession(t *testing.T) {
	fixtureDir := t.TempDir()
	handshakePath := filepath.Join(fixtureDir, "escaped.pid")
	cleanupPath := filepath.Join(fixtureDir, "cleanup")
	e := pipeexecutor.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const id = types.ExecutionID("escaped-session")
	done := make(chan error, 1)
	go func() {
		_, err := e.SendEvents(ctx, id, types.Message{Spawn: &types.SpawnArgs{
			Command: os.Args[0],
			Args:    []string{"-test.run=^TestProcessTreeEscapedSessionHelper$", "-test.count=1"},
			Env: map[string]string{
				processTreeEscapedSessionRoleEnv: "root",
				processTreeHandshakeEnv:          handshakePath,
				processTreeCleanupEnv:            cleanupPath,
			},
		}}, nil)
		done <- err
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		_ = os.WriteFile(cleanupPath, []byte("stop"), 0o600)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("escaped-session root survived deterministic cleanup")
		}
	})
	escapedPID := waitForProcessTreePID(t, handshakePath, 5*time.Second)
	escapedIdentity, err := captureProcessTreeIdentity(escapedPID)
	if err != nil || escapedIdentity == nil {
		cancel()
		<-done
		t.Fatalf("capture escaped-session identity: %v", err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(cleanupPath, []byte("stop"), 0o600)
		if !waitForProcessTreeExit(escapedIdentity, time.Second) {
			_ = processTreeForceKill(escapedIdentity)
			if !waitForProcessTreeExit(escapedIdentity, 2*time.Second) {
				t.Error("escaped-session descendant survived deterministic cleanup")
			}
		}
		closeProcessTreeIdentity(escapedIdentity)
	})
	pgid, err := unix.Getpgid(escapedPID)
	if err != nil {
		t.Fatalf("read escaped-session process group: %v", err)
	}
	if pgid != escapedPID {
		t.Fatalf("escaped-session process group = %d, want its own PID %d", pgid, escapedPID)
	}
	liveEvidence, err := e.ProcessTreeEvidence(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if liveEvidence.OwnershipBoundary != types.ProcessOwnershipBoundaryProcessGroup || liveEvidence.Stopped {
		t.Fatalf("live evidence = %#v, want active process_group boundary", liveEvidence)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled pipe execution did not return")
	}
	finalEvidence, err := e.ProcessTreeEvidence(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if finalEvidence.OwnershipBoundary != types.ProcessOwnershipBoundaryProcessGroup || !finalEvidence.Stopped || finalEvidence.Validate() != nil {
		t.Fatalf("final evidence = %#v, want stopped process_group boundary", finalEvidence)
	}
	if !processTreeProcessAlive(escapedIdentity) {
		t.Fatal("escaped-session descendant was incorrectly included in process_group stop evidence")
	}
	if err := os.WriteFile(cleanupPath, []byte("stop"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !waitForProcessTreeExit(escapedIdentity, 5*time.Second) {
		_ = processTreeForceKill(escapedIdentity)
		t.Fatal("escaped-session descendant did not exit after deterministic cleanup")
	}
}

type processTreeIdentity struct {
	pid       int
	startTime string
	pidfd     int
}

type processTreeStat struct {
	state     string
	startTime string
}

func captureProcessTreeIdentity(pid int) (*processTreeIdentity, error) {
	before, err := readProcessTreeStat(pid)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read process %d identity before pidfd capture: %w", pid, err)
	}
	if before.state == "Z" || before.state == "X" {
		return nil, nil
	}

	pidfd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open pidfd for process %d: %w", pid, err)
	}

	after, err := readProcessTreeStat(pid)
	if errors.Is(err, os.ErrNotExist) {
		_ = unix.Close(pidfd)
		return nil, nil
	}
	if err != nil {
		_ = unix.Close(pidfd)
		return nil, fmt.Errorf("verify process %d identity after pidfd capture: %w", pid, err)
	}
	if after.startTime != before.startTime || after.state == "Z" || after.state == "X" {
		_ = unix.Close(pidfd)
		return nil, nil
	}

	return &processTreeIdentity{pid: pid, startTime: before.startTime, pidfd: pidfd}, nil
}

func processTreeProcessAlive(identity *processTreeIdentity) bool {
	if identity == nil || identity.pidfd < 0 {
		return false
	}
	stat, err := readProcessTreeStat(identity.pid)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	if stat.startTime != identity.startTime || stat.state == "Z" || stat.state == "X" {
		return false
	}
	err = unix.PidfdSendSignal(identity.pidfd, 0, nil, 0)
	return err == nil || !errors.Is(err, unix.ESRCH)
}

func processTreeForceKill(identity *processTreeIdentity) error {
	if identity == nil || identity.pidfd < 0 {
		return nil
	}
	stat, err := readProcessTreeStat(identity.pid)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify process %d identity before force kill: %w", identity.pid, err)
	}
	if stat.startTime != identity.startTime || stat.state == "Z" || stat.state == "X" {
		return nil
	}
	if err := unix.PidfdSendSignal(identity.pidfd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("force kill process %d through pidfd: %w", identity.pid, err)
	}
	return nil
}

func closeProcessTreeIdentity(identity *processTreeIdentity) {
	if identity == nil || identity.pidfd < 0 {
		return
	}
	_ = unix.Close(identity.pidfd)
	identity.pidfd = -1
}

func readProcessTreeStat(pid int) (processTreeStat, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return processTreeStat{}, err
	}
	line := string(data)
	closingParen := strings.LastIndexByte(line, ')')
	if closingParen < 0 {
		return processTreeStat{}, fmt.Errorf("malformed /proc/%d/stat: missing command terminator", pid)
	}
	fields := strings.Fields(line[closingParen+1:])
	if len(fields) <= 19 {
		return processTreeStat{}, fmt.Errorf("malformed /proc/%d/stat: found %d post-command fields", pid, len(fields))
	}
	return processTreeStat{state: fields[0], startTime: fields[19]}, nil
}

func TestProcessTreeForceKill_DoesNotSignalChangedIdentity(t *testing.T) {
	cleanupPath := filepath.Join(t.TempDir(), "cleanup")
	child := exec.Command(os.Args[0], "-test.run=^TestProcessTreeContractHelper$", "-test.count=1")
	child.Env = append(
		os.Environ(),
		processTreeRoleEnv+"=descendant",
		processTreeCleanupEnv+"="+cleanupPath,
	)
	if err := child.Start(); err != nil {
		t.Fatalf("start identity fixture: %v", err)
	}

	identity, err := captureProcessTreeIdentity(child.Process.Pid)
	if err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatalf("capture process identity: %v", err)
	}
	if identity == nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal("identity fixture exited before capture")
	}
	defer closeProcessTreeIdentity(identity)
	defer func() {
		_ = os.WriteFile(cleanupPath, []byte("stop"), 0o600)
		done := make(chan struct{})
		go func() {
			_ = child.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = child.Process.Kill()
			<-done
		}
	}()

	changed := *identity
	changed.startTime += "-reused"
	if err := processTreeForceKill(&changed); err != nil {
		t.Fatalf("force kill changed identity: %v", err)
	}
	if !processTreeProcessAlive(identity) {
		t.Fatal("changed process identity signaled the original process")
	}
}
