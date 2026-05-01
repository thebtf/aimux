// Package main — repl.go implements the interactive multi-turn REPL for the
// session subcommand.
//
// runREPL drives a line-oriented conversation against a types.Session. Stdin
// lines beginning with "/" are parsed as slash-commands; all other lines are
// forwarded as prompts to sess.Send.
//
// Slash-commands:
//
//	/quit              — close session, return exit 0
//	/reset             — close current session, start a new one via sessionFactory
//	/dump              — print current breaker/cooldown/classify state
//	/save <path>       — snapshot the current log to <path>
//	/raw on|off        — toggle L2 raw tee on the CLI session (if supported)
//	/history           — print recent user/agent turns
//	/help              — list slash-commands
//
// Inactivity timer: while sess.Send is running, a background goroutine prints a
// "." to stderr every second so the operator knows the session is alive.
// If the underlying session inactivity timeout (5 s default) fires before new
// data arrives the message "[inactivity timeout — Session.Send returning]" is
// printed; the inner error is then returned by sess.Send and propagated normally.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// replInactivitySeconds matches defaultInactivitySeconds in pkg/executor/pipe/pipe.go.
// Used for the stderr timer message threshold.
const replInactivitySeconds = 5

// historyEntry records one user/agent exchange in the local conversation history.
type historyEntry struct {
	userContent  string
	agentContent string
	turnID       int
}

// runREPL drives an interactive multi-turn session against a types.Session.
//
// Parameters:
//   - ctx: parent context; cancellation (Ctrl+C) exits with code 130.
//   - sess: the live Session to talk to. runREPL owns its Close lifecycle.
//   - sink: event sink for turn / error events (may be nopSink).
//   - cliName: CLI identifier for /dump output (empty for API sessions).
//   - breakerReg: optional; nil → /dump shows "no breaker registry".
//   - cooldown: optional; nil → /dump shows "no cooldown tracker".
//   - sessionFactory: optional; nil → /reset prints a warning.
//   - rawCallback: optional; called with new raw-mode state on /raw toggle. nil → unsupported.
func runREPL(
	ctx context.Context,
	sess types.Session,
	sink EventSink,
	cliName string,
	breakerReg *executor.BreakerRegistry,
	cooldown types.ModelCooldownTracker,
	sessionFactory func() (types.Session, error),
	rawCallback func(bool),
) int {
	// Print a prompt header so the operator knows the session is ready.
	fmt.Fprintf(os.Stderr, "[session %s ready — type a prompt or /help]\n", sess.ID())

	scanner := bufio.NewScanner(os.Stdin)
	var history []historyEntry
	turnID := 0
	var lastClassify executor.ErrorClass
	hasLastClassify := false

	for {
		// Check for context cancellation before blocking on Scan.
		select {
		case <-ctx.Done():
			sink.Emit(KindError, errorPayload{
				Source:  "launcher",
				Message: ctx.Err().Error(),
				Signal:  "interrupt",
			})
			_ = sess.Close()
			return 130
		default:
		}

		fmt.Fprint(os.Stderr, "> ")

		if !scanner.Scan() {
			// EOF — treat as /quit.
			fmt.Fprintln(os.Stderr, "\n[EOF — closing session]")
			_ = sess.Close()
			return 0
		}

		line := scanner.Text()

		if strings.HasPrefix(line, "/") {
			exit := handleSlashCommand(
				ctx, line, sess, sink, cliName,
				breakerReg, cooldown,
				sessionFactory, rawCallback,
				history, lastClassify, hasLastClassify,
				&sess,
			)
			if exit >= 0 {
				return exit
			}
			continue
		}

		if line == "" {
			continue
		}

		// User turn: emit event and forward to session.
		turnID++
		sink.Emit(KindTurn, turnPayload{Role: "user", Content: line, TurnID: turnID})

		// Run Send with inactivity timer visualization.
		result, err := sendWithTimer(ctx, sess, line)

		if ctx.Err() != nil {
			sink.Emit(KindError, errorPayload{
				Source:  "launcher",
				Message: ctx.Err().Error(),
				Signal:  "interrupt",
			})
			_ = sess.Close()
			return 130
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "launcher session: send error: %v\n", err)
			sink.Emit(KindError, errorPayload{Source: "launcher", Message: err.Error()})
			// Classify the error for /dump reference.
			lastClassify = executor.ClassifyError("", err.Error(), 0)
			hasLastClassify = true
			continue
		}

		agentContent := result.Content
		sink.Emit(KindTurn, turnPayload{Role: "agent", Content: agentContent, TurnID: turnID})

		// Print response to stdout, ensuring trailing newline.
		fmt.Print(agentContent)
		if len(agentContent) > 0 && agentContent[len(agentContent)-1] != '\n' {
			fmt.Println()
		}

		// Classify the result and track for /dump.
		lastClassify = executor.ClassifyError(agentContent, "", result.ExitCode)
		hasLastClassify = true

		history = append(history, historyEntry{
			userContent:  line,
			agentContent: agentContent,
			turnID:       turnID,
		})
	}
}

// sendWithTimer calls sess.Send while printing a progress dot every second to
// stderr. On return it prints a newline to terminate the dot line.
// If the send takes longer than replInactivitySeconds, a message is printed
// explaining that the inactivity timeout may be about to fire.
func sendWithTimer(ctx context.Context, sess types.Session, prompt string) (*types.Result, error) {
	done := make(chan struct{})
	start := time.Now()

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Fprint(os.Stderr, ".")
				if time.Since(start) >= replInactivitySeconds*time.Second {
					fmt.Fprintf(os.Stderr, "\n[inactivity timeout — Session.Send returning]\n")
				}
			}
		}
	}()

	result, err := sess.Send(ctx, prompt)
	close(done)

	// Terminate the dot line.
	fmt.Fprintln(os.Stderr)
	return result, err
}

// handleSlashCommand processes a single slash-command and returns the OS exit
// code (≥ 0) if the REPL should terminate, or -1 to continue the loop.
//
// sess is a pointer to the caller's sess variable so /reset can replace it.
func handleSlashCommand(
	ctx context.Context,
	line string,
	sess types.Session,
	sink EventSink,
	cliName string,
	breakerReg *executor.BreakerRegistry,
	cooldown types.ModelCooldownTracker,
	sessionFactory func() (types.Session, error),
	rawCallback func(bool),
	history []historyEntry,
	lastClassify executor.ErrorClass,
	hasLastClassify bool,
	sessPtr *types.Session,
) int {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return -1
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/quit":
		fmt.Fprintln(os.Stderr, "[quit — closing session]")
		_ = sess.Close()
		return 0

	case "/reset":
		if sessionFactory == nil {
			fmt.Fprintln(os.Stderr, "[reset] warning: no session factory available; cannot reset API sessions")
			return -1
		}
		fmt.Fprintln(os.Stderr, "[reset] closing current session...")
		_ = sess.Close()
		fmt.Fprintln(os.Stderr, "[reset] starting new session...")
		newSess, err := sessionFactory()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[reset] failed to start new session: %v\n", err)
			return 1
		}
		*sessPtr = newSess
		fmt.Fprintf(os.Stderr, "[reset] new session %s ready\n", newSess.ID())
		return -1

	case "/dump":
		printDump(cliName, breakerReg, cooldown, lastClassify, hasLastClassify)
		return -1

	case "/save":
		if len(parts) < 2 {
			fmt.Fprintln(os.Stderr, "[save] usage: /save <path>")
			return -1
		}
		savePath := parts[1]
		if err := saveLog(sink, savePath); err != nil {
			fmt.Fprintf(os.Stderr, "[save] error: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[save] log snapshot written to %s\n", savePath)
		}
		return -1

	case "/raw":
		if len(parts) < 2 {
			fmt.Fprintln(os.Stderr, "[raw] usage: /raw on|off")
			return -1
		}
		toggle := strings.ToLower(parts[1])
		if toggle != "on" && toggle != "off" {
			fmt.Fprintln(os.Stderr, "[raw] usage: /raw on|off")
			return -1
		}
		if rawCallback == nil {
			fmt.Fprintln(os.Stderr, "[raw] raw toggle not supported for this backend")
			return -1
		}
		rawCallback(toggle == "on")
		fmt.Fprintf(os.Stderr, "[raw] raw mode %s\n", toggle)
		return -1

	case "/history":
		printHistory(history)
		return -1

	case "/help":
		printHelp()
		return -1

	default:
		fmt.Fprintf(os.Stderr, "[unknown command %q — type /help for list]\n", cmd)
		return -1
	}
}


