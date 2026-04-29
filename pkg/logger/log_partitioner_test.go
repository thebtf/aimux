// Package logger: T040 — LogPartitioner tests (AIMUX-12 Phase 6).
// RED gate: all tests fail before log_partitioner.go is created.
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// newTestFallback creates a lumberjack.Logger writing to a temp file for use as
// the LogPartitioner fallback. Auto-closes via t.Cleanup so Windows file locks
// release before TempDir RemoveAll runs.
func newTestFallback(t *testing.T) (*lumberjack.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fallback.log")
	lj := &lumberjack.Logger{Filename: path, MaxSize: 10}
	t.Cleanup(func() { _ = lj.Close() })
	return lj, path
}

// closeOnCleanup registers a t.Cleanup that closes the partitioner — releases
// all per-tenant lumberjack file handles before TempDir RemoveAll runs.
func closeOnCleanup(t *testing.T, p *LogPartitioner) {
	t.Helper()
	t.Cleanup(func() { _ = p.Close() })
}

// readFile reads the entire content of a file, returning "" on not-found.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("readFile %s: %v", path, err)
	}
	return string(data)
}

// TestLogPartitioner_WritesToTenantFile — writing twice for the same tenantID
// lands both lines in the same file, keyed as <baseDir>/<tenantID>.log.
func TestLogPartitioner_WritesToTenantFile(t *testing.T) {
	baseDir := t.TempDir()
	fallback, _ := newTestFallback(t)

	p := NewLogPartitioner(baseDir, nil, fallback)
	closeOnCleanup(t, p)

	line1 := []byte("first line\n")
	line2 := []byte("second line\n")

	n1, err := p.WriteFor("acme", line1)
	if err != nil {
		t.Fatalf("WriteFor acme line1: %v", err)
	}
	if n1 != len(line1) {
		t.Fatalf("WriteFor line1: wrote %d bytes, want %d", n1, len(line1))
	}

	n2, err := p.WriteFor("acme", line2)
	if err != nil {
		t.Fatalf("WriteFor acme line2: %v", err)
	}
	if n2 != len(line2) {
		t.Fatalf("WriteFor line2: wrote %d bytes, want %d", n2, len(line2))
	}

	tenantFile := filepath.Join(baseDir, "acme.log")
	content := readFile(t, tenantFile)
	if !strings.Contains(content, "first line") {
		t.Errorf("expected 'first line' in %s; got:\n%s", tenantFile, content)
	}
	if !strings.Contains(content, "second line") {
		t.Errorf("expected 'second line' in %s; got:\n%s", tenantFile, content)
	}
}

// TestLogPartitioner_DifferentTenantsIsolated — writes to tenantA must NOT appear
// in tenantB.log and vice versa.
func TestLogPartitioner_DifferentTenantsIsolated(t *testing.T) {
	baseDir := t.TempDir()
	fallback, _ := newTestFallback(t)

	p := NewLogPartitioner(baseDir, nil, fallback)
	closeOnCleanup(t, p)

	_, err := p.WriteFor("tenantA", []byte("msg-for-A\n"))
	if err != nil {
		t.Fatalf("WriteFor tenantA: %v", err)
	}
	_, err = p.WriteFor("tenantB", []byte("msg-for-B\n"))
	if err != nil {
		t.Fatalf("WriteFor tenantB: %v", err)
	}

	fileA := filepath.Join(baseDir, "tenantA.log")
	fileB := filepath.Join(baseDir, "tenantB.log")

	contentA := readFile(t, fileA)
	contentB := readFile(t, fileB)

	if strings.Contains(contentA, "msg-for-B") {
		t.Errorf("tenantA.log must not contain tenantB data; got:\n%s", contentA)
	}
	if strings.Contains(contentB, "msg-for-A") {
		t.Errorf("tenantB.log must not contain tenantA data; got:\n%s", contentB)
	}
	if !strings.Contains(contentA, "msg-for-A") {
		t.Errorf("tenantA.log missing 'msg-for-A'; got:\n%s", contentA)
	}
	if !strings.Contains(contentB, "msg-for-B") {
		t.Errorf("tenantB.log missing 'msg-for-B'; got:\n%s", contentB)
	}
}

// TestLogPartitioner_LegacyFallback — empty tenantID routes to the fallback
// lumberjack logger, NOT to a file named ".log" in baseDir.
func TestLogPartitioner_LegacyFallback(t *testing.T) {
	baseDir := t.TempDir()
	fallback, fallbackPath := newTestFallback(t)

	p := NewLogPartitioner(baseDir, nil, fallback)
	closeOnCleanup(t, p)

	msg := []byte("legacy fallback message\n")
	_, err := p.WriteFor("", msg)
	if err != nil {
		t.Fatalf("WriteFor empty tenantID: %v", err)
	}

	// The named ".log" file in baseDir must NOT be created.
	dotLog := filepath.Join(baseDir, ".log")
	if _, statErr := os.Stat(dotLog); statErr == nil {
		t.Errorf("expected no file at %s; file was created", dotLog)
	}

	// The fallback file MUST contain the message.
	content := readFile(t, fallbackPath)
	if !strings.Contains(content, "legacy fallback message") {
		t.Errorf("expected 'legacy fallback message' in fallback file %s; got:\n%s", fallbackPath, content)
	}
}

// TestLogPartitioner_PathTraversalRejected — tenantIDs containing path traversal
// sequences are sanitized so they cannot escape baseDir.
//
// Per spec: "..", "/", "\", null bytes, and leading dots are rejected/sanitized.
// WriteFor with a dangerous tenantID must either (a) return an error, OR
// (b) sanitize to a safe name and write to fallback. The file ../../etc/passwd
// must never be created outside baseDir.
func TestLogPartitioner_PathTraversalRejected(t *testing.T) {
	baseDir := t.TempDir()
	fallback, _ := newTestFallback(t)

	p := NewLogPartitioner(baseDir, nil, fallback)
	closeOnCleanup(t, p)

	dangerousIDs := []string{
		"../../etc/passwd",
		"../sibling",
		"/etc/shadow",
		"C:\\Windows\\System32\\evil",
		".hidden",
		"\x00null",
		"foo/bar",
		"foo\\bar",
	}

	for _, id := range dangerousIDs {
		t.Run(fmt.Sprintf("id=%q", id), func(t *testing.T) {
			_, writeErr := p.WriteFor(id, []byte("traversal attempt\n"))
			if writeErr != nil {
				return // safe rejection
			}
			// Production routes dangerous IDs to fallback. Verify ZERO files
			// were created INSIDE baseDir for these dangerous IDs (fallback
			// lives outside baseDir per newTestFallback). Listing baseDir
			// должен returnить пустоту.
			entries, err := os.ReadDir(baseDir)
			if err != nil {
				t.Fatalf("read baseDir: %v", err)
			}
			if len(entries) > 0 {
				t.Errorf("path traversal: dangerous id %q created file(s) in baseDir: %v", id, entries)
			}
		})
	}
}

// TestLogPartitioner_ConcurrentWrites — race-detector clean: multiple goroutines
// write to the same and different tenant files concurrently.
func TestLogPartitioner_ConcurrentWrites(t *testing.T) {
	baseDir := t.TempDir()
	fallback, _ := newTestFallback(t)

	p := NewLogPartitioner(baseDir, nil, fallback)
	closeOnCleanup(t, p)

	const goroutines = 20
	const writesPerGoroutine = 50
	tenants := []string{"alpha", "beta", "gamma"}

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*writesPerGoroutine)

	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			tenant := tenants[i%len(tenants)]
			for j := 0; j < writesPerGoroutine; j++ {
				msg := []byte(fmt.Sprintf("goroutine %d write %d\n", i, j))
				if _, err := p.WriteFor(tenant, msg); err != nil {
					errs <- fmt.Errorf("goroutine %d: WriteFor(%s): %w", i, tenant, err)
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent write error: %v", err)
	}

	// Sanity: each tenant file must exist and be non-empty.
	for _, tenant := range tenants {
		path := filepath.Join(baseDir, tenant+".log")
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected file %s to exist: %v", path, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("expected non-empty file %s", path)
		}
	}
}

// TestLogPartitioner_FileMode verifies log files are created with mode 0600 (NFR-11).
// Skipped on Windows where Unix file modes are not enforced.
func TestLogPartitioner_FileMode(t *testing.T) {
	// FR-14: file mode enforcement is Unix-only.
	if !isUnixOS() {
		t.Skip("file mode 0600 enforcement skipped on non-Unix OS (FR-14)")
	}

	baseDir := t.TempDir()
	fallback, _ := newTestFallback(t)
	p := NewLogPartitioner(baseDir, nil, fallback)
	closeOnCleanup(t, p)

	_, err := p.WriteFor("modetest", []byte("check mode\n"))
	if err != nil {
		t.Fatalf("WriteFor: %v", err)
	}

	path := filepath.Join(baseDir, "modetest.log")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	mode := info.Mode() & 0o777
	if mode != 0o600 {
		t.Errorf("file mode = %04o, want 0600", mode)
	}
}

// TestLogPartitioner_SanitizeTenantID verifies that sanitizeTenantID produces
// expected output for a range of inputs.
func TestLogPartitioner_SanitizeTenantID(t *testing.T) {
	cases := []struct {
		input string
		valid bool   // true if the sanitized ID should be used, false if fallback expected
		want  string // expected safe name (only checked when valid=true)
	}{
		// Valid ASCII allowlist cases.
		{"acme", true, "acme"},
		{"tenant-123", true, "tenant-123"},
		{"tenant_123", true, "tenant_123"},
		{"ABC", true, "ABC"},
		{"a", true, "a"},

		// Pre-existing path-traversal rejections.
		{"", false, ""},
		{"../../etc", false, ""},
		{"../parent", false, ""},
		{"/absolute", false, ""},
		{".hidden", false, ""},
		{"\x00null", false, ""},
		{"foo/bar", false, ""},
		{"foo\\bar", false, ""},

		// W1 — Unicode visual-spoof rejections (AIMUX-12 v5.1.0).
		// Cyrillic 'а' (U+0430) renders identically to Latin 'a' in operator
		// audit logs, but produces a distinct file. Reject the entire ID.
		{"аcme", false, ""}, // "аcme" — Cyrillic small letter a
		// RTL override (U+202E) — reverses display direction; visually
		// indistinguishable from "acme" in some terminals.
		{"acme‮", false, ""},
		// Zero-width joiner (U+200D) — invisible glyph that creates a
		// distinct ID identical to "acme" on screen.
		{"acme‍", false, ""},
		// Space and punctuation outside the allowlist.
		{"foo bar", false, ""},
		{"foo!bar", false, ""},
		{"foo.bar", false, ""}, // dot inside is excluded by allowlist
		{"foo@bar", false, ""},
		// NFC-denormalized form: 'é' as 'e' + combining acute (U+0301).
		// IsNormalString returns false → reject.
		{"éclair", false, ""},
		// NFC-normalized non-ASCII: composed acute (U+00E9) passes
		// IsNormalString but fails the strict ASCII allowlist.
		{"éclair", false, ""},
		// Tab (control character) — not in allowlist.
		{"foo\tbar", false, ""},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("input=%q", tc.input), func(t *testing.T) {
			safe, ok := sanitizeTenantID(tc.input)
			if ok != tc.valid {
				t.Fatalf("sanitizeTenantID(%q): ok=%v, want %v", tc.input, ok, tc.valid)
			}
			if tc.valid && safe != tc.want {
				t.Fatalf("sanitizeTenantID(%q) = %q, want %q", tc.input, safe, tc.want)
			}
		})
	}
}

// isUnixOS is defined in platform_unix.go / platform_windows.go via build tags.
