package session_test

import (
	"encoding/hex"
	"testing"

	"github.com/thebtf/aimux/pkg/session"
)

// TestGetDaemonUUID_StableWithinProcess verifies that two calls to GetDaemonUUID
// within the same process return the same value.
func TestGetDaemonUUID_StableWithinProcess(t *testing.T) {
	session.ResetDaemonUUID()

	first := session.GetDaemonUUID()
	second := session.GetDaemonUUID()

	if first != second {
		t.Errorf("GetDaemonUUID() not stable: first=%q second=%q", first, second)
	}
}

// TestGetDaemonUUID_NonEmpty verifies the UUID is a non-empty hex string.
func TestGetDaemonUUID_NonEmpty(t *testing.T) {
	session.ResetDaemonUUID()

	id := session.GetDaemonUUID()
	if id == "" {
		t.Fatal("GetDaemonUUID() returned empty string")
	}

	// Must be valid hex (32 chars = 16 bytes).
	if len(id) != 32 {
		t.Errorf("GetDaemonUUID() length = %d, want 32", len(id))
	}
	b, err := hex.DecodeString(id)
	if err != nil {
		t.Errorf("GetDaemonUUID() = %q is not valid hex: %v", id, err)
	}
	if len(b) != 16 {
		t.Errorf("decoded UUID bytes = %d, want 16", len(b))
	}
}

// TestResetDaemonUUID_ProducesDifferentValue verifies that after ResetDaemonUUID,
// the next call to GetDaemonUUID generates a new (different) UUID.
func TestResetDaemonUUID_ProducesDifferentValue(t *testing.T) {
	session.ResetDaemonUUID()
	first := session.GetDaemonUUID()

	session.ResetDaemonUUID()
	second := session.GetDaemonUUID()

	if first == second {
		// Astronomically unlikely with 16 bytes of crypto/rand, but not impossible.
		// If this fires it almost certainly indicates ResetDaemonUUID is broken.
		t.Errorf("ResetDaemonUUID: two UUIDs are equal (%q) — reset likely did not work", first)
	}
}
