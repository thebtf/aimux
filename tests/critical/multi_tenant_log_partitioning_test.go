//go:build !short

package critical_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/logger"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// newCriticalFallback creates a lumberjack.Logger whose backing file is a
// temp file the test owns. Auto-closed via t.Cleanup so Windows file locks
// release before TempDir RemoveAll runs.
func newCriticalFallback(t *testing.T) (*lumberjack.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fallback.log")
	lj := &lumberjack.Logger{Filename: path, MaxSize: 10}
	t.Cleanup(func() { _ = lj.Close() })
	return lj, path
}

// readCriticalFile reads the content of a file. Returns "" when the file
// does not exist — that is a valid signal in some assertions.
func readCriticalFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("readCriticalFile %s: %v", path, err)
	}
	return string(data)
}

// TestCritical_LogPartitioning_TenantsAreIsolated verifies the FR-12/FR-14
// log-partitioning contract: writes for tenantA land in <baseDir>/tenantA.log
// and never appear in tenantB.log; tenantB's writes are likewise isolated.
//
// @critical — release blocker per rule #10
func TestCritical_LogPartitioning_TenantsAreIsolated(t *testing.T) {
	baseDir := t.TempDir()
	fallback, fallbackPath := newCriticalFallback(t)

	p := logger.NewLogPartitioner(baseDir, nil, fallback)
	t.Cleanup(func() { _ = p.Close() })

	if _, err := p.WriteFor("tenantA", []byte("entry-for-A\n")); err != nil {
		t.Fatalf("WriteFor(tenantA): %v", err)
	}
	if _, err := p.WriteFor("tenantB", []byte("entry-for-B\n")); err != nil {
		t.Fatalf("WriteFor(tenantB): %v", err)
	}

	// Boundary 1: tenantA's file exists at the expected path.
	fileA := filepath.Join(baseDir, "tenantA.log")
	contentA := readCriticalFile(t, fileA)
	if contentA == "" {
		t.Fatalf("CRITICAL: tenantA.log missing or empty at %s", fileA)
	}
	if !strings.Contains(contentA, "entry-for-A") {
		t.Errorf("CRITICAL: tenantA.log lacks own entry; got %q", contentA)
	}
	if strings.Contains(contentA, "entry-for-B") {
		t.Errorf("CRITICAL: tenantA.log leaked tenantB data: %q", contentA)
	}

	// Boundary 2: tenantB's file is symmetric.
	fileB := filepath.Join(baseDir, "tenantB.log")
	contentB := readCriticalFile(t, fileB)
	if !strings.Contains(contentB, "entry-for-B") {
		t.Errorf("CRITICAL: tenantB.log lacks own entry; got %q", contentB)
	}
	if strings.Contains(contentB, "entry-for-A") {
		t.Errorf("CRITICAL: tenantB.log leaked tenantA data: %q", contentB)
	}

	// Boundary 3: the fallback log is NOT used for valid tenant IDs.
	fbContent := readCriticalFile(t, fallbackPath)
	if strings.Contains(fbContent, "entry-for-A") || strings.Contains(fbContent, "entry-for-B") {
		t.Errorf("CRITICAL: fallback.log absorbed tenant traffic: %q", fbContent)
	}
}

// TestCritical_LogPartitioning_PathTraversalRoutesToFallback verifies that
// a hostile tenant ID (e.g. "../etc/passwd") is rejected by sanitizeTenantID
// and routed to the fallback file — never resolved against the real
// filesystem path it tries to escape to.
//
// @critical — release blocker per rule #10
func TestCritical_LogPartitioning_PathTraversalRoutesToFallback(t *testing.T) {
	baseDir := t.TempDir()
	fallback, fallbackPath := newCriticalFallback(t)

	p := logger.NewLogPartitioner(baseDir, nil, fallback)
	t.Cleanup(func() { _ = p.Close() })

	// Common path-traversal payloads — every one MUST route to the fallback.
	hostile := []string{
		"../etc/passwd",
		"..\\windows\\system32",
		"..",
		"./hidden",
		".hidden",
		"good/bad",
		"good\\bad",
		"with\x00null",
	}
	for _, id := range hostile {
		if _, err := p.WriteFor(id, []byte("ATTACK:"+id+"\n")); err != nil {
			t.Fatalf("WriteFor(%q): %v", id, err)
		}
	}

	// Boundary 1: NO file landed outside baseDir or under a traversed path.
	// Walk baseDir and verify every file produced is directly inside it
	// (no nested directory escape).
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("ReadDir(baseDir): %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("CRITICAL: path traversal created nested directory %q under baseDir", e.Name())
		}
		// Reject any file whose name contains traversal markers — none of the
		// hostile inputs should have produced a file at all (they go to
		// fallback), but defend against silent acceptance.
		if strings.Contains(e.Name(), "..") || strings.Contains(e.Name(), string(filepath.Separator)) {
			t.Errorf("CRITICAL: path-traversal payload produced file %q under baseDir", e.Name())
		}
	}

	// Boundary 2: traversal attempt files are NOT created at the targets.
	if _, err := os.Stat(filepath.Join(baseDir, "..", "etc", "passwd")); err == nil {
		t.Fatalf("CRITICAL: ../etc/passwd was created — sanitizeTenantID failed")
	}

	// Boundary 3: the fallback log absorbed every hostile payload — proves
	// that sanitizeTenantID rejected the IDs but the bytes were not silently
	// dropped (audit-trail preservation).
	fbContent := readCriticalFile(t, fallbackPath)
	for _, id := range hostile {
		if !strings.Contains(fbContent, "ATTACK:"+id) {
			t.Errorf("CRITICAL: fallback.log missing rejected payload %q — possible silent drop; got %q",
				id, fbContent)
		}
	}
}
