package executor_test

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/conpty"
	"github.com/thebtf/aimux/pkg/types"
)

// TestCLIConPTYAdapter_Info verifies that Info() returns the expected metadata.
func TestCLIConPTYAdapter_Info(t *testing.T) {
	legacy := conpty.New()
	adapter := executor.NewCLIConPTYAdapter(legacy)

	info := adapter.Info()

	if info.Name != "conpty" {
		t.Errorf("Info().Name = %q, want %q", info.Name, "conpty")
	}
	if info.Type != types.ExecutorTypeCLI {
		t.Errorf("Info().Type = %v, want ExecutorTypeCLI", info.Type)
	}
	if !info.Capabilities.PersistentSessions {
		t.Error("Info().Capabilities.PersistentSessions = false, want true (M6 implemented)")
	}
	if info.Capabilities.Streaming {
		t.Error("Info().Capabilities.Streaming = true, want false")
	}
}

// TestCLIConPTYAdapter_CompileCheck verifies that CLIConPTYAdapter implements ExecutorV2.
// The compile-time assertion in adapter_conpty.go already enforces this, but this
// test makes the requirement explicit and visible in test output.
func TestCLIConPTYAdapter_CompileCheck(t *testing.T) {
	legacy := conpty.New()
	adapter := executor.NewCLIConPTYAdapter(legacy)

	// Interface assignment — if this compiles, the contract is satisfied.
	var _ types.ExecutorV2 = adapter
}

// TestCLIConPTYAdapter_SessionBound_DispatchesViaSession verifies that when a CLIConPTYAdapter
// is constructed with NewCLIConPTYAdapterWithSession, Send() dispatches via session.Send()
// and does NOT invoke the legacy executor's Run() method (anti-stub: removing the
// session != nil branch would call legacy.Run() on a no-op executor and sendCalls would be 0).
//
// This test also verifies that the returned Response carries the content from the Session
// result (identity pass-through via resultToResponse).
func TestCLIConPTYAdapter_SessionBound_DispatchesViaSession(t *testing.T) {
	const wantContent = "session response from conpty"
	sess := &mockSession{
		sendResp:    &types.Result{Content: wantContent},
		aliveResult: true,
	}

	// conpty.New() is the legacy executor — its Run() must NOT be called.
	adapter := executor.NewCLIConPTYAdapterWithSession(conpty.New(), sess)

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
