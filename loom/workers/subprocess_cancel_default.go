//go:build !unix

package workers

import "os/exec"

func configureSubprocessCancellation(_ *exec.Cmd) {}
