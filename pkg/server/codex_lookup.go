package server

import (
	"fmt"
	"os/exec"
)

// lookupCodexBinary returns the full path to the codex binary if available.
func lookupCodexBinary() (string, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("codex not found on PATH: %w", err)
	}
	return path, nil
}
