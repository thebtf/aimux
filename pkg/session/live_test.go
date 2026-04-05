package session_test

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

type mockProcess struct {
	alive bool
	pid   int
}

func (m *mockProcess) ID() string { return "mock-session" }
func (m *mockProcess) Send(ctx context.Context, prompt string) (*types.Result, error) {
	return &types.Result{Content: "response to: " + prompt}, nil
}
func (m *mockProcess) Stream(ctx context.Context, prompt string) (<-chan types.Event, error) {
	ch := make(chan types.Event, 2)
	ch <- types.Event{Type: types.EventTypeContent, Content: "streamed"}
	ch <- types.Event{Type: types.EventTypeComplete}
	close(ch)
	return ch, nil
}
func (m *mockProcess) Close() error { m.alive = false; return nil }
func (m *mockProcess) Alive() bool  { return m.alive }
func (m *mockProcess) PID() int     { return m.pid }

func TestLiveSession_Send(t *testing.T) {
	reg := session.NewRegistry()
	sess := reg.Create("codex", types.SessionModeLive, "/tmp")

	proc := &mockProcess{alive: true, pid: 12345}
	live := session.NewLiveSession(sess, proc, context.Background())

	result, err := live.Send(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Content != "response to: hello" {
		t.Errorf("Content = %q", result.Content)
	}

	if live.Session().Turns != 1 {
		t.Errorf("Turns = %d, want 1", live.Session().Turns)
	}
}

func TestLiveSession_Close(t *testing.T) {
	reg := session.NewRegistry()
	sess := reg.Create("codex", types.SessionModeLive, "/tmp")

	proc := &mockProcess{alive: true, pid: 100}
	live := session.NewLiveSession(sess, proc, context.Background())

	if !live.Alive() {
		t.Error("should be alive before close")
	}

	live.Close()

	if live.Alive() {
		t.Error("should not be alive after close")
	}
}
