package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runCodex emulates the codex CLI (Rust) process behavior.
//
// Key behaviors replicated:
// - Positional prompt (NOT -p; -p is --profile in codex)
// - --json flag for JSONL output
// - --full-auto flag for headless mode
// - Stderr discipline: all non-response output to stderr
// - Stdin EOF: reads all stdin when no prompt + not TTY
// - JSONL events: thread.started → turn.started → item.completed(agent_message) → turn.completed(usage)
// - Human mode: stdout only final message, everything else stderr
// - Exit: 0 success, 1 error
func runCodex() int {
	fs := flag.NewFlagSet("codex", flag.ContinueOnError)

	jsonMode := fs.Bool("json", false, "output JSONL events to stdout")
	fullAuto := fs.Bool("full-auto", false, "auto-approve all actions")
	model := fs.String("m", "gpt-5.4", "model name")
	sandbox := fs.String("sandbox", "", "sandbox mode (read-only)")
	config := fs.String("c", "", "config key=value")

	// Parse flags — remaining args are the prompt (positional)
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "codex: %v\n", err)
		return 1
	}

	_ = fullAuto // used for behavior selection, not output
	_ = sandbox
	_ = config

	// Resolve prompt: positional arg or stdin
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" || prompt == "-" {
		// Read from stdin (codex behavior: reads to EOF when no prompt or prompt is "-")
		if !isStdinTerminal() {
			prompt = readStdinToEOF()
			prompt = strings.TrimSpace(prompt)
		}
	}

	if prompt == "" {
		toStderr("No prompt provided. Either specify one as an argument or pipe the prompt into stdin.")
		return 1
	}

	// Stderr diagnostics (matching codex stderr discipline)
	toStderr("codex exec: model=%s", *model)

	// Generate response text (echo prompt back with wrapper)
	response := fmt.Sprintf("Codex response to: %s", prompt)

	if *jsonMode {
		return codexJSONL(response)
	}
	return codexHuman(response)
}

// codexJSONL emits JSONL events matching real codex --json output.
//
// Real format (from cli-output-formats.md):
//
//	{"type":"thread.started","thread_id":"<uuid>"}
//	{"type":"turn.started"}
//	{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"<response>"}}
//	{"type":"turn.completed","usage":{"input_tokens":N,"cached_input_tokens":0,"output_tokens":M}}
func codexJSONL(response string) int {
	threadID := pseudoUUID()

	// Event 1: thread.started
	simulateLatency(10, 30)
	emitJSONL(os.Stdout, map[string]any{
		"type":      "thread.started",
		"thread_id": threadID,
	})

	// Event 2: turn.started
	simulateLatency(5, 15)
	emitJSONL(os.Stdout, map[string]any{
		"type": "turn.started",
	})

	// Event 3: item.completed (agent_message) — the core content
	// Note: item is NESTED — event.item.type = "agent_message", event.item.text = response
	simulateLatency(50, 150)
	emitJSONL(os.Stdout, map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   "item_0",
			"type": "agent_message",
			"text": response,
		},
	})

	// Event 4: turn.completed with usage stats
	simulateLatency(5, 15)
	emitJSONL(os.Stdout, map[string]any{
		"type": "turn.completed",
		"usage": map[string]any{
			"input_tokens":        42,
			"cached_input_tokens": 0,
			"output_tokens":       17,
		},
	})

	// Token usage summary to stderr (matching codex behavior)
	toStderr("Tokens: 42 input, 17 output")

	return 0
}

// codexHuman emits human-readable output matching codex default (non-JSON) mode.
// All diagnostic output to stderr, only the final message to stdout.
func codexHuman(response string) int {
	// In human mode, everything except final response goes to stderr
	toStderr("Processing...")

	simulateLatency(50, 150)

	// Only the final assistant message goes to stdout
	fmt.Println(response)

	// Usage stats to stderr
	toStderr("Tokens: 42 input, 17 output")

	return 0
}
