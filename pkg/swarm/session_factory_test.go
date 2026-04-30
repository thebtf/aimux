package swarm_test

// Tests for T007+T008+T009: MaybeStartSession capability-detect helper and
// ErrNotSupported sentinel (AIMUX-14 Wave 3, FR-4 / FR-1 C3).

import (
	"context"
	"errors"
	"testing"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

// --- mock helpers ---

// capableExecutor is an ExecutorV2 whose Info() declares PersistentSessions: true.
// Used to test the happy path of MaybeStartSession.
type capableExecutor struct {
	mockExecutorV2
	startSessionFn func(ctx context.Context, args types.SpawnArgs) (types.Session, error)
}

// StartSession implements types.SessionFactory.
func (m *capableExecutor) StartSession(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	if m.startSessionFn != nil {
		return m.startSessionFn(ctx, args)
	}
	return &mockSession{alive: true}, nil
}

func (m *capableExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "capable",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: true,
		},
	}
}

// incapableExecutor is an ExecutorV2 whose Info() declares PersistentSessions: true
// but does NOT implement SessionFactory. Used to verify graceful nil-nil return.
type incapableExecutor struct {
	mockExecutorV2
}

func (m *incapableExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "incapable",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: true, // declared but implementation missing
		},
	}
}

// noCapabilityExecutor is an ExecutorV2 with PersistentSessions: false.
type noCapabilityExecutor struct {
	mockExecutorV2
}

func (m *noCapabilityExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "no-capability",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: false,
		},
	}
}

// mockSession is a minimal types.Session for use in tests.
type mockSession struct {
	alive  bool
	closed bool
}

func (s *mockSession) ID() string { return "mock-session" }

func (s *mockSession) Send(ctx context.Context, prompt string) (*types.Result, error) {
	return &types.Result{Content: "response to: " + prompt}, nil
}

func (s *mockSession) Stream(ctx context.Context, prompt string) (<-chan types.Event, error) {
	ch := make(chan types.Event, 1)
	ch <- types.Event{Type: types.EventTypeComplete}
	close(ch)
	return ch, nil
}

func (s *mockSession) Close() error {
	s.closed = true
	return nil
}

func (s *mockSession) Alive() bool { return s.alive }

func (s *mockSession) PID() int { return 42 }

// --- tests ---

// TestMaybeStartSession_NoCapability verifies that an ExecutorV2 whose
// Info().Capabilities.PersistentSessions is false causes MaybeStartSession
// to return (nil, nil) without invoking StartSession (FR-4, graceful fallback).
func TestMaybeStartSession_NoCapability(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ex := &noCapabilityExecutor{}
	ex.alive = types.HealthAlive

	sess, err := swarm.MaybeStartSession(ctx, ex, types.SpawnArgs{})

	if err != nil {
		t.Errorf("expected nil error for no-capability executor, got: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil session for no-capability executor, got non-nil")
	}
}

// TestMaybeStartSession_CapableButNotSessionFactory verifies that an ExecutorV2
// with PersistentSessions: true that does NOT implement SessionFactory causes
// MaybeStartSession to return (nil, nil) — graceful fallback (FR-4).
func TestMaybeStartSession_CapableButNotSessionFactory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ex := &incapableExecutor{}
	ex.alive = types.HealthAlive

	sess, err := swarm.MaybeStartSession(ctx, ex, types.SpawnArgs{})

	if err != nil {
		t.Errorf("expected nil error when SessionFactory not implemented, got: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil session when SessionFactory not implemented, got non-nil")
	}
}

// TestMaybeStartSession_FullPath verifies that an ExecutorV2 with
// PersistentSessions: true AND implementing SessionFactory causes
// MaybeStartSession to invoke StartSession and return the session (FR-4 happy path).
func TestMaybeStartSession_FullPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	wantSession := &mockSession{alive: true}
	ex := &capableExecutor{}
	ex.alive = types.HealthAlive
	ex.startSessionFn = func(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
		return wantSession, nil
	}

	sess, err := swarm.MaybeStartSession(ctx, ex, types.SpawnArgs{})

	if err != nil {
		t.Fatalf("expected nil error on full path, got: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session on full path, got nil")
	}
	if sess != wantSession {
		t.Errorf("expected the exact session returned by StartSession")
	}
}

// TestMaybeStartSession_ErrNotSupported verifies that when StartSession returns
// ErrNotSupported (FR-1 C3 defensive guard), MaybeStartSession propagates the
// error to the caller — it is NOT silently swallowed (anti-misuse contract).
func TestMaybeStartSession_ErrNotSupported(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ex := &capableExecutor{}
	ex.alive = types.HealthAlive
	ex.startSessionFn = func(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
		return nil, swarm.ErrNotSupported
	}

	sess, err := swarm.MaybeStartSession(ctx, ex, types.SpawnArgs{})

	if sess != nil {
		t.Errorf("expected nil session when ErrNotSupported, got non-nil")
	}
	if !errors.Is(err, swarm.ErrNotSupported) {
		t.Errorf("expected errors.Is(err, swarm.ErrNotSupported)=true, got err=%v", err)
	}
}

// TestErrNotSupported_Identity verifies errors.Is identity and distinctness (T009 AC).
// ErrNotSupported must be a stable sentinel — same value compared to itself, distinct
// from io.EOF and generic errors.
func TestErrNotSupported_Identity(t *testing.T) {
	t.Parallel()

	if !errors.Is(swarm.ErrNotSupported, swarm.ErrNotSupported) {
		t.Error("errors.Is(ErrNotSupported, ErrNotSupported) must be true")
	}
	if errors.Is(swarm.ErrNotSupported, errors.New("other")) {
		t.Error("ErrNotSupported must not match arbitrary errors")
	}
	// Wrapping should work via errors.Is unwrap chain.
	wrapped := errors.Join(swarm.ErrNotSupported, errors.New("extra context"))
	if !errors.Is(wrapped, swarm.ErrNotSupported) {
		t.Error("errors.Is must unwrap joined ErrNotSupported")
	}
}
