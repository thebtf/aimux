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
	if info.Capabilities.Streaming {
		t.Error("Info().Capabilities.Streaming should be false for pipe executor")
	}
}
