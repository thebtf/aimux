package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// runClaude emulates the claude CLI (Bun standalone) process behavior.
//
// Key behaviors replicated:
// - -p prompt flag (mandatory for headless/print mode)
// - --output-format text|json|stream-json
// - Stream-JSON: NDJSON events (NO init event, unlike gemini)
// - JSON mode: single JSON object at end (buffered, same trap as gemini)
// - Text mode: plain text to stdout
// - Stdin: reads if not TTY, 3s peek timeout (emulated as immediate read)
// - Stderr: errors via process.stderr.write
// - EPIPE: exit 0 gracefully
// - --continue session_id for session resume
// - Exit: 0 success, 1 error
func runClaude() int {
	fs := flag.NewFlagSet("claude", flag.ContinueOnError)

	prompt := fs.String("p", "", "prompt (print mode)")
	outputFormat := fs.String("output-format", "text", "output format: text, json, stream-json")
	model := fs.String("model", "claude-sonnet-4-6", "model name")
	continueSession := fs.String("continue", "", "continue session ID")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "claude: %v\n", err)
		return 1
	}

	_ = continueSession // tracked but not used in emulator output

	// If no -p flag, check positional args or stdin
	if *prompt == "" {
		remaining := fs.Args()
		if len(remaining) > 0 {
			*prompt = strings.Join(remaining, " ")
		} else if !isStdinTerminal() {
			// Claude reads stdin with 3s peek timeout
			// In emulator: read immediately (stdin is piped, data arrives fast)
			*prompt = strings.TrimSpace(readStdinToEOF())
		}
	}

	if *prompt == "" {
		toStderr("Error: No prompt provided. Use -p flag or pipe to stdin.")
		return 1
	}

	response := fmt.Sprintf("Claude response to: %s", *prompt)

	switch *outputFormat {
	case "stream-json":
		return claudeStreamJSON(*prompt, response, *model)
	case "json":
		return claudeBufferedJSON(response, *model)
	case "text":
		return claudeText(response)
	default:
		toStderr("Error: Unknown output format: %s", *outputFormat)
		return 1
	}
}

// claudeStreamJSON emits NDJSON events matching real claude --output-format stream-json.
//
// CRITICAL difference from gemini: NO init event. First event is the assistant message.
// Events are message objects with type, role, content fields.
func claudeStreamJSON(prompt, response, model string) int {
	// No init event (unlike gemini)! First output is the message.

	// System message (optional, claude sometimes emits this)
	simulateLatency(20, 60)

	// Assistant message chunks (streaming)
	chunks := splitIntoChunks(response, 3)
	for i, chunk := range chunks {
		simulateLatency(15, 40)
		evt := map[string]any{
			"type": "content_block_delta",
			"index": i,
			"delta": map[string]any{
				"type": "text_delta",
				"text": chunk,
			},
		}
		emitJSONL(os.Stdout, evt)
	}

	// Message complete event
	simulateLatency(10, 20)
	emitJSONL(os.Stdout, map[string]any{
		"type": "message_stop",
	})

	// Final result with usage
	emitJSONL(os.Stdout, map[string]any{
		"type":  "result",
		"model": model,
		"usage": map[string]any{
			"input_tokens":  42,
			"output_tokens": 17,
		},
	})

	return 0
}

// claudeBufferedJSON emulates the JSON buffering trap (same as gemini).
// ZERO stdout until complete, then one JSON blob.
func claudeBufferedJSON(response, model string) int {
	simulateLatency(100, 300)

	result := map[string]any{
		"type":    "result",
		"model":   model,
		"content": response,
		"usage": map[string]any{
			"input_tokens":  42,
			"output_tokens": 17,
		},
		"stop_reason": "end_turn",
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintf(os.Stdout, "%s\n", data)

	return 0
}

// claudeText emits plain text to stdout.
func claudeText(response string) int {
	simulateLatency(50, 150)
	fmt.Println(response)
	return 0
}
