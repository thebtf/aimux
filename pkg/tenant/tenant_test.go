package tenant_test

import (
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/tenant"
)

// --- T001: TenantContext type invariants ---

func TestTenantContext_ZeroValueSafe(t *testing.T) {
	var tc tenant.TenantContext
	// Zero value must not panic on field access; TenantID must be empty string.
	if tc.TenantID != "" {
		t.Fatalf("expected empty TenantID on zero value, got %q", tc.TenantID)
	}
}

func TestNewDaemonContext_ReturnsDaemonInternalScope(t *testing.T) {
	dc := tenant.NewDaemonContext()
	if dc.TenantID == "" {
		t.Fatal("NewDaemonContext() returned empty TenantID")
	}
	// Must not equal LegacyDefault — daemon context is separate scope.
	if dc.TenantID == tenant.LegacyDefault {
		t.Fatalf("daemon context TenantID must not equal LegacyDefault (%q)", tenant.LegacyDefault)
	}
}

func TestTenantContext_LegacyDefaultConstant(t *testing.T) {
	if tenant.LegacyDefault == "" {
		t.Fatal("LegacyDefault must not be empty")
	}
}

func TestTenantContext_IsValueType(t *testing.T) {
	// Assigning a TenantContext to a new variable must produce an independent copy.
	a := tenant.TenantContext{
		TenantID:         "alice",
		PeerUID:          1001,
		SessionID:        "sess-1",
		RequestStartedAt: time.Now(),
	}
	b := a
	b.TenantID = "bob"
	if a.TenantID != "alice" {
		t.Fatal("TenantContext must be a value type — mutation of copy must not affect original")
	}
}

func TestNewLegacyDefaultContext_Fields(t *testing.T) {
	sessionID := "test-session-id"
	tc := tenant.NewLegacyDefaultContext(sessionID)
	if tc.TenantID != tenant.LegacyDefault {
		t.Fatalf("expected TenantID=%q, got %q", tenant.LegacyDefault, tc.TenantID)
	}
	if tc.SessionID != sessionID {
		t.Fatalf("expected SessionID=%q, got %q", sessionID, tc.SessionID)
	}
}
