// Package build exposes immutable build-time constants with zero dependencies,
// so thin binaries (e.g., the shim mode of cmd/aimux) can reference version
// info without pulling in the full daemon dependency graph via pkg/server.
//
// Version injection (priority order, highest wins):
//  1. Release builds — set via -ldflags '-X github.com/thebtf/aimux/pkg/build.Version=vX.Y.Z'
//     (see .goreleaser.yaml + scripts/build.{ps1,sh}).
//  2. Module installs — `go install ...@vX.Y.Z` populates info.Main.Version via
//     runtime/debug.ReadBuildInfo(); init() picks it up if no ldflags ran.
//  3. Plain `go build` — init() embeds the VCS revision (short hash) so locally
//     built binaries are still uniquely identifiable.
//  4. Final fallback — the literal "0.0.0-dev" default below. Never matches a
//     real release: always a signal that injection was skipped.
package build

import "runtime/debug"

// Version is the canonical aimux version string. Default is the explicit
// "0.0.0-dev" sentinel — release/install builds override via ldflags or
// debug.ReadBuildInfo() so this literal NEVER ships in a real release.
var Version = "0.0.0-dev"

// Commit is the short git revision the binary was built from. Empty when
// neither ldflags nor VCS info is available (e.g., source tarball builds).
var Commit = ""

// BuildDate is the RFC3339 build timestamp. Empty when ldflags injection
// did not run (debug.ReadBuildInfo does not surface build time).
var BuildDate = ""

func init() {
	// If ldflags already injected a real Version, leave everything alone.
	if Version != "0.0.0-dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	// Prefer module version (set by `go install ...@vX.Y.Z`).
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
	}
	// Always populate Commit from VCS info when available, even when Version
	// stays at "0.0.0-dev" — gives plain `go build` a unique identifier.
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				if Commit == "" {
					Commit = s.Value[:7]
				}
				if Version == "0.0.0-dev" {
					Version = "0.0.0-dev+" + s.Value[:7]
				}
			}
		case "vcs.time":
			if BuildDate == "" {
				BuildDate = s.Value
			}
		}
	}
}

// String returns the verbose version line: "vX.Y.Z (commit abc1234, built 2026-04-27T12:00:00Z)".
// Components with empty values are omitted.
func String() string {
	out := Version
	if Commit != "" || BuildDate != "" {
		out += " ("
		first := true
		if Commit != "" {
			out += "commit " + Commit
			first = false
		}
		if BuildDate != "" {
			if !first {
				out += ", "
			}
			out += "built " + BuildDate
		}
		out += ")"
	}
	return out
}
