//go:build windows

package upgrade

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func moveStagedBinaryIntoPlace(currentPath, stagedPath string) error {
	cleanupStaleOldSlots(currentPath)
	oldPath, err := prepareOldSlot(currentPath)
	if err != nil {
		return err
	}

	if err := retryMoveFileEx(currentPath, oldPath, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		holders := restartManagerProbe(currentPath)
		return &ErrCurrentBinaryLocked{
			BinaryPath: currentPath,
			Holders:    holders,
			Cause:      err,
		}
	}

	if err := retryMoveFileEx(stagedPath, currentPath, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		_ = os.Rename(oldPath, currentPath)
		return fmt.Errorf("install staged binary: %w", err)
	}
	return nil
}
