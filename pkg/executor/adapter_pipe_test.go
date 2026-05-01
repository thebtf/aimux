package executor_test

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

// mockSession is a test double implementing types.Session.
// It records Send calls and returns a pre-configured response.
// Used across all three adapter session-bound tests in this package.
type mockSession struct {
	sendCalls   int
	sendResp    *types.Result
	sendErr     error
	aliveResult bool
	closeCalls  int
}

func (m *mockSession) ID() string { return "mock-session" }

func (m *mockSession) Send(_ context.Context, _ string) (*types.Result, error) {
	m.sendCalls++
	return m.sendResp, m.sendErr
}

func (m *mockSession) Stream(_ context.Context, _ string) (<-chan types.Event, error) {
	ch := make(chan types.Event, 1)
	close(ch)
	return ch, nil
}

func (m *mockSession) Close() error {
	m.closeCalls++
	return nil
}

func (m *mockSession) Alive() bool { return m.aliveResult }
func (m *mockSession) PID() int    { return 12345 }

// mockLegacyExecutor is a test double implementing types.LegacyExecutor.
// Run() fires the OnOutput callback for each configured output line, then
// returns the configured Result.  Used to verify per-line SendStream behaviour
// without spawning a real subprocess.
type mockLegacyExecutor struct {
	outputLines []string     // lines delivered via SpawnArgs.OnOutput
	result      *types.Result
	runErr      error
	runCalls    int
}

func (m *mockLegacyExecutor) Run(_ context.Context, args types.SpawnArgs) (*types.Result, error) {
	m.runCalls++
	for _, line := range m.outputLines {
		if args.OnOutput != nil {
			args.OnOutput(line)
		}
	}
	return m.result, m.runErr
}

func (m *mockLegacyExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, nil
}

func (m *mockLegacyExecutor) Name() string      { return "mock" }
func (m *mockLegacyExecutor) Available() bool   { return true }

// TestCLIPipeAdapter_CompileCheck verifies that CLIPipeAdapter satisfies
// ExecutorV2 at compile time (redundant with the package-level var _ check,
// but acts as an explicit, searchable test assertion).
func TestCLIPipeAdapter_CompileCheck(t *testing.T) {
	t.Parallel()

	var _ types.ExecutorV2 = executor.NewCLIPipeAdapter(pipe.New())
}

// TestCLIPipeAdapter_SessionBound_DispatchesViaSession verifies that when a CLIPipeAdapter
// is constructed with NewCLIPipeAdapterWithSession, Send() dispatches via session.Send()
// and does NOT invoke the legacy executor's Run() method (anti-stub: removing the
// session != nil branch would call legacy.Run(), which fails against pipe.New() with no
// real process, and the sendCalls assertion would fail).
//
// This test also verifies that the returned Response carries the content from the Session
// result (identity pass-through via resultToResponse).
func TestCLIPipeAdapter_SessionBound_DispatchesViaSession(t *testing.T) {
	t.Parallel()

	const wantContent = "session response from pipe"
	sess := &mockSession{
		sendResp:    &types.Result{Content: wantContent},
		aliveResult: true,
	}

	// pipe.New() is the legacy executor — its Run() must NOT be called.
	adapter := executor.NewCLIPipeAdapterWithSession(pipe.New(), sess)

	resp, err := adapter.Send(context.Background(), types.Message{Content: "hello"})
	if err != nil {
		t.Fatalf("Send: unexpected error: %v", err)
	}

	// Assert session.Send was called exactly once.
	if sess.sendCalls != 1 {
		t.Errorf("session.Send call count = %d; want 1 (removing session branch would break this)", sess.sendCalls)
	}

	// Assert the response carries the session's content (identity pass-through).
	if resp.Content != wantContent {
		t.Errorf("resp.Content = %q; want %q", resp.Content, wantContent)
	}

	// Assert IsAlive delegates to session.Alive().
	if adapter.IsAlive() != types.HealthAlive {
		t.Error("IsAlive() = HealthDead; want HealthAlive when session.Alive() is true")
	}

	// Assert Close delegates to session.Close().
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if sess.closeCalls != 1 {
		t.Errorf("session.Close call count = %d; want 1", sess.closeCalls)
	}
}

// TestCLIPipeAdapter_SendStream_PerLineChunks verifies that SendStream delivers one
// Chunk per output line (Done=false) followed by a terminal Chunk with Done=true.
// Anti-stub: replacing OnOutput wiring would produce a single Done=true chunk,
// failing the len(received) == len(outputLines)+1 assertion.
func TestCLIPipeAdapter_SendStream_PerLineChunks(t *testing.T) {
	t.Parallel()

	lines := []string{"line one", "line two", "line three"}
	mock := &mockLegacyExecutor{
		outputLines: lines,
		result:      &types.Result{Content: "line one\nline two\nline three\n", ExitCode: 0},
	}

	adapter := executor.NewCLIPipeAdapter(mock)

	var received []types.Chunk
	resp, err := adapter.SendStream(context.Background(), types.Message{Content: "test"}, func(c types.Chunk) {
		received = append(received, c)
	})

	if err != nil {
		t.Fatalf("SendStream: unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Expect N per-line chunks + 1 terminal Done=true chunk.
	wantTotal := len(lines) + 1
	if len(received) != wantTotal {
		t.Fatalf("received %d chunks, want %d (per-line + terminal)", len(received), wantTotal)
	}

	// Verify per-line chunks carry correct content and Done=false.
	for i, line := range lines {
		c := received[i]
		if c.Done {
			t.Errorf("chunk[%d] Done=true, want false (intermediate chunk)", i)
		}
		wantContent := line + "\n"
		if c.Content != wantContent {
			t.Errorf("chunk[%d] Content=%q, want %q", i, c.Content, wantContent)
		}
	}

	// Last chunk must be terminal.
	last := received[len(received)-1]
	if !last.Done {
		t.Error("last chunk Done=false, want true")
	}

	// RunCalls must be exactly 1 (not zero — that would indicate a stub bypassing Run).
	if mock.runCalls != 1 {
		t.Errorf("legacy.Run called %d times, want 1", mock.runCalls)
	}
}

// TestCLIPipeAdapter_Info verifies that Info() returns the correct ExecutorInfo.
func TestCLIPipeAdapter_Info(t *testing.T) {
	t.Parallel()

	adapter := executor.NewCLIPipeAdapter(pipe.New())
	info := adapter.Info()

	if info.Name != "pipe" {
		t.Errorf("Info().Name = %q; want %q", info.Name, "pipe")
	}
	if info.Type != types.ExecutorTypeCLI {
		t.Errorf("Info().Type = %v; want ExecutorTypeCLI", info.Type)
	}
	if !info.Capabilities.PersistentSessions {
		t.Error("Info().Capabilities.PersistentSessions should be true")
	}
	if !info.Capabilities.Streaming {
		t.Error("Info().Capabilities.Streaming should be true for pipe executor (per-line streaming enabled)")
	}
}
