package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

var (
	daemonUUIDOnce sync.Once
	daemonUUID     string
)

// GetDaemonUUID returns the daemon-lifetime UUID for this process.
// The UUID is generated once on first call (16 bytes of crypto/rand → 32-char hex).
// Subsequent calls within the same process return the same value.
//
// The UUID is in-memory (not persisted). A new UUID is generated on every daemon
// restart, which is intentional: any session/job rows with a different daemon UUID
// are candidates for abort reconciliation on startup.
func GetDaemonUUID() string {
	daemonUUIDOnce.Do(func() {
		daemonUUID = generateUUID()
	})
	return daemonUUID
}

// generateUUID produces a 32-character hex string from 16 crypto/rand bytes.
// Panics if the OS cannot provide random bytes — if the OS entropy pool is
// broken we must not start, since we cannot guarantee uniqueness.
func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("session: daemon UUID generation failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ResetDaemonUUID forces regeneration of the daemon UUID on the next call to
// GetDaemonUUID. This function exists for testing only — it must not be called
// in production code.
func ResetDaemonUUID() {
	daemonUUIDOnce = sync.Once{}
	daemonUUID = ""
}
