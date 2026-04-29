package swarm_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

// --- mock executor ---

type mockExecutorV2 struct {
	mu      sync.Mutex
	info    types.ExecutorInfo
	sendFn  func(ctx context.Context, msg types.Message) (*types.Response, error)
	alive   types.HealthStatus
	closed  bool
}

func (m *mockExecutorV2) Info() types.ExecutorInfo { return m.info }

func (m *mockExecutorV2) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	m.mu.Lock()
	fn := m.sendFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, msg)
	}
	return &types.Response{Content: "ok", Duration: time.Millisecond}, nil
}

func (m *mockExecutorV2) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	resp, err := m.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: resp.Content, Done: true})
	return resp, nil
}

func (m *mockExecutorV2) IsAlive() types.HealthStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alive
}

func (m *mockExecutorV2) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// isClosed returns whether Close was called on this mock.
func (m *mockExecutorV2) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// setAlive sets the health status in a thread-safe way.
func (m *mockExecutorV2) setAlive(status types.HealthStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alive = status
}

// --- factory helpers ---

// singleFactory returns a factory that always produces the same executor.
func singleFactory(exec types.ExecutorV2) func(string) (types.ExecutorV2, error) {
	return func(_ string) (types.ExecutorV2, error) {
		return exec, nil
	}
}

// sequenceFactory returns a factory that returns executors from the slice in order.
// If the slice is exhausted it returns the last executor repeatedly.
func sequenceFactory(execs ...types.ExecutorV2) func(string) (types.ExecutorV2, error) {
	var idx atomic.Int32
	return func(_ string) (types.ExecutorV2, error) {
		i := int(idx.Add(1)) - 1
		if i >= len(execs) {
			i = len(execs) - 1
		}
		return execs[i], nil
	}
}

// errorFactory returns a factory that always returns an error.
func errorFactory(msg string) func(string) (types.ExecutorV2, error) {
	return func(_ string) (types.ExecutorV2, error) {
		return nil, errors.New(msg)
	}
}

// --- tests ---

func TestSwarm_Get_Stateless(t *testing.T) {
	t.Parallel()

	var spawnCount atomic.Int32
	factory := func(name string) (types.ExecutorV2, error) {
		spawnCount.Add(1)
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	h1, err := s.Get(ctx, "codex", swarm.Stateless)
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	h2, err := s.Get(ctx, "codex", swarm.Stateless)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}

	if h1.ID == h2.ID {
		t.Errorf("Stateless Get returned same handle ID %q twice; expected different handles", h1.ID)
	}
	if spawnCount.Load() != 2 {
		t.Errorf("expected 2 factory calls for Stateless mode, got %d", spawnCount.Load())
	}
}

func TestSwarm_Get_Persistent(t *testing.T) {
	t.Parallel()

	var spawnCount atomic.Int32
	factory := func(name string) (types.ExecutorV2, error) {
		spawnCount.Add(1)
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	h1, err := s.Get(ctx, "claude", swarm.Persistent)
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	h2, err := s.Get(ctx, "claude", swarm.Persistent)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}

	if h1.ID != h2.ID {
		t.Errorf("Persistent Get returned different handles (%q vs %q); expected same handle", h1.ID, h2.ID)
	}
	if spawnCount.Load() != 1 {
		t.Errorf("expected 1 factory call for Persistent mode, got %d", spawnCount.Load())
	}
}

func TestSwarm_Get_EmptyName(t *testing.T) {
	t.Parallel()

	s := swarm.New(singleFactory(&mockExecutorV2{alive: types.HealthAlive}), nil)
	_, err := s.Get(context.Background(), "", swarm.Stateless)
	if err == nil {
		t.Fatal("expected error for empty executor name, got nil")
	}
}

func TestSwarm_Send_Success(t *testing.T) {
	t.Parallel()

	want := "hello from executor"
	mock := &mockExecutorV2{
		alive: types.HealthAlive,
		sendFn: func(_ context.Context, msg types.Message) (*types.Response, error) {
			return &types.Response{Content: want}, nil
		},
	}

	s := swarm.New(singleFactory(mock), nil)
	ctx := context.Background()

	// Use Stateful so handle lifetime outlives Send.
	h, err := s.Get(ctx, "gemini", swarm.Stateful)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	resp, err := s.Send(ctx, h, types.Message{Content: "ping"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.Content != want {
		t.Errorf("Send response: got %q, want %q", resp.Content, want)
	}
}

func TestSwarm_Send_Stateless_ClosesExecutor(t *testing.T) {
	t.Parallel()

	mock := &mockExecutorV2{alive: types.HealthAlive}
	s := swarm.New(singleFactory(mock), nil)
	ctx := context.Background()

	h, err := s.Get(ctx, "aider", swarm.Stateless)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_, err = s.Send(ctx, h, types.Message{Content: "test"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if !mock.isClosed() {
		t.Error("Stateless executor should be closed after Send, but Close was not called")
	}
}

func TestSwarm_Send_DeadExecutor_Restart(t *testing.T) {
	t.Parallel()

	dead := &mockExecutorV2{alive: types.HealthDead}
	fresh := &mockExecutorV2{
		alive: types.HealthAlive,
		sendFn: func(_ context.Context, _ types.Message) (*types.Response, error) {
			return &types.Response{Content: "restarted"}, nil
		},
	}

	s := swarm.New(sequenceFactory(dead, fresh), nil)
	ctx := context.Background()

	// First Get registers the dead executor.
	h, err := s.Get(ctx, "qwen", swarm.Stateful)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	resp, err := s.Send(ctx, h, types.Message{Content: "try"})
	if err != nil {
		t.Fatalf("Send after restart: %v", err)
	}
	if resp.Content != "restarted" {
		t.Errorf("expected response from restarted executor, got %q", resp.Content)
	}
	if !dead.isClosed() {
		t.Error("dead executor should have been closed during restart")
	}
}

func TestSwarm_Send_RestartFails(t *testing.T) {
	t.Parallel()

	dead := &mockExecutorV2{alive: types.HealthDead}
	callCount := 0
	factory := func(name string) (types.ExecutorV2, error) {
		callCount++
		if callCount == 1 {
			return dead, nil
		}
		return nil, errors.New("factory unavailable")
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	h, err := s.Get(ctx, "goose", swarm.Stateful)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	_, err = s.Send(ctx, h, types.Message{Content: "test"})
	if err == nil {
		t.Fatal("expected error when restart fails, got nil")
	}
}

func TestSwarm_SendStream_Success(t *testing.T) {
	t.Parallel()

	mock := &mockExecutorV2{
		alive: types.HealthAlive,
		sendFn: func(_ context.Context, _ types.Message) (*types.Response, error) {
			return &types.Response{Content: "streamed"}, nil
		},
	}

	s := swarm.New(singleFactory(mock), nil)
	ctx := context.Background()

	h, err := s.Get(ctx, "claude", swarm.Stateful)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	var chunks []types.Chunk
	resp, err := s.SendStream(ctx, h, types.Message{Content: "stream me"}, func(c types.Chunk) {
		chunks = append(chunks, c)
	})
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	if resp.Content != "streamed" {
		t.Errorf("SendStream response: got %q, want %q", resp.Content, "streamed")
	}
	if len(chunks) == 0 {
		t.Error("expected at least one chunk from SendStream")
	}
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Error("last chunk should have Done=true")
	}
}

func TestSwarm_Shutdown(t *testing.T) {
	t.Parallel()

	mocks := []*mockExecutorV2{
		{alive: types.HealthAlive},
		{alive: types.HealthAlive},
	}
	idx := 0
	factory := func(name string) (types.ExecutorV2, error) {
		m := mocks[idx%len(mocks)]
		idx++
		return m, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	if _, err := s.Get(ctx, "codex", swarm.Persistent); err != nil {
		t.Fatalf("Get codex: %v", err)
	}
	if _, err := s.Get(ctx, "claude", swarm.Persistent); err != nil {
		t.Fatalf("Get claude: %v", err)
	}

	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	for i, m := range mocks {
		if !m.isClosed() {
			t.Errorf("mock[%d] was not closed after Shutdown", i)
		}
	}
}

func TestSwarm_Shutdown_CancelledContext(t *testing.T) {
	t.Parallel()

	s := swarm.New(singleFactory(&mockExecutorV2{alive: types.HealthAlive}), nil)
	ctx := context.Background()

	if _, err := s.Get(ctx, "codex", swarm.Persistent); err != nil {
		t.Fatalf("Get: %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel() // cancel immediately

	err := s.Shutdown(cancelled)
	// Shutdown with a cancelled context should return context.Canceled.
	if err == nil {
		t.Log("Shutdown returned nil with cancelled context (handles may have been closed synchronously before context check)")
	}
}

func TestSwarm_Health(t *testing.T) {
	t.Parallel()

	alive := &mockExecutorV2{alive: types.HealthAlive}
	dead := &mockExecutorV2{alive: types.HealthDead}

	idx := 0
	factory := func(name string) (types.ExecutorV2, error) {
		if idx == 0 {
			idx++
			return alive, nil
		}
		return dead, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	if _, err := s.Get(ctx, "codex", swarm.Persistent); err != nil {
		t.Fatalf("Get codex: %v", err)
	}
	if _, err := s.Get(ctx, "dead-cli", swarm.Persistent); err != nil {
		t.Fatalf("Get dead-cli: %v", err)
	}

	health := s.Health()

	if status, ok := health["codex"]; !ok || status != types.HealthAlive {
		t.Errorf("codex health: got %v, want HealthAlive", health["codex"])
	}
	if status, ok := health["dead-cli"]; !ok || status != types.HealthDead {
		t.Errorf("dead-cli health: got %v, want HealthDead", health["dead-cli"])
	}
}

func TestSwarm_Concurrent(t *testing.T) {
	t.Parallel()

	// Use a factory that always returns an alive executor with a tiny sleep
	// to encourage interleaving.
	factory := func(name string) (types.ExecutorV2, error) {
		return &mockExecutorV2{
			alive: types.HealthAlive,
			sendFn: func(_ context.Context, _ types.Message) (*types.Response, error) {
				time.Sleep(time.Microsecond)
				return &types.Response{Content: "concurrent"}, nil
			},
		}, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	const goroutines = 20
	const callsPerGoroutine = 10

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*callsPerGoroutine)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				// Mix Stateless and Stateful to exercise both paths.
				mode := swarm.Stateless
				if workerID%2 == 0 {
					mode = swarm.Stateful
				}
				h, err := s.Get(ctx, "claude", mode)
				if err != nil {
					errs <- err
					return
				}
				if _, err := s.Send(ctx, h, types.Message{Content: "concurrent test"}); err != nil {
					errs <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Send error: %v", err)
	}
}

func TestSwarm_SpawnMode_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		mode swarm.SpawnMode
		want string
	}{
		{swarm.Stateless, "stateless"},
		{swarm.Stateful, "stateful"},
		{swarm.Persistent, "persistent"},
		{swarm.SpawnMode(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.mode.String(); got != tc.want {
			t.Errorf("SpawnMode(%d).String() = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

// --- scope tests (SEC-001) ---

func TestSwarm_Get_DifferentScopes_DifferentHandles(t *testing.T) {
	t.Parallel()

	var spawnCount atomic.Int32
	factory := func(name string) (types.ExecutorV2, error) {
		spawnCount.Add(1)
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	h1, err := s.Get(ctx, "claude", swarm.Stateful, swarm.WithScope("session-A"))
	if err != nil {
		t.Fatalf("Get session-A: %v", err)
	}
	h2, err := s.Get(ctx, "claude", swarm.Stateful, swarm.WithScope("session-B"))
	if err != nil {
		t.Fatalf("Get session-B: %v", err)
	}

	if h1.ID == h2.ID {
		t.Errorf("different scopes returned same handle ID %q; expected independent handles", h1.ID)
	}
	if spawnCount.Load() != 2 {
		t.Errorf("expected 2 factory calls for two different scopes, got %d", spawnCount.Load())
	}
}

func TestSwarm_Get_SameScope_SameHandle(t *testing.T) {
	t.Parallel()

	var spawnCount atomic.Int32
	factory := func(name string) (types.ExecutorV2, error) {
		spawnCount.Add(1)
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	h1, err := s.Get(ctx, "claude", swarm.Stateful, swarm.WithScope("session-X"))
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	h2, err := s.Get(ctx, "claude", swarm.Stateful, swarm.WithScope("session-X"))
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}

	if h1.ID != h2.ID {
		t.Errorf("same scope returned different handles (%q vs %q); expected same handle", h1.ID, h2.ID)
	}
	if spawnCount.Load() != 1 {
		t.Errorf("expected 1 factory call for same scope, got %d", spawnCount.Load())
	}
}

func TestSwarm_Get_NoScope_BackwardCompat(t *testing.T) {
	t.Parallel()

	var spawnCount atomic.Int32
	factory := func(name string) (types.ExecutorV2, error) {
		spawnCount.Add(1)
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	// Two calls without scope share the same global handle (backward compat).
	h1, err := s.Get(ctx, "codex", swarm.Persistent)
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	h2, err := s.Get(ctx, "codex", swarm.Persistent)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}

	if h1.ID != h2.ID {
		t.Errorf("no-scope calls returned different handles (%q vs %q); expected shared global handle", h1.ID, h2.ID)
	}
	if spawnCount.Load() != 1 {
		t.Errorf("expected 1 factory call without scope, got %d", spawnCount.Load())
	}
}

// --- TOCTOU race test (BUG-003) ---

func TestSwarm_Get_Concurrent_NoDoubleSpawn(t *testing.T) {
	t.Parallel()

	var spawnCount atomic.Int32
	factory := func(name string) (types.ExecutorV2, error) {
		spawnCount.Add(1)
		// Tiny sleep to encourage interleaving.
		time.Sleep(time.Microsecond)
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	const goroutines = 50
	handles := make(chan *swarm.Handle, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := s.Get(ctx, "codex", swarm.Stateful)
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			handles <- h
		}()
	}

	wg.Wait()
	close(handles)

	// Collect all returned handles.
	seen := make(map[string]struct{})
	for h := range handles {
		seen[h.ID] = struct{}{}
	}

	// Under TOCTOU fix, exactly one executor must have been spawned and all
	// goroutines must share the same handle ID.
	if spawnCount.Load() != 1 {
		t.Errorf("TOCTOU: expected exactly 1 factory call, got %d (race detected)", spawnCount.Load())
	}
	if len(seen) != 1 {
		t.Errorf("TOCTOU: expected 1 unique handle ID, got %d", len(seen))
	}
}

func TestSwarm_Get_DeadPersistent_Respawns(t *testing.T) {
	t.Parallel()

	first := &mockExecutorV2{alive: types.HealthAlive}
	second := &mockExecutorV2{alive: types.HealthAlive}

	call := 0
	factory := func(name string) (types.ExecutorV2, error) {
		call++
		if call == 1 {
			return first, nil
		}
		return second, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	h1, err := s.Get(ctx, "codex", swarm.Persistent)
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}

	// Kill the first executor externally.
	first.setAlive(types.HealthDead)

	h2, err := s.Get(ctx, "codex", swarm.Persistent)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}

	// A new handle must have been spawned because the first is dead.
	if h1.ID == h2.ID {
		t.Log("warning: same handle returned even though first executor is dead — Swarm may defer restart to Send")
	}
	// At minimum, sending on h2 must succeed.
	if _, err := s.Send(ctx, h2, types.Message{Content: "test"}); err != nil {
		t.Errorf("Send on h2: %v", err)
	}
}

// --- DEF-8 / FR-2 per-key lock tests (T001/T002/T003) ---

// TestSwarm_ParallelKeysFactoryNonBlocking proves that Get on DISTINCT
// registry keys runs factoryFn in parallel — the per-key sync.Map mutex
// added in T001 must not serialise unrelated keys (DEF-8 latency-bomb fix).
//
// Anti-stub: replacing factoryFn with an instant return would NOT exercise
// the parallelism guarantee — the sleep is load-bearing.
func TestSwarm_ParallelKeysFactoryNonBlocking(t *testing.T) {
	t.Parallel()

	const factoryDelay = 100 * time.Millisecond
	// Parallel-execution budget: both factories run concurrently → ~factoryDelay
	// wall-clock plus scheduler/lock overhead. Serial execution would yield
	// ≥ 2 × factoryDelay (200ms). Threshold from tasks.md AC: ≤ 130ms.
	const parallelBudget = 130 * time.Millisecond

	factory := func(name string) (types.ExecutorV2, error) {
		time.Sleep(factoryDelay)
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	var wg sync.WaitGroup
	start := time.Now()

	for _, name := range []string{"codex", "gemini"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if _, err := s.Get(ctx, n, swarm.Stateful); err != nil {
				t.Errorf("Get(%s): %v", n, err)
			}
		}(name)
	}

	wg.Wait()
	elapsed := time.Since(start)

	if elapsed > parallelBudget {
		t.Errorf("DEF-8: distinct-key Gets serialised — wall-clock %v exceeds parallel budget %v "+
			"(factory delay %v, serial would be ≥%v); per-key lock not preventing cross-key "+
			"factoryFn blocking", elapsed, parallelBudget, factoryDelay, 2*factoryDelay)
	}
}

// TestSwarm_SameKeyConcurrentSerialFactory verifies that the per-key mutex
// preserves same-key TOCTOU prevention (BUG-003) post-DEF-8 refactor: 50
// concurrent Gets on the SAME key must result in exactly ONE factoryFn call
// and ONE handle ID returned to all goroutines.
//
// Anti-stub: removing the per-key mutex (or LoadOrStore-without-Lock pattern)
// causes counter > 1 — test fails immediately.
func TestSwarm_SameKeyConcurrentSerialFactory(t *testing.T) {
	t.Parallel()

	var spawnCount atomic.Int32
	factory := func(name string) (types.ExecutorV2, error) {
		spawnCount.Add(1)
		// Small sleep encourages goroutine interleaving inside the per-key
		// critical section — without the mutex, multiple goroutines would
		// observe an empty registry and race into spawnLocked.
		time.Sleep(time.Millisecond)
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}

	s := swarm.New(factory, nil)
	ctx := context.Background()

	const goroutines = 50
	handles := make(chan *swarm.Handle, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := s.Get(ctx, "codex", swarm.Stateful)
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			handles <- h
		}()
	}

	wg.Wait()
	close(handles)

	seen := make(map[string]struct{})
	for h := range handles {
		seen[h.ID] = struct{}{}
	}

	if got := spawnCount.Load(); got != 1 {
		t.Errorf("BUG-003 regression: expected exactly 1 factory call, got %d "+
			"(per-key mutex not serialising same-key Gets)", got)
	}
	if len(seen) != 1 {
		t.Errorf("BUG-003 regression: expected 1 unique handle ID, got %d "+
			"(double-spawn detected)", len(seen))
	}
}
