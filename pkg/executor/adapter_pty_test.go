package executor_test

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/types"
)

// TestCLIPTYAdapter_CompileCheck verifies that CLIPTYAdapter satisfies ExecutorV2
// at the type level. The package-level var in adapter_pty.go already enforces this
// at compile time; this test makes the intent explicit and visible in test output.
func TestCLIPTYAdapter_CompileCheck(t *testing.T) {
	t.Parallel()
	// pty.New() returns *pty.Executor which satisfies types.LegacyExecutor.
	var _ types.ExecutorV2 = executor.NewCLIPTYAdapter(pty.New())
}

// TestCLIPTYAdapter_SessionBound_DispatchesViaSession verifies that when a CLIPTYAdapter
// is constructed with NewCLIPTYAdapterWithSession, Send() dispatches via session.Send()
// and does NOT invoke the legacy executor's Run() method (anti-stub: removing the
// session != nil branch would call legacy.Run() on a no-op executor and sendCalls would be 0).
//
// This test also verifies that the returned Response carries the content from the Session
// result (identity pass-through via resultToResponse).
func TestCLIPTYAdapter_SessionBound_DispatchesViaSession(t *testing.T) {
	t.Parallel()

	const wantContent = "session response from pty"
	sess := &mockSession{
		sendResp:    &types.Result{Content: wantContent},
		aliveResult: true,
	}

	// pty.New() is the legacy executor — its Run() must NOT be called.
	adapter := executor.NewCLIPTYAdapterWithSession(pty.New(), sess)

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

// TestCLIPTYAdapter_Info verifies the static ExecutorInfo returned by the adapter.
func TestCLIPTYAdapter_Info(t *testing.T) {
	t.Parallel()

	// pty.New() satisfies types.LegacyExecutor — passed directly.
	adapter := executor.NewCLIPTYAdapter(pty.New())

	info := adapter.Info()

	if info.Name != "pty" {
		t.Errorf("Info().Name = %q, want %q", info.Name, "pty")
	}
	if info.Type != types.ExecutorTypeCLI {
		t.Errorf("Info().Type = %v, want ExecutorTypeCLI", info.Type)
	}
	if info.Capabilities.Streaming {
		t.Error("Info().Capabilities.Streaming = true, want false")
	}
	if !info.Capabilities.PersistentSessions {
		t.Error("Info().Capabilities.PersistentSessions = false, want true (M6 implemented)")
	}
}
