//go:build !windows

package upgrade

import "os"

func moveStagedBinaryIntoPlace(currentPath, stagedPath string) error {
	oldPath := currentPath + ".old"
	_ = os.Remove(oldPath)

	if err := os.Rename(currentPath, oldPath); err != nil {
		return err
	}
	if err := os.Rename(stagedPath, currentPath); err != nil {
		_ = os.Rename(oldPath, currentPath)
		return err
	}
	return nil
}
