//go:build !short

package critical_test

// TestCritical_Swarm_LegacyCanonicalization verifies FR-4 + CodeRabbit MAJOR PR #131:
// callers arriving with empty TenantContext ("") and callers arriving with explicit
// tenant.LegacyDefault context MUST hit the same registry partition. Otherwise the
// same logical legacy stream splits into two registry slots and produces ghost
// ErrHandleNotFound for cross-path Get/Send.
//
// @critical — release blocker per rule #10. Regression guard for canonicalTenantID.

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

// TestCritical_Swarm_LegacyCanonicalization_SamePartition verifies that:
//   - A Stateful handle spawned via context.Background() (TenantID == "")
//   - And a subsequent Get from a context carrying tenant.LegacyDefault
//   - Resolve to the SAME handle (no split-brain).
//
// Anti-stub check: removing canonicalTenantID and reverting to raw tc.TenantID
// makes the second Get spawn a NEW handle with a different ID, failing this test.
//
// @critical — release blocker per rule #10
func TestCritical_Swarm_LegacyCanonicalization_SamePartition(t *testing.T) {
	rec := &criticalAuditRecorder{}
	s := swarm.New(aliveExecutorFactory(), rec)

	// First Get: empty context — TenantID == "".
	emptyCtx := context.Background()
	h1, err := s.Get(emptyCtx, "codex", swarm.Stateful)
	if err != nil {
		t.Fatalf("CRITICAL: empty-ctx Get failed: %v", err)
	}

	// Second Get: explicit LegacyDefault context.
	legacyCtx := tenant.WithContext(context.Background(), tenant.NewLegacyDefaultContext("session-X"))
	h2, err := s.Get(legacyCtx, "codex", swarm.Stateful)
	if err != nil {
		t.Fatalf("CRITICAL: LegacyDefault-ctx Get failed: %v", err)
	}

	if h1.ID != h2.ID {
		t.Fatalf("CRITICAL: split-brain detected — empty ctx and LegacyDefault ctx returned "+
			"different handles (%q vs %q); canonicalTenantID is broken", h1.ID, h2.ID)
	}

	// Reverse direction: LegacyDefault first, then empty.
	h3, err := s.Get(legacyCtx, "gemini", swarm.Stateful)
	if err != nil {
		t.Fatalf("CRITICAL: LegacyDefault-ctx first Get failed: %v", err)
	}
	h4, err := s.Get(emptyCtx, "gemini", swarm.Stateful)
	if err != nil {
		t.Fatalf("CRITICAL: empty-ctx subsequent Get failed: %v", err)
	}
	if h3.ID != h4.ID {
		t.Fatalf("CRITICAL: split-brain (reverse) — LegacyDefault then empty returned "+
			"different handles (%q vs %q)", h3.ID, h4.ID)
	}

	// Cross-direction Send must succeed both ways (no false ErrHandleNotFound).
	if _, err := s.Send(legacyCtx, h1, types.Message{Content: "ping-from-legacy"}); err != nil {
		t.Fatalf("CRITICAL: Send via LegacyDefault ctx on empty-ctx-spawned handle failed: %v", err)
	}
	if _, err := s.Send(emptyCtx, h2, types.Message{Content: "ping-from-empty"}); err != nil {
		t.Fatalf("CRITICAL: Send via empty ctx on LegacyDefault-ctx-spawned handle failed: %v", err)
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("CRITICAL: Shutdown failed: %v", err)
	}
}
