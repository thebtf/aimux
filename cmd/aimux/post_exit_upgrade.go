package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/thebtf/aimux/pkg/upgrade"
)

func maybeRunPostExitUpgrade(args []string) (bool, error) {
	flagIndex := -1
	for i, arg := range args {
		if arg == upgrade.PostExitUpgradeFlag {
			flagIndex = i
			break
		}
	}
	if flagIndex < 0 {
		return false, nil
	}

	fs := flag.NewFlagSet("aimux-post-exit-upgrade", flag.ContinueOnError)
	currentExe := fs.String("current-exe", "", "stable executable path to replace")
	stagedExe := fs.String("staged-exe", "", "staged replacement executable")
	daemonFlag := fs.String("daemon-flag", daemonFlagValue(), "daemon mode flag for replacement start")
	timeoutMs := fs.Int("timeout-ms", 45000, "maximum wait for old executable lock release")
	if err := fs.Parse(args[flagIndex+1:]); err != nil {
		return true, err
	}
	if *timeoutMs <= 0 {
		return true, fmt.Errorf("timeout-ms must be positive")
	}
	return true, upgrade.RunPostExitInstall(upgrade.PostExitInstallOptions{
		CurrentExe:  *currentExe,
		StagedExe:   *stagedExe,
		DaemonFlag:  *daemonFlag,
		WaitTimeout: time.Duration(*timeoutMs) * time.Millisecond,
	})
}
