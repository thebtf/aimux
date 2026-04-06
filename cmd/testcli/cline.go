package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runCline emulates the cline CLI (Node) process behavior.
//
// Key behaviors replicated:
// - NDJSON streaming (--json flag)
// - readStdinIfPiped: reads stdin if pipe/file, null if TTY
// - drainStdout: explicitly waits for stdout drain before exit
// - Graceful SIGTERM handling
// - Plain text mode: only final result to stdout
// - suppressConsoleUnlessVerbose: console.log suppressed by default
func runCline() int {
	fs := flag.NewFlagSet("cline", flag.ContinueOnError)

	jsonMode := fs.Bool("json", false, "output NDJSON events")
	yolo := fs.Bool("yolo", false, "auto-approve mode")
	model := fs.String("model", "claude-sonnet-4-6", "model name")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "cline: %v\n", err)
		return 1
	}

	_ = yolo
	_ = model

	prompt := strings.Join(fs.Args(), " ")

	// readStdinIfPiped: read if stdin is pipe/file
	if !isStdinTerminal() {
		stdinContent := strings.TrimSpace(readStdinToEOF())
		if stdinContent != "" {
			if prompt != "" {
				prompt = stdinContent + "\n" + prompt
			} else {
				prompt = stdinContent
			}
		}
	}

	if prompt == "" {
		toStderr("Error: no prompt provided")
		return 1
	}

	response := fmt.Sprintf("Cline response to: %s", prompt)

	if *jsonMode {
		return clineJSON(response)
	}

	// Plain text: only final result to stdout
	simulateLatency(50, 150)
	fmt.Println(response)

	// drainStdout emulation
	os.Stdout.Sync()
	return 0
}

// clineJSON emits NDJSON events matching cline --json mode.
func clineJSON(response string) int {
	// Cline streams message events as they arrive
	simulateLatency(20, 60)
	emitJSONL(os.Stdout, map[string]any{
		"type":    "message",
		"role":    "assistant",
		"content": response,
	})

	simulateLatency(5, 15)
	emitJSONL(os.Stdout, map[string]any{
		"type":   "completion",
		"status": "success",
	})

	// drainStdout
	os.Stdout.Sync()
	return 0
}
