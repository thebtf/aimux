package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

type restartTestExecutor struct {
	alive   types.HealthStatus
	closed  bool
	content string
}

func (e *restartTestExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{Name: "restart-test", Type: types.ExecutorTypeCLI}
}

func (e *restartTestExecutor) Send(_ context.Context, _ types.Message) (*types.Response, error) {
	return &types.Response{Content: e.content, Duration: time.Millisecond}, nil
}

func (e *restartTestExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	resp, err := e.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: resp.Content, Done: true})
	return resp, nil
}

func (e *restartTestExecutor) IsAlive() types.HealthStatus { return e.alive }

func (e *restartTestExecutor) Close() error {
	e.closed = true
	return nil
}

func TestRestart_ReconstructsTenantContextWhenFactoryCtxMissing(t *testing.T) {
	t.Parallel()

	type resolverCall struct {
		tenantID         string
		requestStartedAt time.Time
	}

	tests := []struct {
		name             string
		ctx              context.Context
		expectedTenantID string
	}{
		{
			name:             "non-empty tenant",
			ctx:              tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-a", RequestStartedAt: time.Now()}),
			expectedTenantID: "tenant-a",
		},
		{
			name:             "legacy default fallback",
			ctx:              context.Background(),
			expectedTenantID: tenant.LegacyDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dead := &restartTestExecutor{alive: types.HealthDead}
			fresh := &restartTestExecutor{alive: types.HealthAlive, content: "restarted"}

			var calls []resolverCall
			callCount := 0
			factory := func(ctx context.Context, _ string) (types.ExecutorV2, error) {
				tc, _ := tenant.FromContext(ctx)
				calls = append(calls, resolverCall{tenantID: tc.TenantID, requestStartedAt: tc.RequestStartedAt})
				callCount++
				if callCount == 1 {
					return dead, nil
				}
				return fresh, nil
			}

			s := NewWithContextFactory(factory, nil)
			h, err := s.Get(tt.ctx, "qwen", Stateful)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			expectedStartedAt := h.startedAt

			h.mu.Lock()
			h.factoryCtx = nil
			h.mu.Unlock()

			resp, err := s.Send(tt.ctx, h, types.Message{Content: "try"})
			if err != nil {
				t.Fatalf("Send after restart: %v", err)
			}
			if resp.Content != "restarted" {
				t.Fatalf("expected response from restarted executor, got %q", resp.Content)
			}
			if !dead.closed {
				t.Fatal("dead executor should have been closed during restart")
			}
			if len(calls) != 2 {
				t.Fatalf("factory calls = %d, want 2", len(calls))
			}
			if calls[1].tenantID != tt.expectedTenantID {
				t.Fatalf("restart fallback tenantID = %q, want %q", calls[1].tenantID, tt.expectedTenantID)
			}
			if calls[1].requestStartedAt.IsZero() {
				t.Fatal("restart fallback RequestStartedAt must be preserved")
			}
			if !calls[1].requestStartedAt.Equal(expectedStartedAt) {
				t.Fatalf("restart fallback RequestStartedAt = %v, want %v", calls[1].requestStartedAt, expectedStartedAt)
			}
			if tt.expectedTenantID == tenant.LegacyDefault && h.TenantID != "" {
				t.Fatalf("handle tenantID = %q, want canonical empty legacy tenant", h.TenantID)
			}
		})
	}
}
