//go:build !windows

package executor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

const (
	processTreeTermGrace    = 2 * time.Second
	processTreeKillGrace    = time.Second
	processTreePollInterval = 10 * time.Millisecond
)

// processTree owns a process group whose ID is the root PID. SysProcAttr creates
// the initial boundary before Start; descendants may deliberately leave it
// later with setsid or setpgid and are then outside this evidence claim.
type processTree struct {
	pgid         int
	releaseOnce  sync.Once
	stopObserved atomic.Bool
}

func prepareProcessTree(cmd *exec.Cmd) (*processTree, error) {
	attributes := syscall.SysProcAttr{}
	if cmd.SysProcAttr != nil {
		attributes = *cmd.SysProcAttr
	}

	if attributes.Pgid != 0 {
		return nil, fmt.Errorf(
			"caller-owned process group %d is not safe for managed spawn",
			attributes.Pgid,
		)
	}
	if attributes.Setsid && (attributes.Setpgid || attributes.Foreground) {
		return nil, fmt.Errorf(
			"Setsid cannot be combined with Setpgid or Foreground for managed spawn",
		)
	}

	// Setsid already makes the child a session and process-group leader.
	// Foreground implies Setpgid in os/exec. Otherwise request a fresh group.
	if !attributes.Setsid && !attributes.Foreground {
		attributes.Setpgid = true
	}
	cmd.SysProcAttr = &attributes
	return &processTree{}, nil
}

func (tree *processTree) attach(cmd *exec.Cmd) error {
	// Start succeeded only after the kernel applied the isolation attributes,
	// so retain the expected PGID even if verification itself fails. The
	// common spawn-failure path can then still terminate the owned group.
	tree.pgid = cmd.Process.Pid
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return fmt.Errorf("read process group for pid %d: %w", cmd.Process.Pid, err)
	}
	if pgid != cmd.Process.Pid {
		return fmt.Errorf(
			"process group %d is not owned by root pid %d",
			pgid,
			cmd.Process.Pid,
		)
	}
	return nil
}

func (tree *processTree) terminate() bool {
	if tree == nil {
		return false
	}
	tree.releaseOnce.Do(func() {
		if tree.pgid <= 0 {
			return
		}
		if err := syscall.Kill(-tree.pgid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
			tree.stopObserved.Store(true)
			return
		}
		if waitForProcessGroupExit(tree.pgid, processTreeTermGrace) {
			tree.stopObserved.Store(true)
			return
		}
		_ = syscall.Kill(-tree.pgid, syscall.SIGKILL)
		if waitForProcessGroupExit(tree.pgid, processTreeKillGrace) {
			tree.stopObserved.Store(true)
		}
	})
	return tree.stopObserved.Load()
}

func (tree *processTree) stopped() bool { return tree != nil && tree.stopObserved.Load() }

func (*processTree) ownershipBoundary() types.ProcessOwnershipBoundary {
	return types.ProcessOwnershipBoundaryProcessGroup
}

func (tree *processTree) discard() {
	if tree == nil {
		return
	}
	tree.releaseOnce.Do(func() {})
}

func waitForProcessGroupExit(pgid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(-pgid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(processTreePollInterval)
	}
}

// killRootProcess preserves the fallback behavior for synthetic handles that
// were not started in an owned process group.
func killRootProcess(h *ProcessHandle) {
	_ = h.Cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-h.Done:
		return
	case <-time.After(5 * time.Second):
		_ = h.Cmd.Process.Signal(os.Kill)
	}
}
