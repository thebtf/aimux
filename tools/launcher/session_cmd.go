// Package main — session_cmd.go implements the "session" subcommand handler.
//
// runSession starts an interactive multi-turn REPL against either a CLI session
// (via SessionFactory.StartSession on the pipe/conpty/pty executor) or an API
// session (if the API executor implements SessionFactory — currently not
// implemented; returns a clear error instead).
//
// Usage:
//
//	launcher session --cli codex --log /tmp/sess.jsonl
//	launcher session --cli claude --executor pipe
//
// Flags:
//
//	--cli <name>          CLI session (mutually exclusive with --provider)
//	--provider <p>        API provider: openai|anthropic|google (mutually exclusive with --cli)
//	--model <m>           model override
//	--config-dir <dir>    aimux config directory (default: "config")
//	--api-key-env <var>   env var name for API key (default: per provider)
//	--executor <choice>   pipe|conpty|pty|auto (CLI mode only; default: pipe)
//	--cwd <dir>           working directory for the spawned process
//	--log <path>          append JSONL events to this file
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// runSession handles the "session" subcommand. Returns an OS exit code.
//
// It builds either a CLI or API executor, acquires a Session via the
// SessionFactory interface, then hands control to runREPL which manages the
// interactive loop until the user runs /quit or stdin is closed.
func runSession(args []string) int {
	fs := flag.NewFlagSet("launcher session", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	cliName    := fs.String("cli", "", "CLI session name (mutually exclusive with --provider)")
	provider   := fs.String("provider", "", "API provider: openai|anthropic|google (mutually exclusive with --cli)")
	model      := fs.String("model", "", "model override (empty = profile/provider default)")
	configDir  := fs.String("config-dir", "config", "aimux config directory (contains default.yaml and cli.d/)")
	apiKeyEnv  := fs.String("api-key-env", "", "env var name for API key (default per provider)")
	execChoice := fs.String("executor", "pipe", "executor backend: pipe|conpty|pty|auto (CLI mode only)")
	cwd        := fs.String("cwd", "", "working directory for the spawned process (empty = inherit)")
	logPath    := fs.String("log", "", "path to append JSONL events (empty = no log)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Mutual exclusion: --cli and --provider are mutually exclusive.
	if *cliName != "" && *provider != "" {
		fmt.Fprintln(os.Stderr, "launcher session: --cli and --provider are mutually exclusive")
		fs.Usage()
		return 2
	}
	if *cliName == "" && *provider == "" {
		fmt.Fprintln(os.Stderr, "launcher session: --cli or --provider required")
		fs.Usage()
		return 2
	}

	// Signal-aware context: Ctrl+C or SIGTERM cancels, REPL exits 130.
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Open the JSONL event sink.
	sink, sinkCloser, err := mkSink(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "launcher: open log %q: %v\n", *logPath, err)
		return 1
	}
	defer func() { _ = sinkCloser.Close() }()

	// Build breaker registry and cooldown tracker (shared across the session).
	breakerReg := executor.NewBreakerRegistry(executor.BreakerConfig{
		FailureThreshold: 3,
		CooldownSeconds:  60,
		HalfOpenMaxCalls: 1,
	})
	cooldownTracker := executor.NewModelCooldownTracker()

	if *cliName != "" {
		return runCLISession(
			sigCtx, *cliName, *configDir, *model, *execChoice, *cwd,
			sink, breakerReg, cooldownTracker,
		)
	}

	// API session path.
	return runAPISession(
		sigCtx, *provider, *model, *apiKeyEnv,
		sink, breakerReg, cooldownTracker,
	)
}

// runCLISession builds a CLI executor, asserts SessionFactory capability, starts
// a Session, and hands control to the REPL.
func runCLISession(
	ctx context.Context,
	cliName, configDir, model, execChoice, cwd string,
	sink EventSink,
	breakerReg *executor.BreakerRegistry,
	cooldown types.ModelCooldownTracker,
) int {
	// Build the inner executor and resolve SpawnArgs.
	// We pass an empty prompt because the session loop will send prompts
	// interactively via Session.Send, not via a one-shot Send call.
	innerExec, spawnArgs, err := buildCLIBackend(configDir, cliName, "", model, "", cwd, execChoice)
	if err != nil {
		errMsg := fmt.Sprintf("backend setup failed: %v", err)
		fmt.Fprintf(os.Stderr, "launcher session: %s\n", errMsg)
		sink.Emit(KindError, errorPayload{Source: "launcher", Message: errMsg})
		return 1
	}
	defer func() {
		if closeErr := innerExec.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "launcher session: executor close: %v\n", closeErr)
		}
	}()

	// Check SessionFactory capability.
	info := innerExec.Info()
	if !info.Capabilities.PersistentSessions {
		fmt.Fprintf(os.Stderr, "launcher session: session not supported for backend %s\n", execChoice)
		return 1
	}

	// CLI adapters (CLIPipeAdapter / ConPTYAdapter / PTYAdapter) do NOT implement
	// SessionFactory directly — the underlying legacy executor (pipe.Executor /
	// conpty.Executor / pty.Executor) does. Probe via LegacyAccessor first; fall
	// back to direct assertion for executors that satisfy the interface themselves
	// (e.g., future stateful API executors). Same pattern as
	// pkg/swarm.MaybeStartSession (`pkg/swarm/swarm.go:578`).
	factory := resolveSessionFactory(innerExec)
	if factory == nil {
		fmt.Fprintf(os.Stderr, "launcher session: executor %q does not implement SessionFactory\n", execChoice)
		return 1
	}

	// Clear the Stdin field: the session prompt is sent interactively, not
	// pre-populated in SpawnArgs (resolver copies --prompt into Stdin).
	spawnArgs.Stdin = ""

	sess, err := factory.StartSession(ctx, spawnArgs)
	if err != nil {
		errMsg := fmt.Sprintf("start session: %v", err)
		fmt.Fprintf(os.Stderr, "launcher session: %s\n", errMsg)
		sink.Emit(KindError, errorPayload{Source: "launcher", Message: errMsg})
		return 1
	}
	defer func() { _ = sess.Close() }()

	// Build a sessionFactory closure for /reset support.
	sessionFactory := func() (types.Session, error) {
		return factory.StartSession(ctx, spawnArgs)
	}

	return runREPL(ctx, sess, sink, cliName, breakerReg, cooldown, sessionFactory, nil)
}

// runAPISession attempts to build an API executor and acquire a session.
// Most API executors do not implement SessionFactory; a clear error is returned
// in that case.
func runAPISession(
	ctx context.Context,
	provider, model, apiKeyEnv string,
	sink EventSink,
	breakerReg *executor.BreakerRegistry,
	cooldown types.ModelCooldownTracker,
) int {
	// Resolve the default API key env var when not specified by the caller.
	keyEnv := apiKeyEnv
	if keyEnv == "" {
		keyEnv = defaultAPIKeyEnv(provider)
		if keyEnv == "" {
			keyEnv = "UNKNOWN_PROVIDER_API_KEY"
		}
	}

	innerExec, err := buildAPIBackend(provider, model, keyEnv)
	if err != nil {
		errMsg := fmt.Sprintf("%v", err)
		fmt.Fprintf(os.Stderr, "launcher session: %s\n", errMsg)
		sink.Emit(KindError, errorPayload{Source: "launcher", Message: errMsg})
		return 1
	}
	defer func() {
		if closeErr := innerExec.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "launcher session: executor close: %v\n", closeErr)
		}
	}()

	// Probe SessionFactory capability. Current API executors (OpenAI, Anthropic,
	// Google AI) do not implement SessionFactory — they are stateless HTTP callers.
	factory := resolveSessionFactory(innerExec)
	if factory == nil {
		fmt.Fprintf(os.Stderr, "launcher session: API session not implemented for provider %s\n", provider)
		fmt.Fprintln(os.Stderr, "  (API executors are stateless; use 'launcher api' for one-shot calls)")
		return 1
	}

	info := innerExec.Info()
	if !info.Capabilities.PersistentSessions {
		fmt.Fprintf(os.Stderr, "launcher session: API session not implemented for provider %s\n", provider)
		return 1
	}

	sess, err := factory.StartSession(ctx, types.SpawnArgs{})
	if err != nil {
		errMsg := fmt.Sprintf("start API session: %v", err)
		fmt.Fprintf(os.Stderr, "launcher session: %s\n", errMsg)
		sink.Emit(KindError, errorPayload{Source: "launcher", Message: errMsg})
		return 1
	}
	defer func() { _ = sess.Close() }()

	return runREPL(ctx, sess, sink, "api:"+provider, breakerReg, cooldown, nil, nil)
}

// resolveSessionFactory returns a SessionFactory bound to the given ExecutorV2,
// or nil when neither the executor nor its underlying legacy executor implements
// SessionFactory. CLI adapters wrap a legacy executor that satisfies the interface;
// LegacyAccessor exposes that legacy executor for probing without breaking the
// adapter abstraction.
//
// This mirrors the probe in pkg/swarm.MaybeStartSession.
func resolveSessionFactory(ex types.ExecutorV2) types.SessionFactory {
	if sf, ok := ex.(types.SessionFactory); ok {
		return sf
	}
	if la, ok := ex.(types.LegacyAccessor); ok {
		if sf, ok := la.Legacy().(types.SessionFactory); ok {
			return sf
		}
	}
	return nil
}
