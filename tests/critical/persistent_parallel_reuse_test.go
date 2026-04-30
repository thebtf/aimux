//go:build !short

// T020 — @critical TestCritical_PersistentMode_ParallelSameNameReuse (EC-9).
//
// AIMUX-14 CR-001 Phase 3 / EC-9: two consecutive Get(ctx, name, Persistent)
// calls with same TenantContext and same scope MUST return handles with
// IDENTICAL ID — no duplicate spawn (registryKey partition AIMUX-13 inherited).
//
// Anti-stub: registryKey partition collapse (e.g., scope-keyed instead of
// (tenantID, scope, name)-keyed) would create distinct handles and the test
// would fail.

package critical

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

type ec9Executor struct {
	mu        sync.Mutex
	closed    bool
	id        string
	sendCount int
}

func (m *ec9Executor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{Name: m.id, Type: types.ExecutorTypeCLI}
}
func (m *ec9Executor) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCount++
	return &types.Response{Content: msg.Content + " #" + strconv.Itoa(m.sendCount)}, nil
}
func (m *ec9Executor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	res, err := m.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: res.Content, Done: true})
	return res, nil
}
func (m *ec9Executor) IsAlive() types.HealthStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return types.HealthDead
	}
	return types.HealthAlive
}
func (m *ec9Executor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func TestCritical_PersistentMode_ParallelSameNameReuse(t *testing.T) {
	auditLog := &recordingAuditLog{}

	factory := func(name string) (types.ExecutorV2, error) {
		return &ec9Executor{id: name}, nil
	}

	sw := swarm.New(factory, auditLog, swarm.WithStatefulTTL(0)) // disable reaper
	defer sw.Shutdown(context.Background())

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "ec9-test",
	})

	// CONCURRENT Get(Persistent) calls — exercises the per-key sync.Map
	// mutex serialization (DEF-8 / FR-2) AND registryKey partition dedup.
	// Sequential calls would not validate the concurrent dedupe path
	// (PR #134 review — coderabbit major).
	const goroutines = 16
	handles := make(chan *swarm.Handle, goroutines)
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // synchronize all goroutines for max contention on first Get
			h, err := sw.Get(ctx, "codex", swarm.Persistent)
			if err != nil {
				errs <- err
				return
			}
			handles <- h
		}()
	}
	close(start)
	wg.Wait()
	close(handles)
	close(errs)

	for e := range errs {
		t.Errorf("concurrent Get error: %v", e)
	}

	// Collect unique handle IDs — all goroutines MUST receive the same handle.
	seen := make(map[string]int)
	var firstID string
	for h := range handles {
		if firstID == "" {
			firstID = h.ID
		}
		seen[h.ID]++
	}

	if len(seen) != 1 {
		t.Errorf("EC-9: %d concurrent Persistent Gets returned %d distinct "+
			"handle IDs (registryKey dedup broken): %v", goroutines, len(seen), seen)
	}

	// Audit must record exactly 1 spawn event for this name (anti-stub:
	// duplicate spawn surfaces 2+ events under concurrent contention).
	spawnCount := auditLog.eventsByType(audit.EventSwarmSpawn)
	const wantSpawns = 1
	if spawnCount != wantSpawns {
		t.Errorf("EC-9: expected %d EventSwarmSpawn under %d concurrent Persistent Gets, "+
			"got %d (duplicate-spawn regression)", wantSpawns, goroutines, spawnCount)
	}

	t.Logf("EC-9 verified: %d concurrent Persistent Gets → handle %q reused → %d spawn events",
		goroutines, firstID, spawnCount)
}
