package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runAider emulates the aider CLI (Python) process behavior.
//
// Key behaviors replicated:
// - --message "prompt" for non-interactive
// - --no-auto-commits --no-dirty-commits --no-auto-test --no-auto-lint (safety flags)
// - --yes for auto-approve
// - Plain text output via rich Console
// - No SIGTERM handling
// - Python-like startup delay
// - NO structured JSON output (plain text only)
func runAider() int {
	fs := flag.NewFlagSet("aider", flag.ContinueOnError)

	message := fs.String("message", "", "prompt message")
	noAutoCommits := fs.Bool("no-auto-commits", false, "disable auto commits")
	noDirtyCommits := fs.Bool("no-dirty-commits", false, "disable dirty commits")
	noAutoTest := fs.Bool("no-auto-test", false, "disable auto test")
	noAutoLint := fs.Bool("no-auto-lint", false, "disable auto lint")
	yes := fs.Bool("yes", false, "auto-approve")
	model := fs.String("model", "gpt-4.1", "model name")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "aider: %v\n", err)
		return 1
	}

	_ = noAutoCommits
	_ = noDirtyCommits
	_ = noAutoTest
	_ = noAutoLint
	_ = yes
	_ = model

	prompt := *message
	if prompt == "" {
		prompt = strings.Join(fs.Args(), " ")
	}

	if prompt == "" {
		toStderr("Error: No prompt. Use --message flag.")
		return 1
	}

	// Python startup delay
	simulateLatency(100, 200)

	response := fmt.Sprintf("Aider response to: %s", prompt)

	// Plain text output (rich Console style — no JSON, no JSONL)
	fmt.Println(response)

	return 0
}
