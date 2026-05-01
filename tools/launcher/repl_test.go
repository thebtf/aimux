// Package main — repl_test.go tests the REPL slash-command parser.
//
// Only handleSlashCommand is tested here — the full REPL loop requires a live
// stdin/Session which is not suitable for unit tests.
//
// Tests:
//   - TestSlashCommandParser_RecognizedCommands:  each slash-command returns correct code.
//   - TestSlashCommandParser_UnknownCommand:      unknown /foo returns -1.
//   - TestSlashCommandParser_RawWithoutArgs:      /raw without on/off returns -1.
//   - TestSlashCommandParser_SaveRequiresPath:    /save without path returns -1.
package main

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// --- mockSession ------------------------------------------------------------

// mockSession is a stub implementing types.Session for slash-command tests.
// No method does any real work; closeCalled tracks whether Close was called.
type mockSession struct {
	id          string
	closeCalled bool
}

func (m *mockSession) ID() string                                             { return m.id }
func (m *mockSession) Send(_ context.Context, _ string) (*types.Result, error) { return &types.Result{}, nil }
func (m *mockSession) Stream(_ context.Context, _ string) (<-chan types.Event, error) {
	ch := make(chan types.Event)
	close(ch)
	return ch, nil
}
func (m *mockSession) Close() error { m.closeCalled = true; return nil }
func (m *mockSession) Alive() bool  { return !m.closeCalled }
func (m *mockSession) PID() int     { return 0 }

// --- callCmd is a convenience wrapper for handleSlashCommand tests ----------

// callCmd calls handleSlashCommand with test defaults and returns the exit code.
func callCmd(cmd string, sess types.Session, sink EventSink, sessPtr *types.Session) int {
	return handleSlashCommand(
		context.Background(),
		cmd,
		sess,
		sink,
		"codex",                 // cliName
		nil,                     // no breaker registry
		nil,                     // no cooldown tracker
		nil,                     // no sessionFactory
		nil,                     // no rawCallback
		nil,                     // empty history
		executor.ErrorClassNone, // lastClassify
		false,                   // hasLastClassify
		sessPtr,
	)
}

// --- TestSlashCommandParser_RecognizedCommands ------------------------------

// TestSlashCommandParser_RecognizedCommands verifies that each recognized
// slash-command returns the expected exit code:
//   - /quit  → 0  (terminates REPL cleanly)
//   - /reset → -1 (no factory → warning + continue)
//   - /dump  → -1 (continue)
//   - /history → -1 (continue)
//   - /help  → -1 (continue)
func TestSlashCommandParser_RecognizedCommands(t *testing.T) {
	t.Parallel()

	cases := []struct {
		cmd       string
		wantCode  int
		wantClose bool
	}{
		{"/quit", 0, true},
		{"/reset", -1, false}, // no factory → warning + return -1, no Close
		{"/dump", -1, false},
		{"/history", -1, false},
		{"/help", -1, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.cmd, func(t *testing.T) {
			t.Parallel()

			sess := &mockSession{id: "test-session"}
			var sessVar types.Session = sess
			sink := nopSink{}

			got := callCmd(tc.cmd, sess, sink, &sessVar)
			if got != tc.wantCode {
				t.Errorf("%s returned %d, want %d", tc.cmd, got, tc.wantCode)
			}
			if tc.wantClose && !sess.closeCalled {
				t.Errorf("%s: expected sess.Close to be called", tc.cmd)
			}
			if !tc.wantClose && sess.closeCalled {
				t.Errorf("%s: unexpected sess.Close call", tc.cmd)
			}
		})
	}
}

// --- TestSlashCommandParser_UnknownCommand ----------------------------------

// TestSlashCommandParser_UnknownCommand verifies that an unrecognized command
// returns -1 (REPL continues) and does not close the session.
func TestSlashCommandParser_UnknownCommand(t *testing.T) {
	t.Parallel()

	sess := &mockSession{id: "s1"}
	var sessVar types.Session = sess
	sink := nopSink{}

	code := callCmd("/foo", sess, sink, &sessVar)
	if code != -1 {
		t.Errorf("/foo returned %d, want -1", code)
	}
	if sess.closeCalled {
		t.Error("/foo must not close the session")
	}
}

// --- TestSlashCommandParser_RawWithoutArgs ----------------------------------

// TestSlashCommandParser_RawWithoutArgs verifies that /raw without on|off
// returns -1 (usage hint path) without closing the session.
func TestSlashCommandParser_RawWithoutArgs(t *testing.T) {
	t.Parallel()

	sess := &mockSession{id: "s2"}
	var sessVar types.Session = sess
	sink := nopSink{}

	code := callCmd("/raw", sess, sink, &sessVar)
	if code != -1 {
		t.Errorf("/raw (no args) returned %d, want -1", code)
	}
	if sess.closeCalled {
		t.Error("/raw must not close the session")
	}
}

// --- TestSlashCommandParser_SaveRequiresPath --------------------------------

// TestSlashCommandParser_SaveRequiresPath verifies that /save without a path
// argument returns -1 (usage hint) and does not close the session.
func TestSlashCommandParser_SaveRequiresPath(t *testing.T) {
	t.Parallel()

	sess := &mockSession{id: "s3"}
	var sessVar types.Session = sess
	sink := nopSink{}

	code := callCmd("/save", sess, sink, &sessVar)
	if code != -1 {
		t.Errorf("/save (no path) returned %d, want -1", code)
	}
	if sess.closeCalled {
		t.Error("/save must not close the session")
	}
}
