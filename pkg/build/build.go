// Package build exposes immutable build-time constants with zero dependencies,
// so thin binaries (e.g., the shim mode of cmd/aimux) can reference version
// info without pulling in the full daemon dependency graph via pkg/server.
package build

// Version is the canonical aimux version string. Populated here at build time
// and re-exported via pkg/server.Version for backward compatibility with
// existing callers.
const Version = "4.5.2"
