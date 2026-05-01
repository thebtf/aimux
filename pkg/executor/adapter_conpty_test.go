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
	if !info.Capabilities.Streaming {
		t.Error("Info().Capabilities.Streaming = false, want true (per-line streaming enabled)")
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

// TestCLIConPTYAdapter_SendStream_PerLineChunks verifies that SendStream delivers one
// Chunk per output line (Done=false) followed by a terminal Chunk with Done=true.
// Anti-stub: replacing OnOutput wiring would produce a single Done=true chunk,
// failing the len(received) == len(outputLines)+1 assertion.
func TestCLIConPTYAdapter_SendStream_PerLineChunks(t *testing.T) {
	lines := []string{"alpha", "beta", "gamma"}
	mock := &mockLegacyExecutor{
		outputLines: lines,
		result:      &types.Result{Content: "alpha\nbeta\ngamma\n", ExitCode: 0},
	}

	adapter := executor.NewCLIConPTYAdapter(mock)

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

	wantTotal := len(lines) + 1
	if len(received) != wantTotal {
		t.Fatalf("received %d chunks, want %d (per-line + terminal)", len(received), wantTotal)
	}

	for i, line := range lines {
		c := received[i]
		if c.Done {
			t.Errorf("chunk[%d] Done=true, want false", i)
		}
		wantContent := line + "\n"
		if c.Content != wantContent {
			t.Errorf("chunk[%d] Content=%q, want %q", i, c.Content, wantContent)
		}
	}

	last := received[len(received)-1]
	if !last.Done {
		t.Error("last chunk Done=false, want true")
	}

	if mock.runCalls != 1 {
		t.Errorf("legacy.Run called %d times, want 1", mock.runCalls)
	}
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
