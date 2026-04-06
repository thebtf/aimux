package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runGptme emulates the gptme CLI (Python) process behavior.
//
// Key behaviors replicated:
// - --non-interactive -n "prompt" for headless
// - Plain text via rich Console
// - _read_stdin() for piped input (wrapped in code block)
// - Exit after single prompt in non-interactive mode
// - atexit goodbye message
// - Python startup delay
func runGptme() int {
	fs := flag.NewFlagSet("gptme", flag.ContinueOnError)

	nonInteractive := fs.Bool("non-interactive", false, "non-interactive mode")
	name := fs.String("n", "", "session name or prompt")
	model := fs.String("model", "gpt-4.1", "model name")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "gptme: %v\n", err)
		return 1
	}

	_ = nonInteractive
	_ = model

	prompt := *name
	if prompt == "" {
		prompt = strings.Join(fs.Args(), " ")
	}

	// Read stdin if piped (gptme wraps in code block)
	if !isStdinTerminal() {
		stdinContent := strings.TrimSpace(readStdinToEOF())
		if stdinContent != "" {
			if prompt != "" {
				prompt = prompt + "\n```stdin\n" + stdinContent + "\n```"
			} else {
				prompt = "```stdin\n" + stdinContent + "\n```"
			}
		}
	}

	if prompt == "" {
		toStderr("Error: No prompt provided.")
		return 1
	}

	// Python startup delay
	simulateLatency(100, 200)

	response := fmt.Sprintf("Gptme response to: %s", prompt)
	fmt.Println(response)

	// atexit goodbye message (gptme behavior)
	toStderr("Goodbye! (resume with: gptme --name test)")

	return 0
}
