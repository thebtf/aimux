package server

// TestServer_AuditInitFatal_MultiTenant — RED gate (FR-4 / DEF-11 / T005).
//
// Verifies that initAuditLog invokes auditFatalfFn when:
//   - the tenant registry reports IsMultiTenant() == true, AND
//   - audit log initialisation fails (unwritable directory path).
//
// Also verifies the single-tenant path (EC-5): the same failure does NOT
// invoke auditFatalfFn — it logs a warning and falls back to discardAuditLog.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/tenant"
)

// newTestLogger is reused from auth_test.go (same package).

// multiTenantRegistry returns a TenantRegistry with one enrolled tenant so
// that IsMultiTenant() returns true.
func multiTenantRegistry(t *testing.T) *tenant.TenantRegistry {
	t.Helper()
	snap := tenant.NewSnapshot(map[int]tenant.TenantConfig{
		1001: {Name: "test-tenant", UID: 1001},
	})
	reg := tenant.NewRegistry()
	reg.Swap(snap)
	if !reg.IsMultiTenant() {
		t.Fatal("test setup: expected IsMultiTenant() == true after Swap")
	}
	return reg
}

// unwritableAuditPath returns a config whose DBPath resolves to a directory
// that cannot be created because its parent is a file (not a directory), forcing
// os.MkdirAll to fail when initAuditLog tries to create the audit log directory.
func unwritableAuditPath(t *testing.T) *config.Config {
	t.Helper()
	// Create a plain file where a directory is expected — MkdirAll will fail.
	parent := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(parent, []byte("not-a-dir"), 0600); err != nil {
		t.Fatalf("setup unwritable path: %v", err)
	}
	// DBPath expansion: filepath.Dir(".aimux/audit.log") traversal needs the
	// parent to be "blocked" so that os.MkdirAll("blocked/.aimux", 0700) fails
	// because "blocked" is a file.
	return &config.Config{
		Server: config.ServerConfig{
			DBPath: filepath.Join(parent, "aimux.db"),
		},
	}
}

// TestServer_AuditInitFatal_MultiTenant verifies that audit init failure in
// multi-tenant mode invokes auditFatalfFn (fail-closed — DEF-11 / FR-4).
func TestServer_AuditInitFatal_MultiTenant(t *testing.T) {
	// Swap auditFatalfFn to a capturing panic-stub; restore on exit.
	var captured string
	saved := auditFatalfFn
	t.Cleanup(func() { auditFatalfFn = saved })
	auditFatalfFn = func(format string, args ...any) {
		captured = fmt.Sprintf(format, args...)
		panic("audit-fatal-stub") // simulate os.Exit(1) without killing the process
	}

	reg := multiTenantRegistry(t)
	cfg := unwritableAuditPath(t)
	log := newTestLogger(t)

	// initAuditLog must call auditFatalfFn → panic "audit-fatal-stub".
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected auditFatalfFn to panic (simulate Fatal) but it did not")
			}
		}()
		initAuditLog(cfg, log, reg)
	}()

	// auditFatalfFn was called — captured must contain the failure context.
	if captured == "" {
		t.Fatal("auditFatalfFn was not called: captured message is empty")
	}
	// Message must reference multi-tenant context.
	const wantSubstr = "multi-tenant mode"
	if !strings.Contains(captured, wantSubstr) {
		t.Fatalf("auditFatalfFn message %q does not contain %q", captured, wantSubstr)
	}
}

// TestServer_AuditInitWarn_SingleTenant verifies that audit init failure in
// single-tenant mode does NOT call auditFatalfFn (EC-5 — dev-iteration path).
func TestServer_AuditInitWarn_SingleTenant(t *testing.T) {
	var fatalCalled bool
	saved := auditFatalfFn
	t.Cleanup(func() { auditFatalfFn = saved })
	auditFatalfFn = func(format string, args ...any) {
		fatalCalled = true
		panic("audit-fatal-stub-should-not-fire")
	}

	// Empty registry: IsMultiTenant() == false.
	reg := tenant.NewRegistry()
	cfg := unwritableAuditPath(t)
	log := newTestLogger(t)

	// initAuditLog must NOT call auditFatalfFn in single-tenant mode.
	// It should return a discardAuditLog and a non-nil DispatchMiddleware.
	al, mw := initAuditLog(cfg, log, reg)

	if fatalCalled {
		t.Fatal("auditFatalfFn must NOT be called in single-tenant mode (EC-5)")
	}
	if al == nil {
		t.Fatal("initAuditLog returned nil AuditLog in single-tenant fallback path")
	}
	if mw == nil {
		t.Fatal("initAuditLog returned nil DispatchMiddleware in single-tenant fallback path")
	}
}

