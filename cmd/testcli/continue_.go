package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runContinue emulates the continue CLI (Node) process behavior.
//
// Key behaviors replicated:
// - -p / --print for headless mode
// - init.ts intercepts console before any imports
// - readStdinSync: synchronous stdin read blocks startup
// - gracefulExit: flushes telemetry then process.exit
// - Double Ctrl+C pattern for SIGINT
// - --output json for JSON output
func runContinue() int {
	fs := flag.NewFlagSet("continue", flag.ContinueOnError)

	printPrompt := fs.String("p", "", "print mode prompt")
	printLong := fs.String("print", "", "print mode prompt (long flag)")
	output := fs.String("output", "text", "output format: text, json")
	model := fs.String("model", "claude-sonnet-4-6", "model name")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "continue: %v\n", err)
		return 1
	}

	_ = model

	prompt := *printPrompt
	if prompt == "" {
		prompt = *printLong
	}
	if prompt == "" {
		prompt = strings.Join(fs.Args(), " ")
	}

	// readStdinSync: blocks until stdin read complete
	if !isStdinTerminal() {
		stdinContent := strings.TrimSpace(readStdinToEOF())
		if stdinContent != "" {
			if prompt != "" {
				prompt = stdinContent + "\n\n" + prompt
			} else {
				prompt = stdinContent
			}
		}
	}

	if prompt == "" {
		toStderr("Error: No prompt. Use -p or --print flag.")
		return 1
	}

	response := fmt.Sprintf("Continue response to: %s", prompt)

	if *output == "json" {
		return continueJSON(response)
	}

	simulateLatency(50, 150)
	fmt.Println(response)
	return 0
}

// continueJSON emits JSON output for continue --output json.
func continueJSON(response string) int {
	simulateLatency(50, 150)
	emitJSONL(os.Stdout, map[string]any{
		"type":    "message",
		"content": response,
		"status":  "complete",
	})
	return 0
}
