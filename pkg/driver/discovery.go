package driver

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LoggerFunc is the optional structured-logger hook invoked by the discovery
// cache for operationally interesting events (e.g. a cached binary vanishing).
// Callers that own a project logger pass a closure such as
//
//	cache.SetLogger(func(format string, args ...any) {
//	    projectLogger.Warn(format, args...)
//	})
//
// to route discovery-cache events through the project's structured logger.
// When unset, the cache falls back to stdlib log.Printf so daemon/test code
// without a logger remains observable on stderr (per Gemini review feedback
// on PR #138 — keep the call site decoupled from a specific logger type).
type LoggerFunc func(format string, args ...any)

// DefaultDiscoveryCacheTTL is the default validity window for a cached
// discovery result. Beyond this, the entry is treated as stale even if the
// binary mtime is unchanged. Operator-tunable via
// config/default.yaml::driver.discovery_cache_ttl_seconds (AIMUX-16 CR-006).
const DefaultDiscoveryCacheTTL = 24 * time.Hour

// discoveryCacheEntry is one cached lookup result for a (profile.Name)
// discovery probe. The mtime of the resolved binary is recorded so that a
// modified binary triggers re-probe even before TTL expiry.
type discoveryCacheEntry struct {
	path     string    // resolved absolute path; empty when discovery returned no match
	mtime    time.Time // mtime of the binary at the time of caching (zero when path is empty)
	cachedAt time.Time // wall-clock time the entry was written
}

// DiscoveryCache stores per-profile binary lookup results, keyed by profile
// name. Lookups are validated against the cached file's current mtime and
// the entry's age before being served from the cache.
//
// Spec: AIMUX-16 CR-006 / FR-6 / EC-6.1..EC-6.4. In-memory only — daemon
// restart re-builds via warmup.
type DiscoveryCache struct {
	mu      sync.RWMutex
	entries map[string]discoveryCacheEntry
	ttl     time.Duration
	logger  LoggerFunc // optional; nil means fall back to stdlib log.Printf

	// Instrumentation: counts each invocation of the underlying probe path
	// (DiscoverBinary). Tests assert "no probe ran" by reading this counter
	// before and after a Lookup call. Atomic to keep Lookup itself safe to
	// call from goroutines without taking the cache mutex.
	probeCalls atomic.Int64
}

// NewDiscoveryCache returns a cache with the given TTL. A zero or negative
// ttl is replaced with DefaultDiscoveryCacheTTL so callers cannot accidentally
// disable freshness checks.
func NewDiscoveryCache(ttl time.Duration) *DiscoveryCache {
	if ttl <= 0 {
		ttl = DefaultDiscoveryCacheTTL
	}
	return &DiscoveryCache{
		entries: make(map[string]discoveryCacheEntry),
		ttl:     ttl,
	}
}

// SetLogger installs an optional structured-logger hook for cache-invalidation
// events. Pass nil to revert to the stdlib log.Printf fallback. Safe to call
// once during daemon startup, before Probe runs.
func (c *DiscoveryCache) SetLogger(fn LoggerFunc) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.logger = fn
	c.mu.Unlock()
}

// logf routes an invalidation event through the configured LoggerFunc when
// present, falling back to stdlib log.Printf. Centralised so the validity
// gate can stay terse.
func (c *DiscoveryCache) logf(format string, args ...any) {
	c.mu.RLock()
	fn := c.logger
	c.mu.RUnlock()
	if fn != nil {
		fn(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Lookup returns the cached resolved path for the given profile name when
// the cache entry is fresh AND the underlying file is unchanged. On a stale,
// missing or invalidated entry it falls back to a full DiscoverBinary scan
// and refreshes the cache from the new result.
//
// Invalidation triggers (per spec EC-6.1..EC-6.4):
//   - cache age ≥ ttl
//   - target binary mtime changed since cache write
//   - target binary file disappeared (stat ENOENT)
//   - target binary path is no longer a regular executable file
//
// A binary deletion observed via stat ENOENT also emits a WARN log line so
// operators can correlate transport-layer failures with disappearing CLIs.
func (c *DiscoveryCache) Lookup(profileName, binary string, searchPaths []string) string {
	if c == nil {
		return DiscoverBinary(binary, searchPaths)
	}

	c.mu.RLock()
	entry, ok := c.entries[profileName]
	ttl := c.ttl
	c.mu.RUnlock()

	if ok {
		if path, valid := c.validateCachedEntry(profileName, entry, ttl); valid {
			return path
		}
	}

	return c.scanAndStore(profileName, binary, searchPaths)
}

// validateCachedEntry returns the cached path and a "still valid" flag. A
// negative-result cache entry (path == "") is always treated as stale so a
// re-probe can pick up a freshly-installed binary.
//
// Validity gates (per spec EC-6.1..EC-6.4 + PR #138 review hardening):
//   - cache age ≥ ttl                                → invalid
//   - target binary missing (stat ENOENT)            → invalid + WARN log
//   - target is a directory or non-regular file      → invalid (race-safety)
//   - target lost its execute bit on POSIX           → invalid (immediate
//     re-probe so a chmod -x doesn't keep the entry "warm" until TTL)
//   - target mtime changed since cache write         → invalid
func (c *DiscoveryCache) validateCachedEntry(profileName string, entry discoveryCacheEntry, ttl time.Duration) (string, bool) {
	if entry.path == "" {
		return "", false
	}
	if time.Since(entry.cachedAt) >= ttl {
		return "", false
	}
	info, err := os.Stat(entry.path)
	if err != nil {
		if os.IsNotExist(err) {
			c.logf("driver/discovery: cached binary for %q vanished (path=%q) — invalidating cache",
				profileName, entry.path)
		}
		return "", false
	}
	if info.IsDir() {
		return "", false
	}
	// Reject non-regular files (e.g. a path replaced by a device/socket
	// between the original probe and now). Symlinks resolved via os.Stat
	// already follow to the regular target, so this rejects only genuinely
	// non-regular replacements.
	if !info.Mode().IsRegular() {
		return "", false
	}
	// On POSIX, lose-of-execute-bit must immediately invalidate the entry
	// rather than serving a stale path until TTL. Windows has no equivalent
	// permission model in this stat shape, so the check is skipped there
	// (binaryCandidates already restricts Windows to executable extensions).
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", false
	}
	if !info.ModTime().Equal(entry.mtime) {
		return "", false
	}
	return entry.path, true
}

// scanAndStore performs a full DiscoverBinary scan and writes the result
// (positive or negative) into the cache. The probeCalls counter is bumped
// once per invocation so tests can assert hit-vs-miss accounting.
//
// Caller MUST have already verified c is non-nil — the nil-cache branch lives
// in Lookup so the hot path here stays focused on persisting one result.
func (c *DiscoveryCache) scanAndStore(profileName, binary string, searchPaths []string) string {
	c.probeCalls.Add(1)

	resolved := DiscoverBinary(binary, searchPaths)

	now := time.Now()
	entry := discoveryCacheEntry{cachedAt: now}
	if resolved != "" {
		entry.path = resolved
		// Capture mtime of the resolved binary (following symlinks) so that
		// changes trigger re-probe per EC-6.3. os.Stat already follows
		// symlinks by default — an explicit EvalSymlinks call is redundant
		// and adds an extra syscall (Gemini review feedback on PR #138).
		if info, err := os.Stat(resolved); err == nil {
			entry.mtime = info.ModTime()
		}
	}

	c.mu.Lock()
	c.entries[profileName] = entry
	c.mu.Unlock()

	return resolved
}

// ProbeCalls returns the number of underlying DiscoverBinary scans the cache
// has performed since construction. Test-only — production code reads
// observability through the standard metrics channel.
func (c *DiscoveryCache) ProbeCalls() int64 {
	if c == nil {
		return 0
	}
	return c.probeCalls.Load()
}

// Invalidate removes the cached entry for one profile. Callers use this when
// they have out-of-band knowledge that a binary has changed (e.g. a hot
// reload event from the config layer).
func (c *DiscoveryCache) Invalidate(profileName string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.entries, profileName)
	c.mu.Unlock()
}

// DiscoverBinary searches for a CLI binary beyond just PATH.
// Search order: PATH → well-known dirs → version manager dirs → profile search_paths.
// Returns the full path if found, or empty string if not.
func DiscoverBinary(name string, profileSearchPaths []string) string {
	// Level 0: standard PATH lookup (fastest)
	if path, err := exec.LookPath(name); err == nil {
		return path
	}

	// Level 1: well-known installation directories per platform
	for _, dir := range wellKnownDirs() {
		if p := probeInDir(dir, name); p != "" {
			return p
		}
	}

	// Level 2: version manager directories (glob patterns)
	for _, pattern := range versionManagerGlobs() {
		expanded := os.ExpandEnv(pattern)
		matches, err := filepath.Glob(expanded)
		if err != nil {
			continue
		}
		for _, dir := range matches {
			if p := probeInDir(dir, name); p != "" {
				return p
			}
		}
	}

	// Level 3: profile-specific search paths from YAML config
	for _, pattern := range profileSearchPaths {
		expanded := os.ExpandEnv(pattern)
		matches, err := filepath.Glob(expanded)
		if err != nil {
			// Not a glob — try as literal directory
			if p := probeInDir(expanded, name); p != "" {
				return p
			}
			continue
		}
		for _, dir := range matches {
			if p := probeInDir(dir, name); p != "" {
				return p
			}
		}
	}

	return ""
}

// probeInDir checks if a binary exists in the given directory.
func probeInDir(dir, name string) string {
	candidates := binaryCandidates(name)
	for _, candidate := range candidates {
		path := filepath.Join(dir, candidate)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		// On Unix, check executable bit
		if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
			continue
		}
		return path
	}
	return ""
}

// binaryCandidates returns possible filenames for a binary name.
// On Windows, appends common extensions (.exe, .cmd, .bat).
func binaryCandidates(name string) []string {
	if runtime.GOOS == "windows" {
		// If name already has extension, use as-is too
		if strings.Contains(name, ".") {
			return []string{name}
		}
		return []string{name + ".exe", name + ".cmd", name + ".bat", name + ".ps1", name}
	}
	return []string{name}
}

// wellKnownDirs returns platform-specific directories where package managers install binaries.
func wellKnownDirs() []string {
	home := homeDir()
	var dirs []string

	// Cross-platform
	dirs = append(dirs,
		filepath.Join(home, ".local", "bin"),        // pip --user, pipx
		filepath.Join(home, ".cargo", "bin"),         // cargo install
		filepath.Join(home, "go", "bin"),             // go install
		filepath.Join(home, ".deno", "bin"),          // deno install
	)

	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		localAppdata := os.Getenv("LOCALAPPDATA")
		if appdata != "" {
			dirs = append(dirs, filepath.Join(appdata, "npm"))              // npm -g on Windows
			dirs = append(dirs, filepath.Join(appdata, "Python", "Scripts")) // pip on Windows
		}
		if localAppdata != "" {
			dirs = append(dirs, filepath.Join(localAppdata, "Programs"))
		}
		// GOPATH/bin on Windows
		if gopath := os.Getenv("GOPATH"); gopath != "" {
			dirs = append(dirs, filepath.Join(gopath, "bin"))
		}

	case "darwin":
		dirs = append(dirs,
			"/opt/homebrew/bin",   // Homebrew Apple Silicon
			"/usr/local/bin",      // Homebrew Intel + manual installs
		)

	case "linux":
		dirs = append(dirs,
			"/usr/local/bin",                                // manual installs, go tarball
			"/snap/bin",                                     // snap packages
			filepath.Join(home, ".nix-profile", "bin"),      // nix
			"/home/linuxbrew/.linuxbrew/bin",                // linuxbrew
		)
	}

	return dirs
}

// versionManagerGlobs returns glob patterns for version manager binary directories.
func versionManagerGlobs() []string {
	home := homeDir()

	return []string{
		// Node version managers
		filepath.Join(home, ".nvm", "versions", "node", "*", "bin"),
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".fnm", "node-versions", "*", "installation", "bin"),

		// Python version managers
		filepath.Join(home, ".pyenv", "shims"),
		filepath.Join(home, ".pyenv", "versions", "*", "bin"),

		// Multi-language version managers
		filepath.Join(home, ".local", "share", "mise", "installs", "*", "*", "bin"),
		filepath.Join(home, ".asdf", "shims"),
	}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}
