//go:build !windows

package upgrade

import (
	"os"
)

// platformAtomicReplace is the Unix implementation of atomicReplaceBinary.
//
// On Linux and macOS, os.Rename succeeds even when other processes hold open
// handles on either source or destination (POSIX semantics: the file is
// unlinked from the directory entry but existing handles survive). This means
// the current binary can be renamed to .old while it is executing, and a new
// binary written to BinaryPath, all without process disruption.
//
// The behavior is byte-identical to the pre-refactor inline logic in
// coordinator.go::applyFromLocal. No retry is needed on Unix.
func platformAtomicReplace(currentPath, sourcePath string) error {
	oldPath := currentPath + ".old"
	// Remove any previous .old file; failure is non-fatal (it may not exist).
	_ = os.Remove(oldPath)

	// Rotate current binary to .old so the original is preserved for rollback.
	if err := os.Rename(currentPath, oldPath); err != nil {
		return err
	}

	// Move source to the binary path. On failure restore the original.
	if err := os.Rename(sourcePath, currentPath); err != nil {
		_ = os.Rename(oldPath, currentPath) // best-effort rollback
		return err
	}

	return nil
}
