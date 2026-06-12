//go:build windows

package upgrade

import (
	"os/exec"
	"syscall"
)

func configurePostExitCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}
