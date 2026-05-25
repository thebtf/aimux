package codex

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/runtime"
)

// TestAppServerProcess_CleanupAfterStartFailure_CancelsAndClosesClient
// verifies the engram #241 fix: cleanupAfterStartFailure must invoke
// cancelReadLoop AND close the JSONLClient so the read goroutine exits.
//
// Without explicit cancel + client.Close, the read loop relies on stdout EOF
// after subprocess death (kill). That is racy in production (kill can be slow
// or fail) and indistinguishable from a leak in tests where no real
// subprocess is spawned. The fix mirrors Shutdown's full cleanup chain in
// the Start-failure paths.
func TestAppServerProcess_CleanupAfterStartFailure_CancelsAndClosesClient(t *testing.T) {
	dialer := newFakeDialer(t)
	clientWrite, clientRead := dialer.dial(t)

	p := &AppServerProcess{
		codexPath: "/fake/codex",
		profile:   runtime.CLIRuntimeProfile{WorkDir: t.TempDir()},
		state:     AppServerStateInitializing, // simulate post-spawn, pre-Ready
	}

	client := NewJSONLClient(clientWrite, clientRead)
	readCtx, realCancel := context.WithCancel(context.Background())
	var cancelCount int32
	cancelReadLoop := func() {
		atomic.AddInt32(&cancelCount, 1)
		realCancel()
	}
	go client.Start(readCtx)

	p.mu.Lock()
	p.client = client
	p.cancelReadLoop = cancelReadLoop
	p.mu.Unlock()

	t.Cleanup(func() {
		realCancel()
		clientWrite.Close()
	})

	// Invoke the helper directly.
	p.cleanupAfterStartFailure()

	if got := atomic.LoadInt32(&cancelCount); got != 1 {
		t.Errorf("cancelReadLoop must be invoked exactly once, got %d calls", got)
	}

	// Verify the JSONLClient.done channel closed — proves client.Close() ran.
	select {
	case <-client.done:
		// Closed as expected.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("JSONLClient.done channel did not close within 500ms; client.Close() was not invoked by cleanupAfterStartFailure")
	}
}

// TestAppServerProcess_CleanupAfterStartFailure_IsSafeOnPartialState verifies
// the helper does not panic when called before spawn() has stored a client
// or cancelReadLoop on the receiver. Spawn-failure path may invoke cleanup
// with these fields still nil.
func TestAppServerProcess_CleanupAfterStartFailure_IsSafeOnPartialState(t *testing.T) {
	p := &AppServerProcess{
		codexPath: "/fake/codex",
		profile:   runtime.CLIRuntimeProfile{WorkDir: t.TempDir()},
		state:     AppServerStateInitializing,
	}
	// No client, no cancelReadLoop, no cmd — full nil state.
	// Must not panic.
	p.cleanupAfterStartFailure()
}

// TestAppServerProcess_CleanupAfterStartFailure_IsIdempotent verifies the
// helper can be called more than once without panicking. JSONLClient.Close
// is guarded by closeOnce, so the second close is a no-op.
func TestAppServerProcess_CleanupAfterStartFailure_IsIdempotent(t *testing.T) {
	dialer := newFakeDialer(t)
	clientWrite, clientRead := dialer.dial(t)

	p := &AppServerProcess{
		codexPath: "/fake/codex",
		profile:   runtime.CLIRuntimeProfile{WorkDir: t.TempDir()},
		state:     AppServerStateInitializing,
	}
	client := NewJSONLClient(clientWrite, clientRead)
	readCtx, realCancel := context.WithCancel(context.Background())
	var cancelCount int32
	cancelReadLoop := func() {
		atomic.AddInt32(&cancelCount, 1)
		realCancel()
	}
	go client.Start(readCtx)

	p.mu.Lock()
	p.client = client
	p.cancelReadLoop = cancelReadLoop
	p.mu.Unlock()

	t.Cleanup(func() {
		realCancel()
		clientWrite.Close()
	})

	p.cleanupAfterStartFailure()
	p.cleanupAfterStartFailure() // must not panic

	if got := atomic.LoadInt32(&cancelCount); got < 1 {
		t.Errorf("cancelReadLoop must be invoked at least once across idempotent calls, got %d", got)
	}
}
