// Package main implements testcli — authentic CLI emulators for e2e testing.
//
// Each subcommand replicates the real process behavior of a specific AI CLI tool:
// output format, buffering, stdin handling, stderr discipline, signal handling, timing.
//
// Usage: testcli <cli> [flags] [prompt]
//
// Supported CLIs:
//   codex  — Rust-style JSONL (item.completed events)
//   gemini — Node-style JSONL (init/message/result events)
//   claude — Bun-style NDJSON (content_block_delta events)
//   goose  — Rust-style JSONL + 100ms OTEL delay
//   crush  — Go-style incremental stdout
//   aider  — Python-style plain text (rich Console)
//   qwen   — Node-style JSONL (gemini fork + SIGTERM)
//   gptme  — Python-style plain text + stdin code block
//   cline  — Node-style NDJSON + drainStdout
//   continue — Node-style with console intercept
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: testcli <cli> [flags] [prompt]")
		fmt.Fprintln(os.Stderr, "supported CLIs: codex, gemini, claude, goose, crush, aider, qwen, gptme, cline, continue")
		os.Exit(1)
	}

	subcmd := os.Args[1]
	// Remove the subcommand from os.Args so each handler sees clean args
	os.Args = append(os.Args[:1], os.Args[2:]...)

	var exitCode int
	switch subcmd {
	case "codex":
		exitCode = runCodex()
	case "gemini":
		exitCode = runGemini()
	case "claude":
		exitCode = runClaude()
	case "goose":
		exitCode = runGoose()
	case "crush":
		exitCode = runCrush()
	case "aider":
		exitCode = runAider()
	case "qwen":
		exitCode = runQwen()
	case "gptme":
		exitCode = runGptme()
	case "cline":
		exitCode = runCline()
	case "continue":
		exitCode = runContinue()
	case "slow-codex":
		exitCode = runSlowCodex()
	default:
		fmt.Fprintf(os.Stderr, "testcli: unknown CLI %q\n", subcmd)
		exitCode = 1
	}

	os.Exit(exitCode)
}
