// Package main — interactive_test.go tests the interactive (ConPTY/PTY) session loop.
//
// Tests:
//   - TestRunInteractiveSession_PipesPassthrough: bytes written to mock's stdout
//     buffer reach os.Stdout; lines read from operator stdin reach mock's stdin.
//   - TestRunInteractiveSession_QuitCommand: /quit closes session cleanly.
//   - TestRunInteractiveSession_StdinEOF: operator stdin EOF leaves session running
//     (session continues until /quit or process exit).
//
// Note: the former TestRunInteractiveSession_NoPipesError test verified a runtime
// error path that no longer exists — runInteractiveSession requires types.InteractivePipes
// at the type level, so the absence of SessionPipes is caught at compile time, not runtime.
package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// --- mockPipedSession -------------------------------------------------------

// mockPipedSession implements types.Session AND types.SessionPipes.
//
// stdout is backed by an io.Pipe so the test controls when EOF is delivered:
// stdout EOF only arrives after Close() is called, which prevents the reader
// goroutine from racing ahead of the input goroutine and exiting via
// process_exit before "hello" reaches stdinBuf.
//
// stdinBuf captures all bytes written by the interactive loop.
type mockPipedSession struct {
	id          string
	stdinBuf    *bytes.Buffer // captures bytes written by the interactive loop
	stdoutPR    *io.PipeReader
	stdoutPW    *io.PipeWriter
	closeCalled bool
}

// newMockPipedSession creates a session whose Stdout() yields stdoutData then
// blocks until Close() is called (which closes the write-end of the pipe,
// delivering EOF to the reader goroutine).
func newMockPipedSession(id string, stdoutData string) *mockPipedSession {
	pr, pw := io.Pipe()
	m := &mockPipedSession{
		id:       id,
		stdinBuf: &bytes.Buffer{},
		stdoutPR: pr,
		stdoutPW: pw,
	}
	// Write the initial TUI data in a background goroutine so the pipe does
	// not block the caller.  The write-end stays open until Close() is called.
	go func() { _, _ = pw.Write([]byte(stdoutData)) }()
	return m
}

// types.Session methods.
func (m *mockPipedSession) ID() string { return m.id }
func (m *mockPipedSession) Send(_ context.Context, _ string) (*types.Result, error) {
	return &types.Result{}, nil
}
func (m *mockPipedSession) Stream(_ context.Context, _ string) (<-chan types.Event, error) {
	ch := make(chan types.Event)
	close(ch)
	return ch, nil
}

// Close marks the session closed and closes the stdout pipe write-end,
// which causes the reader goroutine in runInteractiveSession to see EOF and
// exit cleanly.
func (m *mockPipedSession) Close() error {
	m.closeCalled = true
	_ = m.stdoutPW.Close()
	return nil
}
func (m *mockPipedSession) Alive() bool { return !m.closeCalled }
func (m *mockPipedSession) PID() int    { return 0 }

// types.SessionPipes methods.
func (m *mockPipedSession) Stdin() io.Writer  { return m.stdinBuf }
func (m *mockPipedSession) Stdout() io.Reader { return m.stdoutPR }

// Compile-time assertion that mockPipedSession satisfies types.InteractivePipes.
var _ types.InteractivePipes = (*mockPipedSession)(nil)

// --- TestRunInteractiveSession_PipesPassthrough -----------------------------

// TestRunInteractiveSession_PipesPassthrough verifies the core passthrough contract:
//  1. Bytes pre-loaded in stdoutBuf are forwarded to the output writer.
//  2. Lines typed by the operator are written to the session's stdin buffer.
//
// Input is delivered via io.Pipe so each write becomes its own Read call in the
// input goroutine. This guarantees "hello\n" and "/quit\n" arrive as separate
// chunks, which is required for slash-command detection (the loop checks whether
// a chunk starts with "/"). strings.NewReader would deliver both lines in a single
// Read and the "/quit" prefix would not be found.
func TestRunInteractiveSession_PipesPassthrough(t *testing.T) {
	t.Parallel()

	const tui = "gemini-tui-render\n"
	sess := newMockPipedSession("pipe-test", tui)

	outBuf := &bytes.Buffer{}

	// Use io.Pipe so we can write chunks individually to the input goroutine.
	pr, pw := io.Pipe()

	// Writer goroutine: send "hello\n" as regular input, then "/quit\n" as a
	// slash-command.  Each Write is a separate Read on the pipe reader.
	go func() {
		_, _ = pw.Write([]byte("hello\n"))
		_, _ = pw.Write([]byte("/quit\n"))
		_ = pw.Close()
	}()

	code := runInteractiveSession(
		context.Background(),
		sess,
		nopSink{},
		outBuf,
		pr,
	)

	// /quit → exit 0.
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}

	// TUI bytes from stdout must have been forwarded to outBuf.
	if !strings.Contains(outBuf.String(), "gemini-tui-render") {
		t.Errorf("stdout passthrough missing: outBuf=%q", outBuf.String())
	}

	// "hello\n" written by operator must have reached session stdin.
	if !strings.Contains(sess.stdinBuf.String(), "hello") {
		t.Errorf("operator input not forwarded to session stdin: stdinBuf=%q", sess.stdinBuf.String())
	}

	// /quit must have closed the session.
	if !sess.closeCalled {
		t.Error("/quit must call sess.Close()")
	}
}

// --- TestRunInteractiveSession_QuitCommand ----------------------------------

// TestRunInteractiveSession_QuitCommand verifies /quit returns exit 0 and
// calls sess.Close regardless of remaining stdout data.
func TestRunInteractiveSession_QuitCommand(t *testing.T) {
	t.Parallel()

	sess := newMockPipedSession("quit-test", "some-tui-output\n")
	outBuf := &bytes.Buffer{}
	inReader := strings.NewReader("/quit\n")

	code := runInteractiveSession(
		context.Background(),
		sess,
		nopSink{},
		outBuf,
		inReader,
	)

	if code != 0 {
		t.Errorf("expected 0 on /quit, got %d", code)
	}
	if !sess.closeCalled {
		t.Error("/quit must call sess.Close()")
	}
}

// --- TestRunInteractiveSession_StdinEOF -------------------------------------

// TestRunInteractiveSession_StdinEOF verifies that operator stdin EOF alone does
// NOT close the session or terminate the loop.  The session stays alive until
// /quit is delivered or the process exits.
//
// This test provides an empty stdin (immediate EOF) and then cancels the context
// to drive termination — confirming the loop exits via ctx.Done (code 130) rather
// than treating stdin EOF as a quit signal.
func TestRunInteractiveSession_StdinEOF(t *testing.T) {
	t.Parallel()

	sess := newMockPipedSession("eof-test", "")
	outBuf := &bytes.Buffer{}
	// Empty stdin — immediate EOF delivered to the input goroutine.
	inReader := strings.NewReader("")

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context to drive termination after stdin EOF is observed.
	go func() { cancel() }()

	code := runInteractiveSession(ctx, sess, nopSink{}, outBuf, inReader)

	// Context cancellation → exit 130, not stdin EOF → exit 0.
	if code != 130 {
		t.Errorf("expected exit code 130 on context cancel, got %d", code)
	}
}
