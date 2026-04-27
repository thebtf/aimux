// Package build exposes immutable build-time constants with zero dependencies,
// so thin binaries (e.g., the shim mode of cmd/aimux) can reference version
// info without pulling in the full daemon dependency graph via pkg/server.
package build

// Version is the canonical aimux version string. It remains immutable at runtime
// but is a var so tests and release builds can override it via -ldflags -X.
// It is re-exported via pkg/server.Version for backward compatibility with
// existing callers.
var Version = "5.0.2"
