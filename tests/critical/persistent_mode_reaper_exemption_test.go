//go:build !short

// T018 — @critical TestCritical_PersistentMode_SurvivesStatefulReaper.
//
// AIMUX-14 CR-001 Phase 3 (US3). Verifies the Stateful TTL reaper kills
// only Stateful-mode handles; Persistent-mode handles survive idle past TTL.
//
// Anti-stub: removing the `h.Mode == Stateful` filter in reapStaleStateful
// causes the Persistent handle to also be killed and the test to fail.

package critical

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

// reaperTestExecutor — minimal mockExecutorV2-style stub local to this file
// to avoid coupling to other test packages.
type reaperTestExecutor struct {
	mu        sync.Mutex
	closed    bool
	id        string
	sendCount int
}

func (m *reaperTestExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{Name: m.id, Type: types.ExecutorTypeCLI}
}
func (m *reaperTestExecutor) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCount++
	return &types.Response{Content: msg.Content + " #" + strconv.Itoa(m.sendCount)}, nil
}
func (m *reaperTestExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	res, err := m.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: res.Content, Done: true})
	return res, nil
}
func (m *reaperTestExecutor) IsAlive() types.HealthStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return types.HealthDead
	}
	return types.HealthAlive
}
func (m *reaperTestExecutor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func TestCritical_PersistentMode_SurvivesStatefulReaper(t *testing.T) {
	// 50ms TTL → reaper interval 25ms; tests run ~150ms so 5+ ticks fire.
	const ttl = 50 * time.Millisecond

	factory := func(name string) (types.ExecutorV2, error) {
		return &reaperTestExecutor{id: name}, nil
	}

	sw := swarm.New(factory, audit.DiscardLog{}, swarm.WithStatefulTTL(ttl))
	defer sw.Shutdown(context.Background())

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "reap-test",
	})

	// Spawn 1 Stateful + 1 Persistent under the same tenant + name space.
	statefulHandle, err := sw.Get(ctx, "stateful-cli", swarm.Stateful)
	if err != nil {
		t.Fatalf("Get(Stateful): %v", err)
	}
	statefulID := statefulHandle.ID

	persistentHandle, err := sw.Get(ctx, "persistent-cli", swarm.Persistent)
	if err != nil {
		t.Fatalf("Get(Persistent): %v", err)
	}
	persistentID := persistentHandle.ID

	// Wait > 3×TTL to ensure reaper fires multiple ticks past the TTL boundary.
	time.Sleep(4 * ttl)

	// Stateful handle MUST be reaped — Get(Stateful) on same name returns
	// a NEW handle (different ID) because the previous one is gone.
	freshStateful, err := sw.Get(ctx, "stateful-cli", swarm.Stateful)
	if err != nil {
		t.Fatalf("post-TTL Get(Stateful): %v", err)
	}
	if freshStateful.ID == statefulID {
		t.Errorf("US3: Stateful handle %q survived TTL — reaper not engaging "+
			"(persistent leak; AIMUX-14 NFR-5/FR-4 broken)",
			statefulID)
	}

	// Persistent handle MUST survive — Get(Persistent) returns the SAME handle.
	freshPersistent, err := sw.Get(ctx, "persistent-cli", swarm.Persistent)
	if err != nil {
		t.Fatalf("post-TTL Get(Persistent): %v", err)
	}
	if freshPersistent.ID != persistentID {
		t.Errorf("US3: Persistent handle reaped (was %q, now %q) — reaper "+
			"exemption for Mode==Persistent broken",
			persistentID, freshPersistent.ID)
	}

	t.Logf("US3 verified: TTL=%v elapsed %v; Stateful reaped (old=%q, fresh=%q); "+
		"Persistent survived (id=%q)", ttl, 4*ttl, statefulID, freshStateful.ID, persistentID)
}
