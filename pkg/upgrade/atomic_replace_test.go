package upgrade_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/upgrade"
)

// TestAtomicReplaceBinary_HappyPath verifies that a successful replace leaves
// the new content at currentPath and preserves the original at currentPath+".old".
func TestAtomicReplaceBinary_HappyPath(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-new.exe")

	originalContent := []byte("original-binary-v1")
	newContent := []byte("new-binary-v2")

	if err := os.WriteFile(currentPath, originalContent, 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}
	if err := os.WriteFile(sourcePath, newContent, 0o755); err != nil {
		t.Fatalf("setup sourcePath: %v", err)
	}

	if err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath); err != nil {
		t.Fatalf("atomicReplaceBinary: %v", err)
	}

	// Current path should now contain the new content.
	got, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("ReadFile currentPath: %v", err)
	}
	if string(got) != string(newContent) {
		t.Errorf("currentPath content = %q, want %q", got, newContent)
	}

	// .old path should contain the original content.
	oldPath := currentPath + ".old"
	gotOld, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("ReadFile oldPath: %v", err)
	}
	if string(gotOld) != string(originalContent) {
		t.Errorf("oldPath content = %q, want %q", gotOld, originalContent)
	}
}

// TestAtomicReplaceBinary_SourceMissing verifies that a missing source returns
// an error and leaves currentPath untouched.
func TestAtomicReplaceBinary_SourceMissing(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "nonexistent.exe")

	originalContent := []byte("original-binary-v1")
	if err := os.WriteFile(currentPath, originalContent, 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}

	err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath)
	if err == nil {
		t.Fatal("expected error for missing source, got nil")
	}

	// currentPath must still contain the original content.
	got, readErr := os.ReadFile(currentPath)
	if readErr != nil {
		t.Fatalf("ReadFile currentPath after failure: %v", readErr)
	}
	if string(got) != string(originalContent) {
		t.Errorf("currentPath content = %q, want original %q", got, originalContent)
	}
}

// TestAtomicReplaceBinary_ExistingOldIsReplaced verifies that a pre-existing
// .old file is overwritten / removed and does not block the operation.
func TestAtomicReplaceBinary_ExistingOldIsReplaced(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-new.exe")
	oldPath := currentPath + ".old"

	// Pre-populate .old with stale content.
	if err := os.WriteFile(oldPath, []byte("stale-old-binary"), 0o755); err != nil {
		t.Fatalf("setup oldPath: %v", err)
	}
	if err := os.WriteFile(currentPath, []byte("original-v1"), 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("new-v2"), 0o755); err != nil {
		t.Fatalf("setup sourcePath: %v", err)
	}

	if err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath); err != nil {
		t.Fatalf("atomicReplaceBinary: %v", err)
	}

	// .old should now contain the original (not the stale content).
	gotOld, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("ReadFile oldPath: %v", err)
	}
	if string(gotOld) != "original-v1" {
		t.Errorf("oldPath content = %q, want original-v1", gotOld)
	}
}

// TestAtomicReplaceBinary_ContentIntegrity verifies that the installed binary
// is byte-for-byte identical to the source, even for larger payloads.
func TestAtomicReplaceBinary_ContentIntegrity(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-new.exe")

	// Build a payload large enough to exercise multi-chunk I/O paths.
	payload := make([]byte, 128*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	if err := os.WriteFile(currentPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}
	if err := os.WriteFile(sourcePath, payload, 0o755); err != nil {
		t.Fatalf("setup sourcePath: %v", err)
	}

	if err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath); err != nil {
		t.Fatalf("atomicReplaceBinary: %v", err)
	}

	got, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("ReadFile currentPath: %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("installed binary size = %d, want %d", len(got), len(payload))
	}
	for i, b := range payload {
		if got[i] != b {
			t.Fatalf("byte mismatch at offset %d: got %02x, want %02x", i, got[i], b)
		}
	}
}

// TestAtomicReplaceBinary_ErrorTypes verifies that the exported error sentinel
// types are accessible from outside the package.
func TestAtomicReplaceBinary_ErrorTypes(t *testing.T) {
	// Verify the error types are constructable and implement the error interface.
	var _ error = &upgrade.ErrOldSlotLocked{}
	var _ error = &upgrade.ErrCurrentBinaryLocked{}

	e := &upgrade.ErrOldSlotLocked{OldPath: "/tmp/test.old"}
	if e.Error() == "" {
		t.Error("ErrOldSlotLocked.Error() returned empty string")
	}

	e2 := &upgrade.ErrCurrentBinaryLocked{BinaryPath: "/tmp/test.exe"}
	if e2.Error() == "" {
		t.Error("ErrCurrentBinaryLocked.Error() returned empty string")
	}
}

// TestAtomicReplaceBinary_IsHelpers verifies the Is* sentinel helpers.
func TestAtomicReplaceBinary_IsHelpers(t *testing.T) {
	err1 := &upgrade.ErrOldSlotLocked{OldPath: "/tmp/x.old"}
	if !upgrade.IsOldSlotLocked(err1) {
		t.Error("IsOldSlotLocked returned false for ErrOldSlotLocked")
	}
	if upgrade.IsCurrentBinaryLocked(err1) {
		t.Error("IsCurrentBinaryLocked returned true for ErrOldSlotLocked")
	}

	err2 := &upgrade.ErrCurrentBinaryLocked{BinaryPath: "/tmp/x.exe"}
	if !upgrade.IsCurrentBinaryLocked(err2) {
		t.Error("IsCurrentBinaryLocked returned false for ErrCurrentBinaryLocked")
	}
	if upgrade.IsOldSlotLocked(err2) {
		t.Error("IsOldSlotLocked returned true for ErrCurrentBinaryLocked")
	}
}
