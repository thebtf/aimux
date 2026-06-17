package server

import (
	"os"
	"strings"
)

// ResolveEngineName returns the canonical daemon engine name used to scope
// task ownership in loom (AIMUX-10). It reads AIMUX_ENGINE_NAME first; if empty
// or whitespace-only, returns the stable human-readable product label "aimux".
// muxcore derives the collision-resistant transport namespace internally, so
// binary basenames must not become the default display label.
//
// Spec: AIMUX-10 FR-7. Resolves CHK016 (unusual env values: trim, no length cap).
func ResolveEngineName() string {
	if name := strings.TrimSpace(os.Getenv("AIMUX_ENGINE_NAME")); name != "" {
		return name
	}
	return "aimux"
}
