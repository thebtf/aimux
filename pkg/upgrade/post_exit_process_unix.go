//go:build !windows

package upgrade

import (
	"os/exec"
	"syscall"
)

func configurePostExitCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
}
