// Command launcher is a standalone debug tool for testing aimux executor backends
// directly — without starting the MCP daemon, loom, or guidance layers.
//
// It exposes the same pkg/executor surface that aimux production uses, making
// it the canonical way to reproduce CLI spawn issues without rebuilding the
// full server.
//
// Subcommands:
//
//	cli      — one-shot prompt via the best available CLI executor (ConPTY/PTY/Pipe)
//	api      — one-shot prompt via HTTP API executor (openai|anthropic|google)
//	session  — interactive REPL (not yet implemented)
//	replay   — replay a JSONL log (not yet implemented)
//
// Usage:
//
//	go run ./tools/launcher cli --cli gemini --prompt "say hi"
//	go build ./tools/launcher && ./launcher cli --cli codex --prompt "echo test"
//	./launcher api --provider openai --model gpt-4o-mini --prompt "say hi"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	subcommand := os.Args[1]
	switch subcommand {
	case "cli":
		os.Exit(runCLI(os.Args[2:]))
	case "api":
		os.Exit(runAPI(os.Args[2:]))
	case "session":
		fmt.Fprintln(os.Stderr, "session: not yet implemented in this phase")
		os.Exit(2)
	case "replay":
		fmt.Fprintln(os.Stderr, "replay: not yet implemented in this phase")
		os.Exit(2)
	case "--help", "-help", "-h", "help":
		printUsage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "launcher: unknown subcommand %q\n\n", subcommand)
		printUsage()
		os.Exit(2)
	}
}

// printUsage prints the top-level usage summary to stderr.
func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: launcher <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  cli      one-shot prompt via CLI executor (ConPTY/PTY/Pipe)")
	fmt.Fprintln(os.Stderr, "  api      one-shot prompt via HTTP API executor (openai|anthropic|google)")
	fmt.Fprintln(os.Stderr, "  session  interactive REPL               (not yet implemented)")
	fmt.Fprintln(os.Stderr, "  replay   replay JSONL log               (not yet implemented)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run 'launcher <subcommand> --help' for subcommand flags.")
}

// runCLI handles the 'cli' subcommand. Returns an OS exit code.
func runCLI(args []string) int {
	fs := flag.NewFlagSet("launcher cli", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	configDir := fs.String("config-dir", "config", "aimux config directory (contains default.yaml and cli.d/)")
	cliName := fs.String("cli", "", "CLI name to invoke (required; must match a profile in cli.d/)")
	prompt := fs.String("prompt", "", "prompt text to send (required)")
	model := fs.String("model", "", "model override (empty = profile default)")
	effort := fs.String("effort", "", "reasoning effort override (empty = profile default)")
	cwd := fs.String("cwd", "", "working directory for the spawned process (empty = inherit)")
	execChoice := fs.String("executor", "pipe", "executor backend: pipe|conpty|pty|auto (default pipe — deterministic for headless CLIs)")
	logPath := fs.String("log", "", "path to append JSONL events (empty = no log)")
	bypass := fs.Bool("bypass", false, "L2 mode: pipe-only raw subprocess capture pre-StripANSI (writes raw bytes to log)")
	stream := fs.Bool("stream", false, "use SendStream (streaming chunks) instead of Send")

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already printed the error.
		return 2
	}

	if *cliName == "" {
		fmt.Fprintln(os.Stderr, "launcher cli: --cli is required")
		fs.Usage()
		return 2
	}
	if *prompt == "" {
		fmt.Fprintln(os.Stderr, "launcher cli: --prompt is required")
		fs.Usage()
		return 2
	}

	// --stream + --bypass are mutually exclusive: bypass is non-streaming raw
	// capture; stream routes through SendStream.  Detect early before any I/O.
	if *stream && *bypass {
		fmt.Fprintln(os.Stderr, "launcher cli: --stream + --bypass not supported (--bypass is non-streaming raw capture)")
		return 2
	}

	// --bypass is pipe-only; reject incompatible executor choices early (T009).
	if *bypass && *execChoice != "pipe" {
		fmt.Fprintln(os.Stderr, "launcher cli: --bypass is pipe-only; use --executor pipe")
		return 2
	}

	// Signal-aware context: Ctrl+C or SIGTERM cancels sigCtx and triggers the
	// error event + exit 130 path (per Clarification C3).
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Open the JSONL sink (or nop sink when --log is absent).
	sink, sinkCloser, err := mkSink(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "launcher: open log %q: %v\n", *logPath, err)
		return 1
	}
	defer func() { _ = sinkCloser.Close() }()

	// NFR-7: print unredacted-write warning when --bypass and --log are both active.
	if *bypass && *logPath != "" {
		fmt.Fprintf(os.Stderr, "WARNING: --bypass --log %s writes raw subprocess bytes UNREDACTED.\n", *logPath)
		fmt.Fprintf(os.Stderr, "The log file MAY contain API tokens, passwords, or other secrets pasted in\n")
		fmt.Fprintf(os.Stderr, "--prompt or echoed by the backend. Use only in a trusted dev environment.\n")
		fmt.Fprintf(os.Stderr, "Delete the log file after debugging: rm %s\n", *logPath)
	}

	// Build backend once — returns both ExecutorV2 and SpawnArgs.
	innerExec, spawnArgs, err := buildCLIBackend(*configDir, *cliName, *prompt, *model, *effort, *cwd, *execChoice)
	if err != nil {
		errMsg := fmt.Sprintf("backend setup failed: %v", err)
		fmt.Fprintf(os.Stderr, "launcher cli: %s\n", errMsg)
		sink.Emit(KindError, errorPayload{Source: "launcher", Message: errMsg})
		return 1
	}
	defer func() {
		if closeErr := innerExec.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "launcher cli: executor close: %v\n", closeErr)
		}
	}()

	// --bypass: L2 raw path — skip debugExecutor, call runRawCLI directly.
	if *bypass {
		start := time.Now()

		timeout := defaultTimeout
		if spawnArgs.TimeoutSeconds > 0 {
			timeout = time.Duration(spawnArgs.TimeoutSeconds) * time.Second
		}
		ctx, cancel := context.WithTimeout(sigCtx, timeout)
		defer cancel()

		exitCode, rawErr := runRawCLI(ctx, sigCtx, spawnArgs, sink)
		duration := time.Since(start)

		if sigCtx.Err() != nil {
			sink.Emit(KindError, errorPayload{
				Source:  "launcher",
				Message: sigCtx.Err().Error(),
				Signal:  "interrupt",
			})
			fmt.Fprintln(os.Stderr, "launcher: interrupted")
			return 130
		}
		if rawErr != nil {
			fmt.Fprintf(os.Stderr, "launcher cli: bypass spawn failed: %v\n", rawErr)
			return 1
		}

		fmt.Fprintf(os.Stderr, "[bypass duration: %.3fs exit:%d]\n", duration.Seconds(), exitCode)
		if exitCode != 0 {
			return 1
		}
		return 0
	}

	// Construct the L1 decorator.  Provide a fresh breaker registry and cooldown
	// tracker so state events are always emitted (they will show defaults since
	// this is a standalone session with no shared daemon state).
	breakerReg := executor.NewBreakerRegistry(executor.BreakerConfig{
		FailureThreshold: 3,
		CooldownSeconds:  60,
		HalfOpenMaxCalls: 1,
	})
	cooldownTracker := executor.NewModelCooldownTracker()
	dbgExec := newDebugExecutor(innerExec, sink, *cliName, breakerReg, cooldownTracker)

	sendMsg := types.Message{
		Content:  *prompt,
		Metadata: spawnArgsToMetadata(spawnArgs),
	}

	timeout := defaultTimeout
	if spawnArgs.TimeoutSeconds > 0 {
		timeout = time.Duration(spawnArgs.TimeoutSeconds) * time.Second
	}

	ctx, cancel := context.WithTimeout(sigCtx, timeout)
	defer cancel()

	start := time.Now()
	var resp *types.Response

	if *stream {
		// --stream: route through SendStream, printing each chunk as it arrives.
		// onChunk callback writes chunk content to stdout immediately.
		// resp.Content is NOT printed after SendStream to avoid duplication —
		// content was already delivered chunk by chunk.
		onChunk := func(c types.Chunk) {
			if c.Content != "" {
				fmt.Print(c.Content)
			}
		}
		resp, err = dbgExec.SendStream(ctx, sendMsg, onChunk)
		// Ensure streamed output ends with a newline.
		fmt.Println()
	} else {
		resp, err = dbgExec.Send(ctx, sendMsg)
	}
	duration := time.Since(start)

	// Check whether the cancellation was triggered by a signal (sigCtx done)
	// vs a plain timeout.  Signal → exit 130 (POSIX SIGINT convention).
	if sigCtx.Err() != nil {
		sink.Emit(KindError, errorPayload{
			Source:  "launcher",
			Message: sigCtx.Err().Error(),
			Signal:  "interrupt",
		})
		fmt.Fprintln(os.Stderr, "launcher: interrupted")
		return 130
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "launcher cli: send failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "[duration: %.3fs]\n", duration.Seconds())
		return 1
	}

	if !*stream {
		// Non-streaming: print the complete response content.
		fmt.Print(resp.Content)
		// Ensure the output ends with a newline so the shell prompt appears on its
		// own line even when the CLI omits a trailing newline.
		if len(resp.Content) > 0 && resp.Content[len(resp.Content)-1] != '\n' {
			fmt.Println()
		}
	}

	fmt.Fprintf(os.Stderr, "[duration: %.3fs exit:%d]\n", duration.Seconds(), resp.ExitCode)

	if resp.ExitCode != 0 {
		return 1
	}
	return 0
}
