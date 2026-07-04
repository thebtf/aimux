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

	dead := &restartTestExecutor{alive: types.HealthDead}
	fresh := &restartTestExecutor{alive: types.HealthAlive, content: "restarted"}

	var seen []string
	callCount := 0
	factory := func(ctx context.Context, _ string) (types.ExecutorV2, error) {
		tc, _ := tenant.FromContext(ctx)
		seen = append(seen, tc.TenantID)
		callCount++
		if callCount == 1 {
			return dead, nil
		}
		return fresh, nil
	}

	s := NewWithContextFactory(factory, nil)
	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-a"})

	h, err := s.Get(ctx, "qwen", Stateful)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	h.mu.Lock()
	h.factoryCtx = nil
	h.mu.Unlock()

	resp, err := s.Send(ctx, h, types.Message{Content: "try"})
	if err != nil {
		t.Fatalf("Send after restart: %v", err)
	}
	if resp.Content != "restarted" {
		t.Fatalf("expected response from restarted executor, got %q", resp.Content)
	}
	if !dead.closed {
		t.Fatal("dead executor should have been closed during restart")
	}
	if len(seen) != 2 {
		t.Fatalf("factory calls = %d, want 2", len(seen))
	}
	if seen[0] != "tenant-a" || seen[1] != "tenant-a" {
		t.Fatalf("factory tenant trail = %#v", seen)
	}
}
