//go:build linux

package e2e

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// t016ProcessIdentity is a test-only stable OS process handle used to prove
// actual process death after T016 generic-worker cancel/timeout scenarios. A
// completed Wait on the immediate child process is not tree-death proof for
// its descendants; each captured identity must be independently checked
// after the fact via pidfd plus a /proc start-time fingerprint, which
// prevents a reused PID from being mistaken for the original process.
type t016ProcessIdentity struct {
	pid       int
	startTime string
	pidfd     int
}

type t016ProcessStat struct {
	state     string
	startTime string
}

func captureT016ProcessIdentity(pid int) (*t016ProcessIdentity, error) {
	before, err := readT016ProcessStat(pid)
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

	after, err := readT016ProcessStat(pid)
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

	return &t016ProcessIdentity{pid: pid, startTime: before.startTime, pidfd: pidfd}, nil
}

func t016ProcessAlive(identity *t016ProcessIdentity) bool {
	if identity == nil || identity.pidfd < 0 {
		return false
	}
	stat, err := readT016ProcessStat(identity.pid)
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

func t016ForceKillProcess(identity *t016ProcessIdentity) error {
	if identity == nil || identity.pidfd < 0 {
		return nil
	}
	stat, err := readT016ProcessStat(identity.pid)
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

func closeT016ProcessIdentity(identity *t016ProcessIdentity) {
	if identity == nil || identity.pidfd < 0 {
		return
	}
	_ = unix.Close(identity.pidfd)
	identity.pidfd = -1
}

func readT016ProcessStat(pid int) (t016ProcessStat, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return t016ProcessStat{}, err
	}
	line := string(data)
	closingParen := strings.LastIndexByte(line, ')')
	if closingParen < 0 {
		return t016ProcessStat{}, fmt.Errorf("malformed /proc/%d/stat: missing command terminator", pid)
	}
	fields := strings.Fields(line[closingParen+1:])
	if len(fields) <= 19 {
		return t016ProcessStat{}, fmt.Errorf("malformed /proc/%d/stat: found %d post-command fields", pid, len(fields))
	}
	return t016ProcessStat{state: fields[0], startTime: fields[19]}, nil
}
