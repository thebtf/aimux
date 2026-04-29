//go:build !short

package critical_test

// TestCritical_Swarm_CrossTenantHandleBlocked verifies CHK079 defense-in-depth:
// cross-tenant Send returns ErrHandleNotFound (NEVER 403) + audit emit.
//
// @critical — release blocker per rule #10

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

// aliveExecutorFactory returns a factory that always produces an alive mock executor.
func aliveExecutorFactory() func(string) (types.ExecutorV2, error) {
	return func(_ string) (types.ExecutorV2, error) {
		return &swarmCriticalMock{alive: types.HealthAlive}, nil
	}
}

// swarmCriticalMock is a minimal ExecutorV2 implementation for critical-suite tests.
type swarmCriticalMock struct {
	alive types.HealthStatus
}

func (m *swarmCriticalMock) Info() types.ExecutorInfo { return types.ExecutorInfo{} }

func (m *swarmCriticalMock) Send(_ context.Context, _ types.Message) (*types.Response, error) {
	return &types.Response{Content: "ok", Duration: time.Millisecond}, nil
}

func (m *swarmCriticalMock) SendStream(_ context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	resp, err := m.Send(context.Background(), msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: resp.Content, Done: true})
	return resp, nil
}

func (m *swarmCriticalMock) IsAlive() types.HealthStatus { return m.alive }
func (m *swarmCriticalMock) Close() error                { return nil }

// tenantCtxFor returns a context carrying the given tenantID.
func tenantCtxFor(tenantID string) context.Context {
	tc := tenant.TenantContext{
		TenantID:         tenantID,
		RequestStartedAt: time.Now(),
	}
	return tenant.WithContext(context.Background(), tc)
}

// TestCritical_Swarm_CrossTenantHandleBlocked verifies that Send from a
// different tenant context returns ErrHandleNotFound (not a 403 or any
// information-leaking error), and that the audit log captures a
// EventCrossTenantBlocked event identifying the offending tenant and handle.
//
// Anti-stub check: reverting T005 (registryKey) + T006 (cross-tenant check)
// + T007 (audit emission) makes this test fail. All three aspects are verified.
//
// @critical — release blocker per rule #10
func TestCritical_Swarm_CrossTenantHandleBlocked(t *testing.T) {
	rec := &criticalAuditRecorder{}
	s := swarm.New(aliveExecutorFactory(), rec)

	aliceCtx := tenantCtxFor("alice")
	bobCtx := tenantCtxFor("bob")

	// Alice spawns a Stateful handle.
	h, err := s.Get(aliceCtx, "codex", swarm.Stateful)
	if err != nil {
		t.Fatalf("CRITICAL: Alice Get failed: %v", err)
	}
	if h == nil {
		t.Fatal("CRITICAL: Alice Get returned nil handle")
	}

	// Bob attempts to Send on Alice's handle — must be rejected.
	_, sendErr := s.Send(bobCtx, h, types.Message{Content: "cross-tenant probe"})

	// 1. Error must be ErrHandleNotFound.
	if !errors.Is(sendErr, swarm.ErrHandleNotFound) {
		t.Fatalf("CRITICAL: cross-tenant Send returned %v, want ErrHandleNotFound", sendErr)
	}

	// 2. Error message must not leak Alice's tenantID (CHK079 info-leak defense).
	if sendErr != nil && strings.Contains(sendErr.Error(), "alice") {
		t.Errorf("CRITICAL: error message leaks victim tenant ID %q: %q", "alice", sendErr.Error())
	}

	// 3. Audit log must contain EventCrossTenantBlocked for Bob's offending tenant
	//    with the target handle ID — verifies T007 emission.
	if !rec.hasEvent(func(ev audit.AuditEvent) bool {
		return ev.EventType == audit.EventCrossTenantBlocked &&
			ev.TenantID == "bob" &&
			ev.ResourceID == h.ID
	}) {
		t.Errorf("CRITICAL: missing EventCrossTenantBlocked audit event (TenantID=bob, ResourceID=%s): %+v",
			h.ID, rec.Snapshot())
	}

	// 4. SendStream from Bob on Alice's handle must also be rejected identically
	//    (CHK079 applies to the streaming path as well as the unary path).
	_, streamErr := s.SendStream(bobCtx, h, types.Message{Content: "cross-tenant stream probe"}, func(types.Chunk) {})

	if !errors.Is(streamErr, swarm.ErrHandleNotFound) {
		t.Fatalf("CRITICAL: cross-tenant SendStream returned %v, want ErrHandleNotFound", streamErr)
	}
	if streamErr != nil && strings.Contains(streamErr.Error(), "alice") {
		t.Errorf("CRITICAL: SendStream error message leaks victim tenant ID %q: %q", "alice", streamErr.Error())
	}
	if !rec.hasEvent(func(ev audit.AuditEvent) bool {
		return ev.EventType == audit.EventCrossTenantBlocked &&
			ev.TenantID == "bob" &&
			ev.ResourceID == h.ID
	}) {
		t.Errorf("CRITICAL: missing EventCrossTenantBlocked audit event for SendStream (TenantID=bob, ResourceID=%s): %+v",
			h.ID, rec.Snapshot())
	}
}
