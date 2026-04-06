package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// runGoose emulates the goose CLI (Rust) process behavior.
//
// Key behaviors replicated:
// - goose run -t "prompt" or --text "prompt" or --message "prompt"
// - --output-format json|stream-json
// - SIGTERM handler: graceful shutdown (emulated as clean exit)
// - 100ms OTEL delay at exit for telemetry flush
// - Explicit stdout.flush() after streaming
// - Similar to codex in structure but with OTEL delay
// - anstream auto-strips ANSI if no TTY
func runGoose() int {
	fs := flag.NewFlagSet("goose", flag.ContinueOnError)

	text := fs.String("t", "", "prompt text (short flag)")
	textLong := fs.String("text", "", "prompt text (long flag)")
	message := fs.String("message", "", "prompt message (alias)")
	outputFormat := fs.String("output-format", "text", "output format: text, json, stream-json")
	model := fs.String("model", "gpt-4.1", "model name")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "goose: %v\n", err)
		return 1
	}

	// Resolve prompt from flags or positional
	prompt := *text
	if prompt == "" {
		prompt = *textLong
	}
	if prompt == "" {
		prompt = *message
	}
	if prompt == "" {
		prompt = strings.Join(fs.Args(), " ")
	}
	if prompt == "" && !isStdinTerminal() {
		prompt = strings.TrimSpace(readStdinToEOF())
	}

	if prompt == "" {
		toStderr("Error: No prompt provided. Use -t, --text, or --message flag.")
		return 1
	}

	_ = model // used in output

	response := fmt.Sprintf("Goose response to: %s", prompt)

	var exitCode int
	switch *outputFormat {
	case "stream-json":
		exitCode = gooseStreamJSON(prompt, response, *model)
	case "json":
		exitCode = gooseBufferedJSON(response, *model)
	case "text":
		exitCode = gooseText(response)
	default:
		toStderr("Error: Unknown output format: %s", *outputFormat)
		return 1
	}

	// OTEL flush delay: 100ms sleep after completion for telemetry
	// This is a real goose behavior that can cause timing issues
	time.Sleep(100 * time.Millisecond)

	return exitCode
}

// gooseStreamJSON emits JSONL events similar to codex but with goose-specific structure.
func gooseStreamJSON(prompt, response, model string) int {
	simulateLatency(10, 30)

	// Start event
	emitJSONL(os.Stdout, map[string]any{
		"type":  "start",
		"model": model,
	})

	// Message event
	simulateLatency(50, 150)
	emitJSONL(os.Stdout, map[string]any{
		"type":    "message",
		"role":    "assistant",
		"content": response,
	})

	// Done event with usage
	simulateLatency(5, 15)
	emitJSONL(os.Stdout, map[string]any{
		"type": "done",
		"usage": map[string]any{
			"input_tokens":  42,
			"output_tokens": 17,
		},
	})

	// Explicit flush (matching goose's stdout().flush().unwrap())
	os.Stdout.Sync()

	return 0
}

// gooseBufferedJSON writes a single JSON blob at the end.
func gooseBufferedJSON(response, model string) int {
	simulateLatency(100, 300)

	emitBufferedJSON(os.Stdout, map[string]any{
		"model":    model,
		"response": response,
		"usage": map[string]any{
			"input_tokens":  42,
			"output_tokens": 17,
		},
	})

	return 0
}

// gooseText emits plain text with explicit flush.
func gooseText(response string) int {
	simulateLatency(50, 150)
	fmt.Println(response)
	os.Stdout.Sync()
	return 0
}
