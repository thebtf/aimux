package main

import (
	"fmt"
	"slices"
)

// Mode represents the runtime role of this aimux.exe invocation.
type Mode int

const (
	// ModeShim is the default mode: bridge stdio↔IPC to an existing daemon.
	ModeShim Mode = iota
	// ModeDaemon is the long-lived daemon that serves MCP requests and manages
	// LoomEngine, SQLite, skills, and the orchestrator.
	ModeDaemon
)

// detectMode returns the runtime mode of this aimux.exe invocation, mirroring
// muxcore's own isDaemonMode() / isProxyMode() logic so the two agree under all
// conditions. MCP_MUX_SESSION_ID (proxy) is deliberately rejected with an error
// at the call site — it is NOT a supported branch (see spec FR-4, AIMUX-6).
//
// args: typically os.Args
// env:  typically os.Getenv (function form enables deterministic testing)
func detectMode(args []string, env func(string) string) (Mode, error) {
	daemonFlag := daemonFlagValue()

	// FR-4: Reject proxy-mode invocations (MCP_MUX_SESSION_ID set).
	// AIMUX_ALLOW_LEGACY_PROXY=1 is the undocumented escape hatch for local
	// debugging only — when set, fall through to shim mode without error.
	if env("MCP_MUX_SESSION_ID") != "" {
		if env("AIMUX_ALLOW_LEGACY_PROXY") == "1" {
			return ModeShim, nil
		}
		return 0, fmt.Errorf(
			"aimux: proxy mode (MCP_MUX_SESSION_ID set) is not supported in this version.\n" +
				"       Run aimux standalone (remove MCP_MUX_SESSION_ID from env) OR wait for\n" +
				"       the redesigned mcp-mux integration (see \"Future Integration\" in\n" +
				"       .agent/specs/startup-path-architecture/spec.md).\n" +
				"       To bypass this check for local debugging only, set\n" +
				"       AIMUX_ALLOW_LEGACY_PROXY=1 (undocumented escape hatch).",
		)
	}

	// FR-1: Daemon mode when either daemon flag is present anywhere in args.
	// aimux starts daemon mode with "--muxcore-daemon", while muxcore graceful-
	// restart currently re-execs the successor with "--daemon".
	// Prefix matches like "--muxcore-daemon-debug" still do NOT trigger daemon mode.
	if slices.Contains(args, daemonFlag) || slices.Contains(args, "--daemon") {
		return ModeDaemon, nil
	}

	// ModeDirect (AIMUX_DIRECT_UPSTREAM=1) was removed in v5.1.
	// Upstream-child spawning is no longer used; the in-process SessionHandler
	// (via engine.Config.SessionHandler) replaces that execution path.
	// Detect the legacy env var and fail explicitly so operators receive a clear
	// diagnostic instead of silently falling through to shim mode.
	if env("AIMUX_DIRECT_UPSTREAM") == "1" {
		return 0, fmt.Errorf(
			"aimux: AIMUX_DIRECT_UPSTREAM=1 is no longer supported (ModeDirect removed in v5.1).\n" +
				"       The upstream-child execution path has been replaced by the in-process\n" +
				"       SessionHandler. Remove AIMUX_DIRECT_UPSTREAM from your environment.",
		)
	}

	return ModeShim, nil
}

// daemonFlagValue returns the muxcore daemon flag with the same fallback as
// detectMode uses. Factored out so cmd/aimux/shim.go can share the exact
// same value without drift.
//
// engine.Config{}.DaemonFlag is the zero value of the struct field (empty string
// in a literal); engine.New applies the "--muxcore-daemon" default only when
// constructing. We replicate that fallback here so both pre- and post-v0.21.4
// muxcore behave identically.
func daemonFlagValue() string {
	return "--muxcore-daemon"
}
