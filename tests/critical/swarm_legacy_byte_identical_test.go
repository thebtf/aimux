//go:build !short

package critical_test

// TestCritical_Swarm_LegacyMode_ByteIdentical verifies FR-4 backward compat:
// empty TenantContext → all calls go through LegacyDefault partition byte-identical
// to pre-AIMUX-13 behavior. NO EventSwarmSpawn / EventSwarmClose emits (anti-flood).
//
// @critical — release blocker per rule #10

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

// TestCritical_Swarm_LegacyMode_ByteIdentical verifies that when no TenantContext
// is present in ctx (legacy single-tenant deployment), Swarm behaves byte-identically
// to pre-AIMUX-13: Get returns reused handles and NO swarm_spawn / swarm_close
// audit events are emitted (FR-4 anti-flood protection).
//
// Anti-stub check: reverting FR-4 anti-flood logic in emitSpawn/emitClose (T007)
// makes this test fail because the recorder would contain spawn/close events.
// Reverting T005 registryKey shape would break handle reuse, also failing the test.
//
// @critical — release blocker per rule #10
func TestCritical_Swarm_LegacyMode_ByteIdentical(t *testing.T) {
	rec := &criticalAuditRecorder{}
	s := swarm.New(aliveExecutorFactory(), rec)

	// Empty context — no TenantContext injected — simulates pre-AIMUX-13 caller.
	ctx := context.Background()

	// First Get must succeed and produce a Stateful handle.
	h1, err := s.Get(ctx, "codex", swarm.Stateful)
	if err != nil {
		t.Fatalf("CRITICAL: legacy mode Get #1 failed: %v", err)
	}
	if h1 == nil {
		t.Fatal("CRITICAL: legacy mode Get #1 returned nil handle")
	}

	// Second Get with identical ctx must reuse the same handle (byte-identical
	// to pre-AIMUX-13 Stateful caching behaviour).
	h2, err := s.Get(ctx, "codex", swarm.Stateful)
	if err != nil {
		t.Fatalf("CRITICAL: legacy mode Get #2 failed: %v", err)
	}
	if h1.ID != h2.ID {
		t.Errorf("CRITICAL: legacy mode Get returned different handles (%q vs %q); "+
			"expected same handle (byte-identical pre-AIMUX-13 behavior)", h1.ID, h2.ID)
	}

	// Send must succeed on the legacy handle.
	_, sendErr := s.Send(ctx, h1, types.Message{Content: "legacy ping"})
	if sendErr != nil {
		t.Fatalf("CRITICAL: legacy mode Send failed: %v", sendErr)
	}

	// Shutdown must not error.
	shutdownCtx := context.Background()
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("CRITICAL: legacy mode Shutdown failed: %v", err)
	}

	// Anti-flood assertion: NO swarm_spawn or swarm_close events must have been
	// emitted in legacy mode. The single-tenant deployment must not be flooded
	// with audit log entries on routine Get/Send/Shutdown calls (FR-4).
	for _, ev := range rec.Snapshot() {
		if ev.EventType == audit.EventSwarmSpawn {
			t.Errorf("CRITICAL: legacy mode emitted EventSwarmSpawn (anti-flood violated): %+v", ev)
		}
		if ev.EventType == audit.EventSwarmClose {
			t.Errorf("CRITICAL: legacy mode emitted EventSwarmClose (anti-flood violated): %+v", ev)
		}
	}
}
