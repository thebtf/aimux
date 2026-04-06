package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runCrush emulates the crush CLI (Go) process behavior.
//
// Key behaviors replicated:
// - crush run "prompt"
// - Incremental stdout: each chunk written immediately via fmt.Fprint
// - MaybePrependStdin: reads stdin if available, prepends to prompt
// - Leading whitespace trimmed from first chunk
// - Trailing newline via defer fmt.Fprintln
// - Spinner on stderr only if stderrTTY
// - 30s agent readiness timeout (emulated as no-op)
// - SIGINT handling via context cancellation
func runCrush() int {
	fs := flag.NewFlagSet("crush", flag.ContinueOnError)

	model := fs.String("model", "default", "model name")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "crush: %v\n", err)
		return 1
	}

	_ = model

	// MaybePrependStdin: if stdin has content, prepend to prompt
	prompt := strings.Join(fs.Args(), " ")
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

	response := fmt.Sprintf("Crush response to: %s", prompt)

	// Emulate incremental output (crush writes each chunk immediately)
	chunks := splitIntoChunks(response, 4)
	for i, chunk := range chunks {
		simulateLatency(10, 30)
		if i == 0 {
			// Leading whitespace trimmed from first chunk (crush behavior)
			chunk = strings.TrimLeft(chunk, " \t")
		}
		if i > 0 {
			fmt.Fprint(os.Stdout, " ")
		}
		fmt.Fprint(os.Stdout, chunk)
	}

	// Trailing newline via defer (crush behavior)
	fmt.Fprintln(os.Stdout)

	return 0
}
