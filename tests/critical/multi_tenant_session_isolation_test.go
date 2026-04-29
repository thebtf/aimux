//go:build !short

// Package critical hosts the AIMUX-12 critical-suite — release-blocking
// integration tests that MUST pass on a real or staged dev stand before
// every release per nvmd-platform rule #10.
//
// Each test in this package is annotated with `// @critical` on the
// function godoc line so the critical-suite skill can grep for them.
//
// The tests consume only public APIs of the packages they exercise; they
// do not depend on muxcore daemon being live and run fully in-process via
// t.TempDir() SQLite databases.
package critical_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
	_ "modernc.org/sqlite"
)

// makeCriticalSession constructs a minimal Session row for tenant-isolation
// tests. TenantID is stamped on the persisted row; CLI/Mode/Status are set
// to safe constants so the FK and CHECK constraints in the schema are met.
func makeCriticalSession(id, tenantID string) *session.Session {
	now := time.Now().UTC()
	return &session.Session{
		ID:           id,
		CLI:          "codex",
		Mode:         types.SessionModeLive,
		Status:       types.SessionStatusCreated,
		TenantID:     tenantID,
		CreatedAt:    now,
		LastActiveAt: now,
	}
}

// TestCritical_SessionIsolation_CrossTenantGetReturnsNotFound verifies the
// AIMUX-12 ADR-09 information-hiding contract: tenant B asking for a session
// owned by tenant A must receive (nil, nil) — not 403, not "tenant denied",
// not the foreign row. The 404 semantics prevent existence disclosure.
//
// @critical — release blocker per rule #10
func TestCritical_SessionIsolation_CrossTenantGetReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "critical_session_isolation.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ts := session.NewTenantScopedStore(store)

	tcA := tenant.TenantContext{TenantID: "tenantA", RequestStartedAt: time.Now()}
	tcB := tenant.TenantContext{TenantID: "tenantB", RequestStartedAt: time.Now()}
	ctxA := session.WithTenant(context.Background(), tcA)
	ctxB := session.WithTenant(context.Background(), tcB)

	const sharedID = "sess-tenantA-secret"
	if err := ts.SnapshotSession(ctxA, makeCriticalSession(sharedID, "tenantA")); err != nil {
		t.Fatalf("SnapshotSession(A): %v", err)
	}

	// Boundary 1: tenantB MUST NOT see tenantA's session via direct GetSession.
	got, err := ts.GetSession(ctxB, sharedID)
	if err != nil {
		t.Fatalf("GetSession(B, sharedID): unexpected error %v — must be (nil, nil) not error to avoid existence disclosure", err)
	}
	if got != nil {
		t.Fatalf("CRITICAL: cross-tenant read leaked session %q to tenantB (TenantID=%q)", got.ID, got.TenantID)
	}

	// Boundary 2: tenantB QuerySessions returns ZERO rows — not even masked.
	rowsB, err := ts.QuerySessions(ctxB)
	if err != nil {
		t.Fatalf("QuerySessions(B): %v", err)
	}
	if len(rowsB) != 0 {
		t.Fatalf("CRITICAL: tenantB sees %d session(s); expected 0", len(rowsB))
	}

	// Boundary 3: tenantA still sees its own session — isolation is symmetric,
	// not a reads-blocked-for-everyone bug.
	gotA, err := ts.GetSession(ctxA, sharedID)
	if err != nil {
		t.Fatalf("GetSession(A): %v", err)
	}
	if gotA == nil || gotA.ID != sharedID || gotA.TenantID != "tenantA" {
		t.Fatalf("tenantA owner read failed: got %+v", gotA)
	}
}

// TestCritical_SessionIsolation_MissingTenantContextRejected verifies that a
// caller that forgets to inject a TenantContext receives a clean error rather
// than a panic or a silent legacy-default insert. Defends against the FR-1
// regression where a tool handler bypasses the WithTenant middleware.
//
// @critical — release blocker per rule #10
func TestCritical_SessionIsolation_MissingTenantContextRejected(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "critical_no_ctx.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ts := session.NewTenantScopedStore(store)

	if err := ts.SnapshotSession(context.Background(), makeCriticalSession("orphan", "")); err == nil {
		t.Fatal("CRITICAL: SnapshotSession accepted a context with no TenantContext — must return ErrMissingTenantContext")
	}

	if _, err := ts.QuerySessions(context.Background()); err == nil {
		t.Fatal("CRITICAL: QuerySessions accepted a context with no TenantContext — must return ErrMissingTenantContext")
	}
}
