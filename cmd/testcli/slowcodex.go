package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// runSlowCodex emulates a codex-like process that emits three progress lines
// with deliberate pauses between them, then exits. Designed for progress_tail
// e2e polling tests: the caller polls status between lines and observes
// progress_tail changing.
//
// Output format: plain text lines to stdout (no JSONL).
// Each line is printed immediately (no buffering) so the pipe executor picks
// it up before the next sleep.
//
// Flags:
//
//	-p <prompt>  prompt text (ignored, just for flag compatibility)
func runSlowCodex() int {
	fs := flag.NewFlagSet("slow-codex", flag.ContinueOnError)
	prompt := fs.String("p", "", "prompt (ignored)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "slow-codex: %v\n", err)
		return 1
	}
	_ = prompt

	lines := []string{"line 1", "line 2", "line 3"}
	for _, l := range lines {
		fmt.Println(l)
		// Flush is implicit: fmt.Println writes to os.Stdout which is unbuffered
		// for subprocess pipes. Sleep gives the polling test time to call status().
		time.Sleep(200 * time.Millisecond)
	}
	return 0
}
