package driver

// Internal whitebox tests for DiscoveryCache (AIMUX-16 CR-006).
// Lives in package driver so tests can read the unexported probeCalls
// counter via the exported ProbeCalls accessor without crossing the
// package boundary, and so symlink fixtures can be wired alongside the
// existing discovery test helpers.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/config"
)

// writeFakeBinary writes an executable file at <dir>/<name> (with .exe on
// Windows) and returns its absolute path. Mirrors the helper pattern from
// discovery_test.go without exporting it.
func writeFakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	bin := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return bin
}

// TestDiscoveryCache_FreshMissTriggersScan asserts that a never-seen profile
// causes exactly one DiscoverBinary scan on the first lookup.
func TestDiscoveryCache_FreshMissTriggersScan(t *testing.T) {
	dir := t.TempDir()
	writeFakeBinary(t, dir, "mycli")

	cache := NewDiscoveryCache(time.Hour)

	got := cache.Lookup("mycli", "mycli", []string{dir})
	if got == "" {
		t.Fatalf("first lookup returned empty path")
	}
	if calls := cache.ProbeCalls(); calls != 1 {
		t.Errorf("first lookup probe calls = %d, want 1", calls)
	}
}

// TestDiscoveryCache_HitWithinTTLSkipsScan exercises the canonical fast path:
// a fresh entry within TTL with unchanged mtime returns from cache without
// invoking DiscoverBinary again.
func TestDiscoveryCache_HitWithinTTLSkipsScan(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeBinary(t, dir, "mycli")

	cache := NewDiscoveryCache(time.Hour)

	// Prime the cache.
	if got := cache.Lookup("mycli", "mycli", []string{dir}); got != bin {
		t.Fatalf("prime lookup = %q, want %q", got, bin)
	}
	if calls := cache.ProbeCalls(); calls != 1 {
		t.Fatalf("after prime probe calls = %d, want 1", calls)
	}

	// Second lookup — must serve from cache.
	if got := cache.Lookup("mycli", "mycli", []string{dir}); got != bin {
		t.Errorf("warm lookup = %q, want %q", got, bin)
	}
	if calls := cache.ProbeCalls(); calls != 1 {
		t.Errorf("warm lookup bumped probe calls to %d, want 1 (cache hit expected)", calls)
	}
}

// TestDiscoveryCache_MTimeChangeInvalidates verifies that a binary mtime
// change short-circuits TTL — even with a long TTL, the cache re-probes.
func TestDiscoveryCache_MTimeChangeInvalidates(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeBinary(t, dir, "mycli")

	cache := NewDiscoveryCache(24 * time.Hour)

	if got := cache.Lookup("mycli", "mycli", []string{dir}); got != bin {
		t.Fatalf("prime lookup = %q, want %q", got, bin)
	}
	beforeProbes := cache.ProbeCalls()

	// Bump mtime forward so it differs from the cached value. os.Chtimes
	// works cross-platform and is more reliable than rewriting the file
	// (some filesystems have low mtime granularity).
	future := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(bin, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if got := cache.Lookup("mycli", "mycli", []string{dir}); got != bin {
		t.Errorf("post-mtime lookup = %q, want %q", got, bin)
	}
	if calls := cache.ProbeCalls(); calls != beforeProbes+1 {
		t.Errorf("mtime change probe calls = %d, want %d (one re-probe)", calls, beforeProbes+1)
	}
}

// TestDiscoveryCache_BinaryDeletionInvalidates verifies EC-6.1: a cached
// binary that disappears causes a cache miss on the next lookup. The WARN
// log line is emitted via stdlib log.Printf — we don't capture it here
// because the cache contract is the observable behaviour (re-probe runs).
func TestDiscoveryCache_BinaryDeletionInvalidates(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeBinary(t, dir, "mycli")

	cache := NewDiscoveryCache(24 * time.Hour)

	if got := cache.Lookup("mycli", "mycli", []string{dir}); got != bin {
		t.Fatalf("prime lookup = %q, want %q", got, bin)
	}
	beforeProbes := cache.ProbeCalls()

	if err := os.Remove(bin); err != nil {
		t.Fatalf("remove binary: %v", err)
	}

	got := cache.Lookup("mycli", "mycli", []string{dir})
	if got != "" {
		t.Errorf("post-deletion lookup = %q, want empty (binary gone)", got)
	}
	if calls := cache.ProbeCalls(); calls != beforeProbes+1 {
		t.Errorf("deletion probe calls = %d, want %d (one re-probe)", calls, beforeProbes+1)
	}
}

// TestDiscoveryCache_TTLExpiry verifies that a successful entry past its
// TTL re-probes even when the binary is unchanged.
func TestDiscoveryCache_TTLExpiry(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeBinary(t, dir, "mycli")

	// Tiny TTL so the test does not need to sleep for hours.
	cache := NewDiscoveryCache(20 * time.Millisecond)

	if got := cache.Lookup("mycli", "mycli", []string{dir}); got != bin {
		t.Fatalf("prime lookup = %q, want %q", got, bin)
	}
	beforeProbes := cache.ProbeCalls()

	// Wait past TTL.
	time.Sleep(40 * time.Millisecond)

	if got := cache.Lookup("mycli", "mycli", []string{dir}); got != bin {
		t.Errorf("post-TTL lookup = %q, want %q", got, bin)
	}
	if calls := cache.ProbeCalls(); calls != beforeProbes+1 {
		t.Errorf("TTL expiry probe calls = %d, want %d", calls, beforeProbes+1)
	}
}

// TestDiscoveryCache_SymlinkResolvesToTarget verifies EC-6.3: a symlink
// stat tracks the target, so changing the target's mtime invalidates the
// cache. Symlink creation requires Developer Mode or admin privileges on
// Windows; the test skips on Windows when symlink creation fails.
func TestDiscoveryCache_SymlinkResolvesToTarget(t *testing.T) {
	dir := t.TempDir()
	target := writeFakeBinary(t, dir, "mycli-real")

	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "mycli")
	if runtime.GOOS == "windows" {
		link += ".exe"
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported here: %v", err)
	}

	cache := NewDiscoveryCache(24 * time.Hour)
	if got := cache.Lookup("mycli", "mycli", []string{linkDir}); got != link {
		t.Fatalf("prime lookup = %q, want symlink %q", got, link)
	}
	beforeProbes := cache.ProbeCalls()

	// Bump the TARGET's mtime, not the symlink. With proper EvalSymlinks
	// resolution the cache must invalidate.
	future := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatalf("chtimes target: %v", err)
	}

	if got := cache.Lookup("mycli", "mycli", []string{linkDir}); got != link {
		t.Errorf("post-target-mtime lookup = %q, want %q", got, link)
	}
	if calls := cache.ProbeCalls(); calls != beforeProbes+1 {
		t.Errorf("target mtime change probe calls = %d, want %d (re-probe expected via target stat)",
			calls, beforeProbes+1)
	}
}

// TestDiscoveryCache_NegativeResultIsAlwaysReprobed asserts that a not-found
// outcome is never served from cache — a freshly installed binary must be
// picked up on the next lookup without waiting for TTL expiry.
func TestDiscoveryCache_NegativeResultIsAlwaysReprobed(t *testing.T) {
	dir := t.TempDir()

	cache := NewDiscoveryCache(24 * time.Hour)

	// First lookup: no binary present → empty path stored.
	if got := cache.Lookup("mycli", "mycli-nope", []string{dir}); got != "" {
		t.Fatalf("missing-binary lookup = %q, want empty", got)
	}
	beforeProbes := cache.ProbeCalls()

	// Install the binary now and look up again — must re-probe and succeed.
	bin := writeFakeBinary(t, dir, "mycli-nope")
	if got := cache.Lookup("mycli", "mycli-nope", []string{dir}); got != bin {
		t.Errorf("post-install lookup = %q, want %q", got, bin)
	}
	if calls := cache.ProbeCalls(); calls != beforeProbes+1 {
		t.Errorf("post-install probe calls = %d, want %d", calls, beforeProbes+1)
	}
}

// TestRegistry_ProbeUsesCache exercises the integration path: Registry.Probe
// runs the cached lookup, so two consecutive Probes do at most one underlying
// scan per profile.
func TestRegistry_ProbeUsesCache(t *testing.T) {
	dir := t.TempDir()
	writeFakeBinary(t, dir, "mycli")

	profiles := map[string]*config.CLIProfile{
		"mycli": {Name: "mycli", Binary: "mycli", SearchPaths: []string{dir}},
	}
	reg := NewRegistry(profiles)

	reg.Probe()
	first := reg.DiscoveryCache().ProbeCalls()
	if first != 1 {
		t.Fatalf("after first Probe calls = %d, want 1", first)
	}

	reg.Probe()
	second := reg.DiscoveryCache().ProbeCalls()
	if second != first {
		t.Errorf("after second Probe calls = %d, want %d (warm cache, no rescan)", second, first)
	}
}

// TestRegistry_ProbeWarmCacheUnder1ms verifies the FR-6 / spec acceptance
// signal: warm cache with 12 unchanged binaries completes Probe in under
// 1ms total. Acts as a regression guard against accidental re-introduction
// of synchronous full scans on the hot path.
func TestRegistry_ProbeWarmCacheUnder1ms(t *testing.T) {
	dir := t.TempDir()

	// Build a 12-profile registry mirroring the spec scope (3 active CLIs +
	// 9 deferred). Each profile points at a unique fake binary so the cache
	// has 12 distinct positive entries to validate per Probe.
	const profileCount = 12
	profiles := make(map[string]*config.CLIProfile, profileCount)
	for i := 0; i < profileCount; i++ {
		name := "cli" + string(rune('a'+i))
		writeFakeBinary(t, dir, name)
		profiles[name] = &config.CLIProfile{
			Name:        name,
			Binary:      name,
			SearchPaths: []string{dir},
		}
	}
	reg := NewRegistry(profiles)

	// Warm the cache with one full Probe so the second one hits exclusively.
	reg.Probe()
	beforeProbes := reg.DiscoveryCache().ProbeCalls()

	start := time.Now()
	reg.Probe()
	elapsed := time.Since(start)

	if afterProbes := reg.DiscoveryCache().ProbeCalls(); afterProbes != beforeProbes {
		t.Fatalf("warm Probe ran %d underlying scans, want 0 (fully cached)",
			afterProbes-beforeProbes)
	}

	// Surface the timing for PR-evidence even when the test passes.
	t.Logf("warm Probe (%d profiles): %v", profileCount, elapsed)

	if elapsed > time.Millisecond {
		t.Errorf("warm Probe took %v, want ≤ 1ms (FR-6 perf budget)", elapsed)
	}
}
