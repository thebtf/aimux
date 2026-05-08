package e2e

import (
	"os"
	"os/exec"
)

// codexOnPATH returns true if the codex binary is resolvable on PATH.
func codexOnPATH() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

// codexE2EEnabled gates real Codex process tests behind explicit opt-in.
func codexE2EEnabled() bool {
	return codexOnPATH() && os.Getenv("CODEX_E2E") == "1"
}
