// Package main — interactive.go implements bidirectional interactive (TUI) mode
// for the session subcommand when backed by ConPTY or PTY executors.
//
// runInteractiveSession owns the full I/O loop for interactive sessions:
//   - A reader goroutine drains sess.Stdout() and forwards raw bytes to the
//     operator's terminal, passing ANSI escape sequences through unmodified so
//     the CLI's TUI (header bar, status bar, prompt input) renders correctly.
//   - An input goroutine reads operator stdin line by line and writes to
//     sess.Stdin().  Slash-commands (/quit, /dump, etc.) are processed inline
//     without forwarding to the session.
//   - The main loop selects on stdout data, operator input results, and context
//     cancellation (SIGINT/SIGTERM).
//
// Contrast with runREPL (repl.go), which is the request/response loop used by
// the pipe backend: it sends a prompt, waits for a full response, then emits a
// turn{agent} event.  runInteractiveSession makes no such assumption — bytes
// flow as they arrive in both directions.
//
// WARNING (NFR-7): when --log is active and --executor is conpty or pty, the
// raw_bytes events written to the log MAY contain terminal escape sequences,
// pasted secrets, or other sensitive data.  A warning is printed to stderr at
// session start.
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// interactiveRawEvent is the JSONL payload for raw bytes forwarded from the
// session stdout to the operator terminal (kind="stdout", stream="raw").
type interactiveRawEvent struct {
	Stream   string `json:"stream"`
	BytesHex string `json:"bytes_hex"`
}

// interactiveCompleteEvent is the JSONL payload emitted when the session
// process exits cleanly (kind="complete").
type interactiveCompleteEvent struct {
	Reason string `json:"reason"`
}

// inputResult carries one operator input line or a terminal error from the
// input goroutine to the main select loop.
type inputResult struct {
	line string
	err  error // io.EOF when stdin is exhausted; other errors are propagated.
}

// runInteractiveSession drives a bidirectional interactive session against sess.
//
// It requires sess to satisfy types.SessionPipes.  If the assertion fails, it
// prints an error to stderr and returns exit code 1.
//
// Parameters:
//   - ctx:    parent context; cancellation (SIGINT/SIGTERM) exits with code 130.
//   - sess:   live Session.  runInteractiveSession is responsible for sess.Close().
//   - sink:   JSONL event sink (may be nopSink).
//   - output: where TUI bytes are written (os.Stdout in production; bytes.Buffer
//     in tests so tests do not pollute real stdout).
//   - input:  operator's stdin reader (os.Stdin in production; strings.Reader
//     in tests).
func runInteractiveSession(
	ctx context.Context,
	sess types.Session,
	sink EventSink,
	output io.Writer,
	input io.Reader,
) int {
	pipes, ok := sess.(types.SessionPipes)
	if !ok {
		fmt.Fprintf(os.Stderr,
			"launcher session: interactive mode requires a SessionPipes-capable session "+
				"(conpty or pty backend); pipe backend does not satisfy this interface\n")
		return 1
	}

	fmt.Fprintf(os.Stderr,
		"[session %s ready — interactive TUI mode]\n", sess.ID())
	fmt.Fprintln(os.Stderr,
		"[NFR-7 warning: log may contain terminal escapes, pasted secrets; use with care]")
	fmt.Fprintln(os.Stderr, "[type /help for slash-commands, /quit to exit]")

	// stdoutCh carries raw byte chunks from the reader goroutine to the main loop.
	// Closing stdoutCh signals that the session process has exited (EOF on stdout).
	stdoutCh := make(chan []byte, 64)
	// inputCh carries operator stdin lines to the main loop.
	inputCh := make(chan inputResult, 16)

	// Reader goroutine: drains sess.Stdout() and forwards chunks.
	// The goroutine exits when the session process closes stdout (EOF or error).
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pipes.Stdout().Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case stdoutCh <- chunk:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				close(stdoutCh)
				return
			}
		}
	}()

	// Input goroutine: reads operator stdin line by line and forwards to inputCh.
	// Operator stdin EOF is forwarded as an inputResult{err: io.EOF} so the main
	// loop can decide whether to keep the session alive (session continues until
	// /quit or process exit — stdin EOF alone does not close the session).
	go func() {
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			select {
			case inputCh <- inputResult{line: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		// Scanner done — EOF or scanner error.
		scanErr := scanner.Err()
		if scanErr == nil {
			scanErr = io.EOF
		}
		select {
		case inputCh <- inputResult{err: scanErr}:
		case <-ctx.Done():
		}
	}()

	for {
		// Drain any pending stdout chunks BEFORE servicing operator input.
		// Rationale: in interactive TUI mode the rendered output must always
		// reach the operator's terminal before any operator input is handled.
		// If both stdoutCh and inputCh are ready, Go's select picks at random —
		// that race lets "/quit" close the session before the TUI render
		// reaches the operator. Drain stdout opportunistically with a brief
		// blocking window (5 ms) so the reader goroutine has a chance to
		// surface its first chunk before we look at inputCh. 5 ms is below
		// human perception in interactive use yet long enough to absorb Go's
		// goroutine-startup jitter on Windows.
		stdoutDrainExited := false
		for !stdoutDrainExited {
			select {
			case chunk, open := <-stdoutCh:
				if !open {
					sink.Emit(KindComplete, interactiveCompleteEvent{Reason: "process_exit"})
					_ = sess.Close()
					return 0
				}
				_, _ = output.Write(chunk)
				sink.Emit(KindStdout, interactiveRawEvent{
					Stream:   "raw",
					BytesHex: hex.EncodeToString(chunk),
				})
			case <-time.After(5 * time.Millisecond):
				stdoutDrainExited = true
			}
		}

		select {
		case <-ctx.Done():
			// SIGINT / SIGTERM — close session and exit 130.
			sink.Emit(KindError, errorPayload{
				Source:  "launcher",
				Message: ctx.Err().Error(),
				Signal:  "interrupt",
			})
			_ = sess.Close()
			return 130

		case chunk, open := <-stdoutCh:
			if !open {
				// Session stdout closed — process exited.
				sink.Emit(KindComplete, interactiveCompleteEvent{Reason: "process_exit"})
				_ = sess.Close()
				return 0
			}
			// Forward raw bytes to operator terminal (ANSI passes through).
			_, _ = output.Write(chunk)
			// Emit to log when sink is active.
			sink.Emit(KindStdout, interactiveRawEvent{
				Stream:   "raw",
				BytesHex: hex.EncodeToString(chunk),
			})

		case res, open := <-inputCh:
			if !open {
				// inputCh closed — goroutine exited without EOF sentinel.
				// Keep session alive until stdout closes or ctx is cancelled.
				inputCh = nil
				continue
			}
			if res.err != nil {
				// Operator stdin exhausted (EOF) or scanner error.
				// Session stays alive — only /quit or process exit terminates it.
				// Nil out inputCh so we stop selecting on it (avoids busy loop).
				inputCh = nil
				continue
			}

			line := res.line
			if strings.HasPrefix(line, "/") {
				// Slash-command: process without forwarding to session.
				code := handleInteractiveSlashCommand(line, sess, sink)
				if code >= 0 {
					return code
				}
				continue
			}

			// Regular input: forward to session stdin with newline.
			if _, err := fmt.Fprintln(pipes.Stdin(), line); err != nil {
				fmt.Fprintf(os.Stderr,
					"launcher session: stdin write error: %v\n", err)
			}
		}
	}
}

// handleInteractiveSlashCommand processes slash-commands in interactive mode.
//
// Returns the OS exit code (≥ 0) when the session should terminate, or -1 to
// continue the loop.  Supported commands: /quit, /help.  /reset, /dump, /save,
// /raw, /history print a notice that they are not supported in interactive mode
// (state tracking requires the request/response model in runREPL).
func handleInteractiveSlashCommand(line string, sess types.Session, sink EventSink) int {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return -1
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/quit":
		fmt.Fprintln(os.Stderr, "[quit — closing session]")
		sink.Emit(KindComplete, interactiveCompleteEvent{Reason: "quit"})
		_ = sess.Close()
		return 0

	case "/help":
		printInteractiveHelp()
		return -1

	case "/reset", "/dump", "/save", "/raw", "/history":
		fmt.Fprintf(os.Stderr,
			"[%s] not supported in interactive TUI mode — use /quit to exit\n", cmd)
		return -1

	default:
		fmt.Fprintf(os.Stderr,
			"[unknown command %q — type /help for list]\n", cmd)
		return -1
	}
}

// printInteractiveHelp prints the slash-command reference for interactive mode.
func printInteractiveHelp() {
	fmt.Fprintln(os.Stderr, "Interactive TUI slash-commands:")
	fmt.Fprintln(os.Stderr, "  /quit    close session and exit 0")
	fmt.Fprintln(os.Stderr, "  /help    show this help")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "All other input is forwarded directly to the CLI process.")
	fmt.Fprintln(os.Stderr, "Use the CLI's own keybindings (e.g. Ctrl+C, Ctrl+D) when appropriate.")
	fmt.Fprintln(os.Stderr, "For request/response REPL with full slash-command support use --executor pipe.")
}
