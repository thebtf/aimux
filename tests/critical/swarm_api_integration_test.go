//go:build !short

package critical_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	api "github.com/thebtf/aimux/pkg/executor/api"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

func tenantCtxForSwarmAPI(tenantID string) context.Context {
	return tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID:         tenantID,
		RequestStartedAt: time.Now(),
	})
}

func TestCritical_SwarmAPI_MultiProviderIntegration(t *testing.T) {
	t.Parallel()

	rec := &criticalAuditRecorder{}

	var mu sync.Mutex
	resolverCalls := map[string]int{}
	resolvedKeys := map[string][]string{}
	apiFactory := api.NewSwarmFactory(func(_ context.Context, provider, tenantID string) (string, error) {
		key := tenantID + "|" + provider
		resolved := "test-key-" + tenantID + "-" + provider
		mu.Lock()
		resolverCalls[key]++
		resolvedKeys[key] = append(resolvedKeys[key], resolved)
		mu.Unlock()
		return resolved, nil
	}, api.WithTimeout(2*time.Second), api.WithMaxRetries(1))

	cliCalls := 0
	cliFactory := func(name string) (types.ExecutorV2, error) {
		cliCalls++
		return &swarmAPICriticalMock{name: name, alive: types.HealthAlive}, nil
	}

	sw := swarm.NewWithContextFactory(api.ContextCompositeFactory(cliFactory, apiFactory), rec, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	tenantA := tenantCtxForSwarmAPI("tenantA")
	tenantB := tenantCtxForSwarmAPI("tenantB")

	executorNames := []string{
		"api:openai:gpt-4o",
		"api:anthropic:claude-sonnet-4-5-20250929",
		"api:google:gemini-2.0-flash",
	}

	seenIDs := map[string]bool{}
	for _, name := range executorNames {
		h, err := sw.Get(tenantA, name, swarm.Stateless)
		if err != nil {
			t.Fatalf("Get(%s, tenantA): %v", name, err)
		}
		if h == nil {
			t.Fatalf("Get(%s, tenantA): nil handle", name)
		}
		if h.Name != name {
			t.Fatalf("handle.Name = %q, want %q", h.Name, name)
		}
		if h.Mode != swarm.Stateless {
			t.Fatalf("handle.Mode = %v, want %v", h.Mode, swarm.Stateless)
		}
		if h.TenantID != "tenantA" {
			t.Fatalf("handle.TenantID = %q, want tenantA", h.TenantID)
		}
		if seenIDs[h.ID] {
			t.Fatalf("duplicate handle ID %q for %s", h.ID, name)
		}
		seenIDs[h.ID] = true
	}

	crossTenantA, err := sw.Get(tenantA, "api:openai:gpt-4o", swarm.Stateless)
	if err != nil {
		t.Fatalf("Get(openai, tenantA repeat): %v", err)
	}
	crossTenantB, err := sw.Get(tenantB, "api:openai:gpt-4o", swarm.Stateless)
	if err != nil {
		t.Fatalf("Get(openai, tenantB): %v", err)
	}
	if crossTenantA.ID == crossTenantB.ID {
		t.Fatalf("cross-tenant stateless handles reused ID %q", crossTenantA.ID)
	}
	if crossTenantA.TenantID != "tenantA" || crossTenantB.TenantID != "tenantB" {
		t.Fatalf("cross-tenant tenant IDs = (%q, %q), want (tenantA, tenantB)", crossTenantA.TenantID, crossTenantB.TenantID)
	}

	if cliCalls != 0 {
		t.Fatalf("cli factory called %d time(s), want 0 for api:* names", cliCalls)
	}

	mu.Lock()
	defer mu.Unlock()
	if resolverCalls["tenantA|openai"] != 2 {
		t.Fatalf("tenantA openai resolver calls = %d, want 2", resolverCalls["tenantA|openai"])
	}
	if resolverCalls["tenantB|openai"] != 1 {
		t.Fatalf("tenantB openai resolver calls = %d, want 1", resolverCalls["tenantB|openai"])
	}
	if resolverCalls["tenantA|anthropic"] != 1 {
		t.Fatalf("tenantA anthropic resolver calls = %d, want 1", resolverCalls["tenantA|anthropic"])
	}
	if resolverCalls["tenantA|google"] != 1 {
		t.Fatalf("tenantA google resolver calls = %d, want 1", resolverCalls["tenantA|google"])
	}
	if got := resolvedKeys["tenantA|openai"]; len(got) != 2 || got[0] != "test-key-tenantA-openai" || got[1] != "test-key-tenantA-openai" {
		t.Fatalf("tenantA openai resolved keys = %#v", got)
	}
	if got := resolvedKeys["tenantB|openai"]; len(got) != 1 || got[0] != "test-key-tenantB-openai" {
		t.Fatalf("tenantB openai resolved keys = %#v", got)
	}
	if resolvedKeys["tenantA|openai"][0] == resolvedKeys["tenantB|openai"][0] {
		t.Fatal("cross-tenant openai key resolution collapsed to the same key")
	}

	if !rec.hasEvent(func(ev audit.AuditEvent) bool {
		return ev.EventType == audit.EventSwarmSpawn && ev.TenantID == "tenantA"
	}) {
		t.Fatal("missing EventSwarmSpawn for tenantA api handle")
	}
	if !rec.hasEvent(func(ev audit.AuditEvent) bool {
		return ev.EventType == audit.EventSwarmSpawn && ev.TenantID == "tenantB"
	}) {
		t.Fatal("missing EventSwarmSpawn for tenantB api handle")
	}
}

type swarmAPICriticalMock struct {
	name  string
	alive types.HealthStatus
}

func (m *swarmAPICriticalMock) Info() types.ExecutorInfo {
	return types.ExecutorInfo{Name: m.name, Type: types.ExecutorTypeCLI}
}

func (m *swarmAPICriticalMock) Send(_ context.Context, _ types.Message) (*types.Response, error) {
	return &types.Response{Content: "ok", Duration: time.Millisecond}, nil
}

func (m *swarmAPICriticalMock) SendStream(_ context.Context, _ types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	resp := &types.Response{Content: "ok", Duration: time.Millisecond}
	onChunk(types.Chunk{Content: resp.Content, Done: true})
	return resp, nil
}

func (m *swarmAPICriticalMock) IsAlive() types.HealthStatus { return m.alive }
func (m *swarmAPICriticalMock) Close() error                { return nil }
