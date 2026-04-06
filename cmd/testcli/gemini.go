package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runGemini emulates the gemini CLI (Node/TypeScript) process behavior.
//
// Key behaviors replicated:
// - -p prompt flag MANDATORY for headless (positional = interactive, hangs!)
// - --output-format text|json|stream-json
// - JSON mode TRAP: --output-format json → ZERO stdout until complete, then one big blob
// - Stream-JSON mode: JSONL events: init → message(user) → message(assistant,delta) → result(stats)
// - Text mode: plain text to stdout
// - Stderr: diagnostics via process.stderr.write('[LEVEL] message')
// - ConsolePatcher: stray stdout → stderr redirect
// - EPIPE handling: exit 0 gracefully
func runGemini() int {
	fs := flag.NewFlagSet("gemini", flag.ContinueOnError)

	prompt := fs.String("p", "", "prompt (mandatory for headless mode)")
	outputFormat := fs.String("output-format", "text", "output format: text, json, stream-json")
	model := fs.String("m", "gemini-2.5-pro", "model name")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "gemini: %v\n", err)
		return 1
	}

	// CRITICAL: positional prompt means interactive mode (hangs forever).
	// In real gemini, using positional prompt without -p starts REPL.
	// For testcli, we just use remaining args if -p is empty.
	if *prompt == "" {
		remaining := fs.Args()
		if len(remaining) > 0 {
			// Simulate the interactive mode trap: warn on stderr but still process
			toStderr("[WARNING] Positional prompt detected — in real gemini this starts interactive mode.")
			*prompt = strings.Join(remaining, " ")
		}
	}

	if *prompt == "" {
		toStderr("[ERROR] No prompt provided. Use -p flag for headless mode.")
		return 1
	}

	// Stderr diagnostics (matching gemini stderr format)
	toStderr("[INFO] Gemini CLI started: model=%s format=%s", *model, *outputFormat)

	// Generate response
	response := fmt.Sprintf("Gemini response to: %s", *prompt)

	switch *outputFormat {
	case "stream-json":
		return geminiStreamJSON(*prompt, response, *model)
	case "json":
		return geminiBufferedJSON(*prompt, response, *model)
	case "text":
		return geminiText(response)
	default:
		toStderr("[ERROR] Unknown output format: %s", *outputFormat)
		return 1
	}
}

// geminiStreamJSON emits JSONL events matching real gemini --output-format stream-json.
//
// Real format (from cli-output-formats.md):
//
//	{"type":"init","timestamp":"<ISO>","session_id":"<uuid>","model":"<model>"}
//	{"type":"message","timestamp":"<ISO>","role":"user","content":"<prompt>"}
//	{"type":"message","timestamp":"<ISO>","role":"assistant","content":"<chunk>","delta":true}
//	{"type":"result","timestamp":"<ISO>","status":"success","stats":{"total_tokens":N,"input_tokens":N,"output_tokens":N,"duration_ms":N}}
func geminiStreamJSON(prompt, response, model string) int {
	sessionID := pseudoUUID()

	// Event 1: init (emitted before first API call, ~100ms after startup)
	simulateLatency(10, 30)
	emitJSONL(os.Stdout, map[string]any{
		"type":       "init",
		"timestamp":  isoTimestamp(),
		"session_id": sessionID,
		"model":      model,
	})

	// Event 2: message (user)
	simulateLatency(5, 15)
	emitJSONL(os.Stdout, map[string]any{
		"type":      "message",
		"timestamp": isoTimestamp(),
		"role":      "user",
		"content":   prompt,
	})

	// Event 3: message (assistant, delta) — streaming chunks
	// Real gemini sends multiple delta events as tokens arrive.
	// We simulate 2-3 chunks for realism.
	chunks := splitIntoChunks(response, 3)
	for _, chunk := range chunks {
		simulateLatency(20, 60)
		emitJSONL(os.Stdout, map[string]any{
			"type":      "message",
			"timestamp": isoTimestamp(),
			"role":      "assistant",
			"content":   chunk,
			"delta":     true,
		})
	}

	// Event 4: result (stats)
	simulateLatency(10, 30)
	emitJSONL(os.Stdout, map[string]any{
		"type":      "result",
		"timestamp": isoTimestamp(),
		"status":    "success",
		"stats": map[string]any{
			"total_tokens":  59,
			"input_tokens":  42,
			"output_tokens": 17,
			"duration_ms":   350,
		},
	})

	return 0
}

// geminiBufferedJSON emulates the JSON buffering trap.
// CRITICAL: ZERO stdout output until the task is complete, then one big JSON blob.
// This is the documented buffering issue that caused months of debugging in v2.
func geminiBufferedJSON(prompt, response, model string) int {
	// Simulate processing time — during this time, NOTHING goes to stdout
	simulateLatency(100, 300)

	// Build the complete response object
	result := map[string]any{
		"model":      model,
		"session_id": pseudoUUID(),
		"prompt":     prompt,
		"response":   response,
		"stats": map[string]any{
			"total_tokens":  59,
			"input_tokens":  42,
			"output_tokens": 17,
			"duration_ms":   350,
		},
	}

	// Write the entire response as one pretty-printed JSON blob
	emitBufferedJSON(os.Stdout, result)

	return 0
}

// geminiText emits plain text to stdout (matching gemini default text mode).
func geminiText(response string) int {
	simulateLatency(50, 150)
	fmt.Println(response)
	return 0
}

// splitIntoChunks splits a string into n roughly equal chunks.
func splitIntoChunks(s string, n int) []string {
	if n <= 1 || len(s) == 0 {
		return []string{s}
	}

	words := strings.Fields(s)
	if len(words) <= n {
		// Each word is a chunk
		chunks := make([]string, 0, len(words))
		for _, w := range words {
			chunks = append(chunks, w+" ")
		}
		return chunks
	}

	chunkSize := (len(words) + n - 1) / n
	var chunks []string
	for i := 0; i < len(words); i += chunkSize {
		end := i + chunkSize
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[i:end], " "))
	}
	return chunks
}

