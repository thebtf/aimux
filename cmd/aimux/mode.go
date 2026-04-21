package main

import (
	"fmt"
	"os"
	"slices"

	"github.com/thebtf/mcp-mux/muxcore/engine"
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

	// FR-5: AIMUX_NO_ENGINE=1 is deprecated and ignored. Log to stderr and
	// proceed — the muxcore engine is always used after AIMUX-6.
	if env("AIMUX_NO_ENGINE") == "1" {
		fmt.Fprintf(os.Stderr,
			"aimux: AIMUX_NO_ENGINE=1 is deprecated and ignored; aimux always runs via muxcore engine (daemon or shim mode).\n",
		)
	}

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

	// FR-1: Daemon mode when the daemon flag is present anywhere in args.
	// slices.Contains is an exact match — prefix matches like
	// "--muxcore-daemon-debug" do NOT trigger daemon mode (spec EC-3).
	if slices.Contains(args, daemonFlag) {
		return ModeDaemon, nil
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
	daemonFlag := engine.Config{}.DaemonFlag
	if daemonFlag == "" {
		daemonFlag = "--muxcore-daemon"
	}
	return daemonFlag
}
