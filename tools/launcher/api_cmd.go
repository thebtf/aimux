// Package main — api_cmd.go implements the "api" subcommand handler.
//
// runAPI sends a one-shot prompt to an HTTP API executor (OpenAI, Anthropic, or
// Google AI) using the same pkg/executor/api surface that aimux production uses.
// The ExecutorV2 is wrapped in the L1 debugExecutor so structured JSONL events
// are emitted alongside the response, identical to the "cli" subcommand.
//
// Usage:
//
//	launcher api --provider openai --model gpt-4o-mini --prompt "say hi"
//	launcher api --provider anthropic --prompt "hello" --stream --log out.jsonl
//	launcher api --provider google --api-key-env GOOGLE_AI_API_KEY --prompt "test"
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

// runAPI handles the "api" subcommand. Returns an OS exit code.
//
// It constructs the appropriate API executor for the given --provider, wraps it
// in the L1 debugExecutor, then calls Send or SendStream depending on --stream.
// Duration and token counts are printed to stderr after the call completes.
func runAPI(args []string) int {
	fs := flag.NewFlagSet("launcher api", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	provider  := fs.String("provider", "", "API provider: openai|anthropic|google (required)")
	model     := fs.String("model", "", "model override (empty = provider default)")
	prompt    := fs.String("prompt", "", "prompt text to send (required)")
	apiKeyEnv := fs.String("api-key-env", "", "env var name for the API key (default per provider: OPENAI_API_KEY / ANTHROPIC_API_KEY / GOOGLE_AI_API_KEY)")
	stream    := fs.Bool("stream", false, "use SendStream (streaming chunks) instead of Send")
	logPath   := fs.String("log", "", "path to append JSONL events (empty = no log)")

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already printed the error.
		return 2
	}

	if *provider == "" {
		fmt.Fprintln(os.Stderr, "launcher api: --provider is required (openai|anthropic|google)")
		fs.Usage()
		return 2
	}
	if *prompt == "" {
		fmt.Fprintln(os.Stderr, "launcher api: --prompt is required")
		fs.Usage()
		return 2
	}

	// Resolve the default env var name when the caller did not specify one.
	keyEnv := *apiKeyEnv
	if keyEnv == "" {
		keyEnv = defaultAPIKeyEnv(*provider)
		if keyEnv == "" {
			// Unknown provider — buildAPIBackend will surface the proper error below.
			keyEnv = "UNKNOWN_PROVIDER_API_KEY"
		}
	}

	// Signal-aware context: Ctrl+C or SIGTERM cancels and triggers error event + exit 130.
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Open the JSONL sink (or nop sink when --log is absent).
	sink, sinkCloser, err := mkSink(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "launcher: open log %q: %v\n", *logPath, err)
		return 1
	}
	defer func() { _ = sinkCloser.Close() }()

	// Build the API backend. buildAPIBackend reads the API key from keyEnv and
	// applies the provider's default model when *model is empty.
	innerExec, err := buildAPIBackend(*provider, *model, keyEnv)
	if err != nil {
		errMsg := fmt.Sprintf("%v", err)
		fmt.Fprintf(os.Stderr, "launcher: %s\n", errMsg)
		sink.Emit(KindError, errorPayload{Source: "launcher", Message: errMsg})
		return 1
	}
	defer func() {
		if closeErr := innerExec.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "launcher api: executor close: %v\n", closeErr)
		}
	}()

	// Wrap in L1 decorator. API executors have no CLI name, breaker registry, or
	// cooldown tracker in the standalone launcher context — pass empty/nil for those.
	// The decorator will still emit spawn_args, complete, and classify events.
	breakerReg := executor.NewBreakerRegistry(executor.BreakerConfig{
		FailureThreshold: 3,
		CooldownSeconds:  60,
		HalfOpenMaxCalls: 1,
	})
	cooldownTracker := executor.NewModelCooldownTracker()
	// Use "api:<provider>" as the cliName so the breaker registry and JSONL events
	// carry a meaningful identifier even though this is not a CLI executor.
	// API executors do not implement LegacyAccessor, so diag=false always falls
	// through to the standard path inside debugExecutor.Send.
	dbgExec := newDebugExecutor(innerExec, sink, "api:"+*provider, breakerReg, cooldownTracker, false)

	sendMsg := types.Message{Content: *prompt}

	ctx, cancel := context.WithTimeout(sigCtx, defaultTimeout)
	defer cancel()

	start := time.Now()
	var resp *types.Response

	if *stream {
		// Streaming path: print each chunk as it arrives; resp.Content not re-printed.
		onChunk := func(c types.Chunk) {
			if c.Content != "" {
				fmt.Print(c.Content)
			}
		}
		resp, err = dbgExec.SendStream(ctx, sendMsg, onChunk)
		// Ensure streaming output ends with a newline.
		fmt.Println()
	} else {
		resp, err = dbgExec.Send(ctx, sendMsg)
	}
	duration := time.Since(start)

	// Signal check — exit 130 on Ctrl+C / SIGTERM.
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
		fmt.Fprintf(os.Stderr, "launcher api: send failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "[duration: %.3fs]\n", duration.Seconds())
		return 1
	}

	if !*stream {
		// Non-streaming: print the complete response content.
		fmt.Print(resp.Content)
		if len(resp.Content) > 0 && resp.Content[len(resp.Content)-1] != '\n' {
			fmt.Println()
		}
	}

	// Print duration and token counts to stderr. Token counts are omitted when
	// both are zero (e.g., the executor did not report them).
	if resp.TokensUsed.Input > 0 || resp.TokensUsed.Output > 0 {
		fmt.Fprintf(os.Stderr, "[duration: %.3fs tokens: %d in / %d out]\n",
			duration.Seconds(), resp.TokensUsed.Input, resp.TokensUsed.Output)
	} else {
		fmt.Fprintf(os.Stderr, "[duration: %.3fs]\n", duration.Seconds())
	}

	return 0
}
