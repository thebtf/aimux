//go:build windows

package upgrade_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/upgrade"
)

// TestAtomicReplaceBinary_Windows_OldSlotError verifies that when the .old path
// is a directory (which cannot be removed on Windows), an ErrOldSlotLocked is
// returned and currentPath is untouched.
func TestAtomicReplaceBinary_Windows_OldSlotError(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-new.exe")
	oldPath := currentPath + ".old"

	// Create a directory at oldPath — os.Remove on a non-empty dir fails on Windows.
	// An empty dir is sufficient since Windows MoveFileExW won't overwrite a directory.
	if err := os.Mkdir(oldPath, 0o755); err != nil {
		t.Fatalf("setup oldPath as dir: %v", err)
	}
	// Populate the directory so it isn't trivially removable.
	if err := os.WriteFile(filepath.Join(oldPath, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup sentinel: %v", err)
	}

	if err := os.WriteFile(currentPath, []byte("original"), 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("new"), 0o755); err != nil {
		t.Fatalf("setup sourcePath: %v", err)
	}

	err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath)
	if err == nil {
		t.Fatal("expected error when .old slot is a directory, got nil")
	}

	// Must be classified as ErrOldSlotLocked.
	if !upgrade.IsOldSlotLocked(err) {
		t.Errorf("expected ErrOldSlotLocked, got %T: %v", err, err)
	}

	// currentPath must still contain the original content.
	got, readErr := os.ReadFile(currentPath)
	if readErr != nil {
		t.Fatalf("ReadFile currentPath after failure: %v", readErr)
	}
	if string(got) != "original" {
		t.Errorf("currentPath modified after ErrOldSlotLocked; content = %q", got)
	}
}

// TestAtomicReplaceBinary_Windows_ErrorTypesWrapped verifies that errors returned
// by AtomicReplaceBinaryForTest can be unwrapped via errors.As to the concrete types.
func TestAtomicReplaceBinary_Windows_ErrorTypesWrapped(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-new.exe")
	oldPath := currentPath + ".old"

	if err := os.Mkdir(oldPath, 0o755); err != nil {
		t.Fatalf("setup oldPath as dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldPath, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup sentinel: %v", err)
	}
	if err := os.WriteFile(currentPath, []byte("original"), 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("new"), 0o755); err != nil {
		t.Fatalf("setup sourcePath: %v", err)
	}

	err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var locked *upgrade.ErrOldSlotLocked
	if !errors.As(err, &locked) {
		t.Fatalf("errors.As(ErrOldSlotLocked) failed for %T: %v", err, err)
	}
	if locked.OldPath == "" {
		t.Error("ErrOldSlotLocked.OldPath is empty")
	}
	// Cause must be non-nil (the underlying OS error).
	if locked.Cause == nil {
		t.Error("ErrOldSlotLocked.Cause is nil; expected wrapped OS error")
	}
}

// TestAtomicReplaceBinary_Windows_LockedOldSlotFallsBackToUniqueBackup verifies
// the deferred-update regression: a previous aimux.exe.old can remain locked by
// an older daemon/shim image. That stale rollback file must not block the next
// local-source update.
func TestAtomicReplaceBinary_Windows_LockedOldSlotFallsBackToUniqueBackup(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-new.exe")
	oldPath := currentPath + ".old"

	if err := os.WriteFile(oldPath, []byte("stale-locked-old"), 0o755); err != nil {
		t.Fatalf("setup oldPath: %v", err)
	}
	lockedOld, err := os.Open(oldPath)
	if err != nil {
		t.Fatalf("open locked oldPath: %v", err)
	}
	defer lockedOld.Close()

	if err := os.WriteFile(currentPath, []byte("original-v1"), 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("new-v2"), 0o755); err != nil {
		t.Fatalf("setup sourcePath: %v", err)
	}

	if err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath); err != nil {
		t.Fatalf("AtomicReplaceBinaryForTest with locked .old: %v", err)
	}

	got, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("ReadFile currentPath: %v", err)
	}
	if string(got) != "new-v2" {
		t.Fatalf("currentPath = %q, want new-v2", got)
	}

	stale, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("ReadFile locked oldPath: %v", err)
	}
	if string(stale) != "stale-locked-old" {
		t.Fatalf("locked oldPath = %q, want stale-locked-old", stale)
	}

	matches, err := filepath.Glob(currentPath + ".old.*")
	if err != nil {
		t.Fatalf("Glob unique old slots: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected unique rollback slot when fixed .old is locked")
	}
	foundOriginal := false
	for _, match := range matches {
		data, readErr := os.ReadFile(match)
		if readErr != nil {
			t.Fatalf("ReadFile unique old slot %s: %v", match, readErr)
		}
		if string(data) == "original-v1" {
			foundOriginal = true
			break
		}
	}
	if !foundOriginal {
		t.Fatalf("unique rollback slots %v did not contain original-v1", matches)
	}
}

// TestAtomicReplaceBinary_Windows_CleanupStaleOldSlotsKeepsUnrelatedFiles
// verifies cleanup is limited to the unique rollback slot format generated by
// uniqueOldPath: .old.<pid>.<nanos>. Operator-created files with the same
// prefix must not be deleted.
func TestAtomicReplaceBinary_Windows_CleanupStaleOldSlotsKeepsUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-new.exe")
	staleGeneratedSlot := currentPath + ".old.123.456"
	manualOldFile := currentPath + ".old.manual"

	if err := os.WriteFile(currentPath, []byte("original-v1"), 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("new-v2"), 0o755); err != nil {
		t.Fatalf("setup sourcePath: %v", err)
	}
	if err := os.WriteFile(staleGeneratedSlot, []byte("generated-stale"), 0o755); err != nil {
		t.Fatalf("setup stale generated slot: %v", err)
	}
	if err := os.WriteFile(manualOldFile, []byte("operator-note"), 0o644); err != nil {
		t.Fatalf("setup manual old file: %v", err)
	}

	if err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath); err != nil {
		t.Fatalf("AtomicReplaceBinaryForTest: %v", err)
	}

	if _, err := os.Stat(staleGeneratedSlot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generated stale slot should be removed, stat=%v", err)
	}
	manual, err := os.ReadFile(manualOldFile)
	if err != nil {
		t.Fatalf("manual old file should remain readable: %v", err)
	}
	if string(manual) != "operator-note" {
		t.Fatalf("manual old file content = %q, want operator-note", manual)
	}
}

// TestAtomicReplaceBinary_Windows_NewFileCleanedOnStagingFailure verifies that
// the .new staging file is not left on disk when the source does not exist.
func TestAtomicReplaceBinary_Windows_NewFileCleanedOnStagingFailure(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "nonexistent.exe")
	newPath := currentPath + ".new"

	if err := os.WriteFile(currentPath, []byte("original"), 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}

	err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath)
	if err == nil {
		t.Fatal("expected error for missing source, got nil")
	}

	// The .new file must not linger after a staging failure.
	if _, statErr := os.Stat(newPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf(".new file left on disk after staging failure; stat=%v", statErr)
	}
}

// TestAtomicReplaceBinary_Windows_StagedNewCleanedOnRotateFailure verifies that
// the .new file is removed when step 3 (current→.old rotation) fails. This is
// hard to trigger without OS-level locking, so the test validates the happy
// path from which the rollback branch would diverge — ensuring the .new file is
// never visible at currentPath after a successful install.
func TestAtomicReplaceBinary_Windows_StagedNewNotVisibleAfterInstall(t *testing.T) {
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-new.exe")
	newPath := currentPath + ".new"

	if err := os.WriteFile(currentPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("setup currentPath: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("v2"), 0o755); err != nil {
		t.Fatalf("setup sourcePath: %v", err)
	}

	if err := upgrade.AtomicReplaceBinaryForTest(currentPath, sourcePath); err != nil {
		t.Fatalf("AtomicReplaceBinaryForTest: %v", err)
	}

	// After a successful install, the .new staging file must be gone.
	if _, statErr := os.Stat(newPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf(".new file still present after successful install; stat=%v", statErr)
	}

	// currentPath must hold the new content.
	got, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("ReadFile currentPath: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("currentPath = %q, want v2", got)
	}
}
