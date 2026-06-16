//go:build windows

package upgrade

import (
	"fmt"

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
		if rollbackErr := retryMoveFileEx(oldPath, currentPath, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); rollbackErr != nil {
			return fmt.Errorf("install staged binary: %w; rollback failed: %v", err, rollbackErr)
		}
		holders := restartManagerProbe(stagedPath)
		return &ErrStagedBinaryLocked{
			StagedPath: stagedPath,
			Holders:    holders,
			Cause:      err,
		}
	}
	return nil
}
