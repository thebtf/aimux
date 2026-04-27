package server

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveEngineName returns the canonical daemon engine name used to scope
// task ownership in loom (AIMUX-10). It reads AIMUX_ENGINE_NAME first; if
// empty or whitespace-only, falls back to the binary basename (os.Args[0])
// stripped of the .exe/.EXE extension. As a last-resort backstop returns "aimux"
// — matches the existing default in cmd/aimux/shim.go and main.go.
//
// Spec: AIMUX-10 FR-7. Resolves CHK016 (unusual env values: trim, no length cap).
func ResolveEngineName() string {
	if name := strings.TrimSpace(os.Getenv("AIMUX_ENGINE_NAME")); name != "" {
		return name
	}
	if len(os.Args) > 0 && os.Args[0] != "" {
		base := filepath.Base(os.Args[0])
		base = strings.TrimSuffix(base, ".exe")
		base = strings.TrimSuffix(base, ".EXE")
		if trimmed := strings.TrimSpace(base); trimmed != "" {
			return trimmed
		}
	}
	return "aimux"
}
