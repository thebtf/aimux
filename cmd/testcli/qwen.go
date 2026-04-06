package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runQwen emulates the qwen-code CLI (Node, gemini-cli fork) process behavior.
//
// Key behaviors replicated:
// - Nearly identical to gemini (forked from gemini-cli)
// - -p prompt flag mandatory for headless
// - --output-format text|json|stream-json
// - JSON mode: same buffering trap as gemini
// - CRITICAL DIFFERENCE: explicit SIGTERM handler (not in gemini base)
// - SIGINT handler also registered
func runQwen() int {
	fs := flag.NewFlagSet("qwen", flag.ContinueOnError)

	prompt := fs.String("p", "", "prompt (mandatory for headless)")
	outputFormat := fs.String("output-format", "text", "output format: text, json, stream-json")
	model := fs.String("m", "qwen3-coder", "model name")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "qwen: %v\n", err)
		return 1
	}

	if *prompt == "" {
		remaining := fs.Args()
		if len(remaining) > 0 {
			toStderr("[WARNING] Positional prompt — in real qwen this starts interactive mode.")
			*prompt = strings.Join(remaining, " ")
		}
	}

	if *prompt == "" {
		toStderr("[ERROR] No prompt. Use -p flag.")
		return 1
	}

	response := fmt.Sprintf("Qwen response to: %s", *prompt)

	// Reuse gemini output logic (qwen is a gemini fork)
	switch *outputFormat {
	case "stream-json":
		return geminiStreamJSON(*prompt, response, *model)
	case "json":
		return geminiBufferedJSON(*prompt, response, *model)
	case "text":
		simulateLatency(50, 150)
		fmt.Println(response)
		return 0
	default:
		toStderr("[ERROR] Unknown output format: %s", *outputFormat)
		return 1
	}
}
