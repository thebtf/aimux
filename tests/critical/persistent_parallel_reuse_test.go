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

	// First Get(Persistent) — spawns the handle.
	h1, err := sw.Get(ctx, "codex", swarm.Persistent)
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}

	// Second Get(Persistent) with same tenantID+scope+name — MUST return same handle.
	h2, err := sw.Get(ctx, "codex", swarm.Persistent)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}

	if h1.ID != h2.ID {
		t.Errorf("EC-9: parallel Persistent Get returned distinct handles "+
			"(h1=%q, h2=%q) — registryKey partition broken", h1.ID, h2.ID)
	}

	// Audit must record exactly 1 spawn event for this name (anti-stub:
	// duplicate spawn surfaces 2 events).
	spawnCount := auditLog.eventsByType(audit.EventSwarmSpawn)
	const wantSpawns = 1
	if spawnCount != wantSpawns {
		t.Errorf("EC-9: expected %d EventSwarmSpawn for repeated Persistent Get, "+
			"got %d (duplicate-spawn regression)", wantSpawns, spawnCount)
	}

	t.Logf("EC-9 verified: %d Persistent Gets → handle %q reused → %d spawn events",
		2, h1.ID, spawnCount)
}
