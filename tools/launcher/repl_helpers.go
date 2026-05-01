// Package main — repl_helpers.go contains REPL display helpers extracted from
// repl.go to keep that file within the 300-line NFR-4 budget.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// printDump prints the current breaker, cooldown, and classify state.
// Per Clarification C5: when no turns have been executed yet, a notice is
// printed first, then the default snapshot is shown anyway.
func printDump(
	cliName string,
	breakerReg *executor.BreakerRegistry,
	cooldown types.ModelCooldownTracker,
	lastClassify executor.ErrorClass,
	hasLastClassify bool,
) {
	if !hasLastClassify {
		fmt.Fprintln(os.Stderr, "No turns yet — breaker default Closed, no cooldown entries, no last classification.")
	}

	fmt.Fprintln(os.Stderr, "--- dump ---")

	// Breaker state.
	if breakerReg != nil && cliName != "" {
		cb := breakerReg.Get(cliName)
		fmt.Fprintf(os.Stderr, "breaker[%s]: state=%s failures=%d\n",
			cliName, breakerStateString(cb.State()), cb.Failures())
	} else {
		fmt.Fprintln(os.Stderr, "breaker: no registry available")
	}

	// Cooldown state.
	if cooldown != nil {
		entries := cooldown.List()
		if len(entries) == 0 {
			fmt.Fprintln(os.Stderr, "cooldown: no active entries")
		} else {
			fmt.Fprintf(os.Stderr, "cooldown: %d active entries\n", len(entries))
			for _, e := range entries {
				fmt.Fprintf(os.Stderr, "  %s expires=%s\n", e.Model, e.ExpiresAt.Format(time.RFC3339))
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "cooldown: no tracker available")
	}

	// Last classify result.
	if hasLastClassify {
		fmt.Fprintf(os.Stderr, "last classify: %s (code %d)\n",
			errorClassName(lastClassify), int(lastClassify))
	} else {
		fmt.Fprintln(os.Stderr, "last classify: none")
	}

	fmt.Fprintln(os.Stderr, "--- end dump ---")
}

// printHistory prints the conversation turn history to stderr.
func printHistory(history []historyEntry) {
	if len(history) == 0 {
		fmt.Fprintln(os.Stderr, "[history] no turns yet")
		return
	}
	fmt.Fprintf(os.Stderr, "[history] %d turns:\n", len(history))
	for _, h := range history {
		fmt.Fprintf(os.Stderr, "  [%d] user: %s\n", h.turnID, truncate(h.userContent, 80))
		fmt.Fprintf(os.Stderr, "  [%d] agent: %s\n", h.turnID, truncate(h.agentContent, 80))
	}
}

// printHelp prints the slash-command reference to stderr.
func printHelp() {
	fmt.Fprintln(os.Stderr, "Slash-commands:")
	fmt.Fprintln(os.Stderr, "  /quit            close session and exit 0")
	fmt.Fprintln(os.Stderr, "  /reset           close session and start a new one (CLI only)")
	fmt.Fprintln(os.Stderr, "  /dump            print breaker / cooldown / classify state")
	fmt.Fprintln(os.Stderr, "  /save <path>     copy current log to <path>")
	fmt.Fprintln(os.Stderr, "  /raw on|off      toggle L2 raw tee (CLI pipe sessions only)")
	fmt.Fprintln(os.Stderr, "  /history         print conversation turns")
	fmt.Fprintln(os.Stderr, "  /help            show this help")
}

// saveLog copies the current log sink contents to destPath.
// Supported only for jsonlSink (file-backed). Returns an error for nopSink.
func saveLog(sink EventSink, destPath string) error {
	js, ok := sink.(*jsonlSink)
	if !ok {
		return fmt.Errorf("no log to save (no --log flag was specified)")
	}

	// Read the underlying file from the sink's writer.
	f, ok := js.w.(*os.File)
	if !ok {
		return fmt.Errorf("log sink is not backed by a file")
	}

	src, err := os.Open(f.Name())
	if err != nil {
		return fmt.Errorf("open source log %s: %w", f.Name(), err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create destination %s: %w", destPath, err)
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// truncate returns s truncated to maxLen runes, appending "..." when truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
