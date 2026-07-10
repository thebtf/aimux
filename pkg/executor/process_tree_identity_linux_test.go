//go:build linux

package executor_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

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
