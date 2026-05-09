package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/upgrade"
)

func TestRequireOperator_AllowsOperatorAndDeniesPlain(t *testing.T) {
	srv := testServer(t)

	operatorCtx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "operator-a",
		Role:     tenant.RoleOperator,
	})
	if err := srv.requireOperator(operatorCtx, "upgrade apply"); err != nil {
		t.Fatalf("operator context denied: %v", err)
	}

	plainCtx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "tenant-b",
		Role:     tenant.RolePlain,
	})
	err := srv.requireOperator(plainCtx, "upgrade apply")
	if err == nil {
		t.Fatal("expected plain tenant to be denied")
	}
	if !strings.Contains(err.Error(), tenant.RoleOperator) {
		t.Fatalf("error = %q, want operator role detail", err)
	}
}

func TestSessionsListScopesPlainTenant(t *testing.T) {
	srv := testServer(t)
	importSession(t, srv, "session-a", "tenant-a")
	importSession(t, srv, "session-b", "tenant-b")

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "tenant-a",
		Role:     tenant.RolePlain,
	})
	result, err := srv.handleSessions(ctx, makeRequest("sessions", map[string]any{
		"action": "list",
	}))
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}

	payload := parseResult(t, result)
	rows, ok := payload["sessions"].([]any)
	if !ok {
		t.Fatalf("sessions field = %T, want []any; payload=%v", payload["sessions"], payload)
	}
	if len(rows) != 1 {
		t.Fatalf("sessions length = %d, want 1; rows=%v", len(rows), rows)
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("session row = %T, want map", rows[0])
	}
	if row["id"] != "session-a" {
		t.Fatalf("visible session id = %v, want session-a", row["id"])
	}
}

func TestSessionsAllAndKillRequireOperator(t *testing.T) {
	srv := testServer(t)
	importSession(t, srv, "session-a", "tenant-a")
	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "tenant-a",
		Role:     tenant.RolePlain,
	})

	listAll, err := srv.handleSessions(ctx, makeRequest("sessions", map[string]any{
		"action": "list",
		"all":    true,
	}))
	if err != nil {
		t.Fatalf("handleSessions list all: %v", err)
	}
	if !strings.Contains(parseResult(t, listAll)["text"].(string), tenant.RoleOperator) {
		t.Fatalf("list all error = %v, want operator denial", parseResult(t, listAll))
	}

	kill, err := srv.handleSessions(ctx, makeRequest("sessions", map[string]any{
		"action":     "kill",
		"session_id": "session-a",
	}))
	if err != nil {
		t.Fatalf("handleSessions kill: %v", err)
	}
	if !strings.Contains(parseResult(t, kill)["text"].(string), tenant.RoleOperator) {
		t.Fatalf("kill error = %v, want operator denial", parseResult(t, kill))
	}
}

func TestUpgradeApplyRequiresOperator(t *testing.T) {
	srv := testServer(t)
	called := false
	srv.applyUpgrade = func(context.Context, *upgrade.Coordinator, upgrade.Mode, bool) (*upgrade.Result, error) {
		called = true
		return nil, nil
	}

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "tenant-a",
		Role:     tenant.RolePlain,
	})
	result, err := srv.handleUpgrade(ctx, makeRequest("upgrade", map[string]any{
		"action": "apply",
		"source": "local-dev.exe",
	}))
	if err != nil {
		t.Fatalf("handleUpgrade: %v", err)
	}
	if called {
		t.Fatal("applyUpgrade must not be called for a plain tenant")
	}
	if !strings.Contains(parseResult(t, result)["text"].(string), tenant.RoleOperator) {
		t.Fatalf("upgrade error = %v, want operator denial", parseResult(t, result))
	}
}

func TestTenantRoleForID_DoesNotPromoteLegacyDefaultInMultiTenantMode(t *testing.T) {
	srv := testServer(t)
	reg := tenant.NewRegistry()
	reg.Swap(tenant.NewSnapshot(map[int]tenant.TenantConfig{
		1001: {Name: "tenant-a", UID: 1001, Role: tenant.RolePlain},
	}))
	srv.dispatchMW = NewDispatchMiddleware(reg, discardAuditLog{})

	if got := srv.tenantRoleForID(tenant.LegacyDefault); got != "" {
		t.Fatalf("legacy-default role in multi-tenant mode = %q, want empty deny role", got)
	}
	if got := srv.tenantRoleForID(""); got != "" {
		t.Fatalf("empty tenant role in multi-tenant mode = %q, want empty deny role", got)
	}
	if got := srv.tenantRoleForID("tenant-a"); got != tenant.RolePlain {
		t.Fatalf("tenant-a role = %q, want %q", got, tenant.RolePlain)
	}
}

func importSession(t *testing.T, srv *Server, id, tenantID string) {
	t.Helper()
	now := time.Now()
	srv.sessions.Import(&session.Session{
		ID:           id,
		CLI:          "codex",
		Mode:         types.SessionModeOnceStateless,
		Status:       types.SessionStatusRunning,
		TenantID:     tenantID,
		CreatedAt:    now,
		LastActiveAt: now,
	})
}
