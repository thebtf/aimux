// Package upgrade orchestrates the aimux binary upgrade flow.
package upgrade

import (
	"errors"
	"fmt"
)

// atomicReplaceBinary atomically replaces the binary at currentPath with the
// file at sourcePath. The implementation is platform-specific:
//
//   - Windows: stage-then-swap via MoveFileExW with bounded retry and
//     Restart Manager probe for holder-PID diagnosis on failure.
//   - Unix: direct os.Rename(sourcePath, currentPath), preserving the behavior
//     that existed before this refactor.
//
// On success the file formerly at currentPath is preserved at currentPath+".old"
// for rollback purposes. On failure currentPath is untouched.
func atomicReplaceBinary(currentPath, sourcePath string) error {
	return platformAtomicReplace(currentPath, sourcePath)
}

// ErrOldSlotLocked is returned when the .old slot cannot be cleared because
// another process holds a handle on it (Windows-specific failure mode).
type ErrOldSlotLocked struct {
	OldPath  string
	Holders  []ProcessHolder
	Cause    error
}

func (e *ErrOldSlotLocked) Error() string {
	if len(e.Holders) == 0 {
		return fmt.Sprintf("old binary slot locked (holders unknown): %s: %v", e.OldPath, e.Cause)
	}
	return fmt.Sprintf("old binary slot locked by %d process(es) — %s: %v", len(e.Holders), formatHolders(e.Holders), e.Cause)
}

func (e *ErrOldSlotLocked) Unwrap() error { return e.Cause }

// ErrCurrentBinaryLocked is returned when the current binary cannot be renamed
// to the .old slot even after the .old slot was successfully cleared.
type ErrCurrentBinaryLocked struct {
	BinaryPath string
	Holders    []ProcessHolder
	Cause      error
}

func (e *ErrCurrentBinaryLocked) Error() string {
	if len(e.Holders) == 0 {
		return fmt.Sprintf("current binary locked (holders unknown): %s: %v", e.BinaryPath, e.Cause)
	}
	return fmt.Sprintf("current binary locked by %d process(es) — %s: %v", len(e.Holders), formatHolders(e.Holders), e.Cause)
}

func (e *ErrCurrentBinaryLocked) Unwrap() error { return e.Cause }

// ProcessHolder carries identifying information about a process holding a file handle.
type ProcessHolder struct {
	PID  uint32
	Name string
}

func formatHolders(holders []ProcessHolder) string {
	if len(holders) == 0 {
		return "(none)"
	}
	var s string
	for i, h := range holders {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("pid=%d name=%s", h.PID, h.Name)
	}
	return s
}

// IsOldSlotLocked reports whether err (or any error in its chain) is ErrOldSlotLocked.
func IsOldSlotLocked(err error) bool {
	var e *ErrOldSlotLocked
	return errors.As(err, &e)
}

// IsCurrentBinaryLocked reports whether err (or any error in its chain) is ErrCurrentBinaryLocked.
func IsCurrentBinaryLocked(err error) bool {
	var e *ErrCurrentBinaryLocked
	return errors.As(err, &e)
}
