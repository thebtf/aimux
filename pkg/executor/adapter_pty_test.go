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

// TestCLIPTYAdapter_SendStream_PerLineChunks verifies that SendStream delivers one
// Chunk per output line (Done=false) followed by a terminal Chunk with Done=true.
// Anti-stub: replacing OnOutput wiring would produce a single Done=true chunk,
// failing the len(received) == len(outputLines)+1 assertion.
func TestCLIPTYAdapter_SendStream_PerLineChunks(t *testing.T) {
	t.Parallel()

	lines := []string{"x1", "x2"}
	mock := &mockLegacyExecutor{
		outputLines: lines,
		result:      &types.Result{Content: "x1\nx2\n", ExitCode: 0},
	}

	adapter := executor.NewCLIPTYAdapter(mock)

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
		t.Fatalf("received %d chunks, want %d", len(received), wantTotal)
	}

	for i, line := range lines {
		c := received[i]
		if c.Done {
			t.Errorf("chunk[%d] Done=true, want false", i)
		}
		if c.Content != line+"\n" {
			t.Errorf("chunk[%d] Content=%q, want %q", i, c.Content, line+"\n")
		}
	}

	if !received[len(received)-1].Done {
		t.Error("last chunk Done=false, want true")
	}

	if mock.runCalls != 1 {
		t.Errorf("legacy.Run called %d times, want 1", mock.runCalls)
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
	if !info.Capabilities.Streaming {
		t.Error("Info().Capabilities.Streaming = false, want true (per-line streaming enabled)")
	}
	if !info.Capabilities.PersistentSessions {
		t.Error("Info().Capabilities.PersistentSessions = false, want true (M6 implemented)")
	}
}
