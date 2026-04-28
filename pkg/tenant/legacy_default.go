package tenant

import (
	"os"
	"os/user"
	"runtime"
	"strconv"
)

// LegacyDefaultSnapshot returns a Snapshot containing a single entry for the
// LegacyDefault tenant. It is used when tenants.yaml is absent, meaning the
// daemon runs in single-tenant legacy mode.
//
// The entry is mapped to the current OS UID on Unix platforms. On Windows,
// uid() returns 0 because Windows does not expose POSIX UIDs; the caller should
// treat uid=0 as the legacy-default identity (FR-14 fallback).
//
// Design note: presence of tenants.yaml is the HARD branch indicator:
//   - tenants.yaml present → multi-tenant mode; LoadFromFile drives the snapshot.
//   - tenants.yaml absent  → legacy-default mode; this function drives the snapshot.
//
// No tenant infrastructure (drain controller, hot-reload) activates in legacy mode.
func LegacyDefaultSnapshot() *Snapshot {
	uid := currentUID()
	cfg := TenantConfig{
		Name: LegacyDefault,
		UID:  uid,
		Role: RoleOperator, // legacy-default has operator privileges
	}.WithDefaults()

	// Build the map with uid as key so Resolve(currentUID()) returns the config.
	// On Windows uid=0; callers must handle the platform-specific fallback.
	entries := map[int]TenantConfig{uid: cfg}
	return NewSnapshot(entries)
}

// currentUID returns the OS UID of the current process.
//
// On Windows, POSIX UIDs are not available; returns 0 as the legacy-default
// fallback per FR-14. A startup warning is logged by the daemon startup path.
func currentUID() int {
	if runtime.GOOS == "windows" {
		// Windows does not expose POSIX UIDs. Return 0 as the sentinel for
		// legacy-default Windows mode. Phase 8 (muxcore #110+#111) will handle
		// Windows auth via an alternative mechanism.
		return 0
	}
	// Use os.Getuid() directly — fast, no allocation, no error path.
	// Falls back to user.Current() only when os.Getuid() returns -1 (rare,
	// indicates the process was started with a synthetic UID in some container envs).
	uid := os.Getuid()
	if uid >= 0 {
		return uid
	}
	// Fallback: parse from user.Current() for container environments.
	u, err := user.Current()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0
	}
	return n
}
