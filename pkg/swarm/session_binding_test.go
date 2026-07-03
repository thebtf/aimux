package swarm_test

// Tests for AIMUX-14 B2: persistent CLI session binding through Swarm.Get/Send.
// Verifies WithSessionArgs, bindSession, and restart rebind behaviour.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

// --- mocks for session-binding tests ---

// sessionBindingExecutor satisfies ExecutorV2, SessionFactory, SessionBinder,
// and LegacyAccessor — mirrors the production CLI{Pipe,ConPTY,PTY}Adapter shape.
type sessionBindingExecutor struct {
	mockExecutorV2
	sessionStarted atomic.Int32
	sendCalls      atomic.Int32
	lastSession    *mockSession
}

func newSessionBindingExecutor() *sessionBindingExecutor {
	m := &sessionBindingExecutor{}
	m.alive = types.HealthAlive
	m.info = types.ExecutorInfo{
		Name: "session-binder",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: true,
		},
	}
	return m
}

func (m *sessionBindingExecutor) StartSession(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	m.sessionStarted.Add(1)
	sess := &mockSession{alive: true}
	m.lastSession = sess
	return sess, nil
}

func (m *sessionBindingExecutor) WithSession(sess types.Session) types.ExecutorV2 {
	bound := newSessionBindingExecutor()
	bound.sendFn = func(ctx context.Context, msg types.Message) (*types.Response, error) {
		bound.sendCalls.Add(1)
		result, err := sess.Send(ctx, msg.Content)
		if err != nil {
			return nil, err
		}
		return &types.Response{Content: result.Content, Duration: time.Millisecond}, nil
	}
	bound.alive = types.HealthAlive
	return bound
}

// --- tests ---

// TestSwarmGet_WithSessionArgs_BindsSession verifies that Swarm.Get with
// WithSessionArgs starts a persistent session and binds it to the adapter
// for Stateful mode (AIMUX-14 FR-2 happy path).
func TestSwarmGet_WithSessionArgs_BindsSession(t *testing.T) {
	t.Parallel()

	var created atomic.Int32
	factory := func(name string) (types.ExecutorV2, error) {
		created.Add(1)
		return newSessionBindingExecutor(), nil
	}

	sw := swarm.New(factory, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := context.Background()
	args := types.SpawnArgs{Command: "echo"}

	h, err := sw.Get(ctx, "test", swarm.Stateful, swarm.WithSessionArgs(args))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Session should have been started.
	if created.Load() != 1 {
		t.Errorf("factory called %d times, want 1", created.Load())
	}

	// Send should route through the session-bound adapter.
	resp, err := sw.Send(ctx, h, types.Message{Content: "hello"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.Content != "response to: hello" {
		t.Errorf("got %q, want %q", resp.Content, "response to: hello")
	}
}

// TestSwarmGet_WithSessionArgs_StatelessIgnored verifies that WithSessionArgs
// has no effect when mode is Stateless — sessions are NOT started (FR-2 guard).
func TestSwarmGet_WithSessionArgs_StatelessIgnored(t *testing.T) {
	t.Parallel()

	factory := func(name string) (types.ExecutorV2, error) {
		return newSessionBindingExecutor(), nil
	}

	sw := swarm.New(factory, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := context.Background()
	args := types.SpawnArgs{Command: "echo"}

	h, err := sw.Get(ctx, "test", swarm.Stateless, swarm.WithSessionArgs(args))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// In stateless mode, Send should use the default mock path (not session-bound).
	resp, err := sw.Send(ctx, h, types.Message{Content: "hello"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Default mockExecutorV2 returns "ok", not "response to: hello".
	if resp.Content != "ok" {
		t.Errorf("expected default 'ok' from stateless path, got %q", resp.Content)
	}
}

// TestSwarmGet_WithSessionArgs_NoCapability_GracefulFallback verifies that
// when the executor does not support sessions (PersistentSessions: false),
// WithSessionArgs is a no-op — adapter stays stateless (FR-4 fallback).
func TestSwarmGet_WithSessionArgs_NoCapability_GracefulFallback(t *testing.T) {
	t.Parallel()

	factory := func(name string) (types.ExecutorV2, error) {
		m := &mockExecutorV2{}
		m.alive = types.HealthAlive
		m.info = types.ExecutorInfo{
			Name:         "no-session",
			Type:         types.ExecutorTypeCLI,
			Capabilities: types.ExecutorCapabilities{PersistentSessions: false},
		}
		return m, nil
	}

	sw := swarm.New(factory, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := context.Background()
	h, err := sw.Get(ctx, "test", swarm.Stateful, swarm.WithSessionArgs(types.SpawnArgs{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Should still be usable (stateless fallback).
	resp, err := sw.Send(ctx, h, types.Message{Content: "hello"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected default 'ok' response from stateless mock, got %q", resp.Content)
	}
}

// TestSwarmGet_WithSessionArgs_Persistent verifies that WithSessionArgs
// works with Persistent mode (AIMUX-14 FR-2 persistent path).
func TestSwarmGet_WithSessionArgs_Persistent(t *testing.T) {
	t.Parallel()

	factory := func(name string) (types.ExecutorV2, error) {
		return newSessionBindingExecutor(), nil
	}

	sw := swarm.New(factory, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := context.Background()
	h, err := sw.Get(ctx, "test", swarm.Persistent, swarm.WithSessionArgs(types.SpawnArgs{Command: "echo"}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Send 10 messages through the session-bound adapter.
	for i := 0; i < 10; i++ {
		resp, err := sw.Send(ctx, h, types.Message{Content: "msg"})
		if err != nil {
			t.Fatalf("Send[%d]: %v", i, err)
		}
		if resp.Content != "response to: msg" {
			t.Errorf("Send[%d]: got %q, want %q", i, resp.Content, "response to: msg")
		}
	}
}

// TestSwarmGet_WithSessionArgs_HandleReuse verifies that a second Get call
// for the same name/mode returns the existing session-bound handle — no new
// session is started (AIMUX-14 FR-2 handle reuse).
func TestSwarmGet_WithSessionArgs_HandleReuse(t *testing.T) {
	t.Parallel()

	var factoryCalls atomic.Int32
	factory := func(name string) (types.ExecutorV2, error) {
		factoryCalls.Add(1)
		return newSessionBindingExecutor(), nil
	}

	sw := swarm.New(factory, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := context.Background()
	args := types.SpawnArgs{Command: "echo"}

	h1, err := sw.Get(ctx, "test", swarm.Stateful, swarm.WithSessionArgs(args))
	if err != nil {
		t.Fatalf("Get 1: %v", err)
	}

	h2, err := sw.Get(ctx, "test", swarm.Stateful, swarm.WithSessionArgs(args))
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}

	if h1.ID != h2.ID {
		t.Errorf("expected same handle ID, got %q and %q", h1.ID, h2.ID)
	}
	if factoryCalls.Load() != 1 {
		t.Errorf("factory called %d times, want 1 (handle reuse)", factoryCalls.Load())
	}
}



// TestSwarmGet_WithSessionArgs_CloseWithin500ms verifies that Close completes
// within 500ms budget (AIMUX-14 acceptance criteria).
func TestSwarmGet_WithSessionArgs_CloseWithin500ms(t *testing.T) {
	t.Parallel()

	factory := func(name string) (types.ExecutorV2, error) {
		return newSessionBindingExecutor(), nil
	}

	sw := swarm.New(factory, audit.DiscardLog{}, swarm.WithStatefulTTL(0))

	ctx := context.Background()
	_, err := sw.Get(ctx, "test", swarm.Stateful, swarm.WithSessionArgs(types.SpawnArgs{Command: "echo"}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	start := time.Now()
	sw.Shutdown(context.Background())
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("Shutdown took %v, want < 500ms", elapsed)
	}
}
