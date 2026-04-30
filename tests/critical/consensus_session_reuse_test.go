//go:build !short

// T014 — @critical TestCritical_Consensus_SessionReuse_SpawnEventCount.
//
// AIMUX-14 CR-001 Phase 2 (US2). Verifies that Swarm Stateful mode caches
// handles across multiple Send rounds: 3 CLI factories driven through 5 rounds
// must produce exactly 3 EventSwarmSpawn audit events (one per CLI), NOT 15
// (per-round spawn).
//
// Anti-stub: bypassing the registry cache (e.g., changing Get(Stateful) to
// always spawn a new handle) immediately surfaces 15 spawn events and fails
// the test. The existing Swarm registry partition (AIMUX-13) is a hard
// prerequisite — this test guards against regression.

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

// recordingAuditLog captures all events for assertion. Thread-safe.
type recordingAuditLog struct {
	mu     sync.Mutex
	events []audit.AuditEvent
}

func (r *recordingAuditLog) Emit(ev audit.AuditEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingAuditLog) Close() error { return nil }

func (r *recordingAuditLog) eventsByType(t audit.EventType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := 0
	for _, e := range r.events {
		if e.EventType == t {
			c++
		}
	}
	return c
}

// mockSessionAwareExecutor mocks an ExecutorV2 with PersistentSessions
// capability so Swarm Stateful caching is exercised across rounds.
type mockSessionAwareExecutor struct {
	name      string
	mu        sync.Mutex
	sendCount int
}

func (m *mockSessionAwareExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: m.name,
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: true,
		},
	}
}

func (m *mockSessionAwareExecutor) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCount++
	return &types.Response{
		Content:  m.name + ": " + msg.Content + " #" + strconv.Itoa(m.sendCount),
		Duration: time.Microsecond,
	}, nil
}

func (m *mockSessionAwareExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	res, err := m.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: res.Content, Done: true})
	return res, nil
}

func (m *mockSessionAwareExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (m *mockSessionAwareExecutor) Close() error                { return nil }

func TestCritical_Consensus_SessionReuse_SpawnEventCount(t *testing.T) {
	auditLog := &recordingAuditLog{}

	// One factory shared across all CLIs — the name argument selects the mock.
	factory := func(name string) (types.ExecutorV2, error) {
		return &mockSessionAwareExecutor{name: name}, nil
	}

	sw := swarm.New(factory, auditLog)
	defer sw.Shutdown(context.Background())

	clis := []string{"codex", "gemini", "claude"}
	const rounds = 5

	// Bind a multi-tenant ctx so audit emit is not flood-suppressed
	// (FR-4 isMultiTenantID guard — legacy-default tenants skip spawn/close
	// audit lines to avoid per-Send chatter on single-operator deployments).
	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "test-tenant",
	})

	for round := 1; round <= rounds; round++ {
		for _, cli := range clis {
			h, err := sw.Get(ctx, cli, swarm.Stateful)
			if err != nil {
				t.Fatalf("Get(%s) round %d: %v", cli, round, err)
			}
			if _, err := sw.Send(ctx, h, types.Message{Content: "round-" + strconv.Itoa(round)}); err != nil {
				t.Fatalf("Send(%s) round %d: %v", cli, round, err)
			}
		}
	}

	// US2 acceptance: exactly 3 spawn events (one per CLI), NOT rounds×3=15.
	spawnCount := auditLog.eventsByType(audit.EventSwarmSpawn)
	const wantSpawns = 3
	if spawnCount != wantSpawns {
		t.Errorf("US2: expected exactly %d EventSwarmSpawn events (one per CLI), "+
			"got %d (handle caching broken; per-round spawn would be %d)",
			wantSpawns, spawnCount, len(clis)*rounds)
	}

	t.Logf("US2 verified: %d CLIs × %d rounds → %d spawn events (cache hit %d times)",
		len(clis), rounds, spawnCount, len(clis)*rounds-spawnCount)
}
