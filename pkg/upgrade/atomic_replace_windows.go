//go:build windows

package upgrade

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// retryDelays defines the bounded backoff schedule for filesystem retry attempts
// per ADR-003: total upper bound ~2.6 s across three attempts.
var retryDelays = [3]time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2000 * time.Millisecond}

// platformAtomicReplace is the Windows implementation of atomicReplaceBinary.
//
// Uses the stage-then-swap pattern per ADR-001:
//  1. Stage: copy source → currentPath+".new" (plain write, no rename yet)
//  2. Prepare rollback slot: prefer currentPath+".old", fall back to a unique
//     .old.<pid>.<nanos> path when the fixed .old file is still held by an
//     older live process after a deferred restart
//  3. Rotate: MoveFileExW(currentPath → rollback slot) with bounded retry
//  4. Install: MoveFileExW(.new → currentPath, REPLACE_EXISTING)
//  5. Cleanup: remove .new on success; on failure the .new is removed in defer
//
// Structured errors are returned with best-effort Restart Manager holder PIDs.
func platformAtomicReplace(currentPath, sourcePath string) error {
	newPath := currentPath + ".new"

	// Step 1: Stage the new binary as .new. Defer cleanup so .new is always
	// removed on any error path — we never want to leave a partial .new on disk.
	if err := copyFile(sourcePath, newPath); err != nil {
		return fmt.Errorf("stage new binary: %w", err)
	}
	stagedOK := false
	defer func() {
		if !stagedOK {
			_ = os.Remove(newPath)
		}
	}()

	// Step 2: Prepare a rollback slot. A fixed .old slot is nice for humans,
	// but deferred Windows updates can leave old daemon/shim processes running
	// from aimux.exe.old. Do not block a second update on that stale image
	// handle; use a unique rollback name and clean old slots opportunistically.
	cleanupStaleOldSlots(currentPath)
	oldPath, err := prepareOldSlot(currentPath)
	if err != nil {
		return err
	}

	// Step 3: Rotate current binary → .old via MoveFileExW.
	// MOVEFILE_WRITE_THROUGH flushes metadata to disk before returning.
	if err := retryMoveFileEx(currentPath, oldPath, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		holders := restartManagerProbe(currentPath)
		return &ErrCurrentBinaryLocked{
			BinaryPath: currentPath,
			Holders:    holders,
			Cause:      err,
		}
	}

	// Step 4: Install staged .new → currentPath (REPLACE_EXISTING for safety,
	// though the slot was just vacated in step 3).
	if err := retryMoveFileEx(newPath, currentPath, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		// Partial failure: currentPath slot is now empty. Attempt rollback by
		// restoring the original binary from .old.
		_ = os.Rename(oldPath, currentPath)
		return fmt.Errorf("install new binary: %w", err)
	}

	stagedOK = true // suppress .new cleanup in defer (it was moved away)
	return nil
}

func prepareOldSlot(currentPath string) (string, error) {
	fixedOldPath := currentPath + ".old"
	err := retryRemove(fixedOldPath)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return fixedOldPath, nil
	}

	info, statErr := os.Stat(fixedOldPath)
	if statErr == nil && info.Mode().IsRegular() {
		return uniqueOldPath(currentPath), nil
	}

	holders := restartManagerProbe(fixedOldPath)
	return "", &ErrOldSlotLocked{
		OldPath: fixedOldPath,
		Holders: holders,
		Cause:   err,
	}
}

func uniqueOldPath(currentPath string) string {
	return currentPath + ".old." + strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func cleanupStaleOldSlots(currentPath string) {
	dir := filepath.Dir(currentPath)
	prefix := filepath.Base(currentPath) + ".old."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !isUniqueOldSlotName(entry.Name(), prefix) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}

func isUniqueOldSlotName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := strings.TrimPrefix(name, prefix)
	pidPart, nanosPart, ok := strings.Cut(rest, ".")
	if !ok || pidPart == "" || nanosPart == "" || strings.Contains(nanosPart, ".") {
		return false
	}
	pid, pidErr := strconv.Atoi(pidPart)
	nanos, nanosErr := strconv.ParseInt(nanosPart, 10, 64)
	return pidErr == nil && nanosErr == nil && pid > 0 && nanos > 0
}

// retryRemove attempts os.Remove up to len(retryDelays) times with exponential
// backoff. Returns nil if the file is gone (including ErrNotExist on first try).
func retryRemove(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	for _, d := range retryDelays {
		time.Sleep(d)
		err = os.Remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	return err
}

// retryMoveFileEx calls MoveFileExW with bounded retry using the backoff schedule.
func retryMoveFileEx(from, to string, flags uint32) error {
	err := moveFileEx(from, to, flags)
	if err == nil {
		return nil
	}
	for _, d := range retryDelays {
		time.Sleep(d)
		err = moveFileEx(from, to, flags)
		if err == nil {
			return nil
		}
	}
	return err
}

// moveFileEx is a thin wrapper around windows.MoveFileEx that converts paths.
func moveFileEx(from, to string, flags uint32) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return fmt.Errorf("encode source path: %w", err)
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return fmt.Errorf("encode destination path: %w", err)
	}
	return windows.MoveFileEx(fromPtr, toPtr, flags)
}

// copyFile copies src to dst using os.ReadFile / os.WriteFile semantics.
// dst is created with mode 0o755 so the installed binary remains executable.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return fmt.Errorf("write destination: %w", err)
	}
	return nil
}

// ---- Restart Manager probe (best-effort, ADR-004) -------------------------

// Windows Restart Manager API constants.
const (
	rmSessionKeyLen = 32
	ccmMaxProcesses = 10
)

// restartManagerProbe attempts to identify which processes hold a handle on
// the given file path using the Windows Restart Manager API. On any failure
// it returns an empty slice (degraded mode per ADR-004).
func restartManagerProbe(filePath string) []ProcessHolder {
	// Load Rstrtmgr.dll dynamically — it is not available on all Windows
	// editions (e.g. some embedded SKUs). Graceful degradation on load failure.
	rstrtmgr, err := windows.LoadDLL("Rstrtmgr.dll")
	if err != nil {
		return nil
	}
	defer rstrtmgr.Release() //nolint:errcheck

	rmStartSession, err := rstrtmgr.FindProc("RmStartSession")
	if err != nil {
		return nil
	}
	rmRegisterResources, err := rstrtmgr.FindProc("RmRegisterResources")
	if err != nil {
		return nil
	}
	rmGetList, err := rstrtmgr.FindProc("RmGetList")
	if err != nil {
		return nil
	}
	rmEndSession, err := rstrtmgr.FindProc("RmEndSession")
	if err != nil {
		return nil
	}

	// Start a Restart Manager session.
	var sessionHandle uint32
	var sessionKey [rmSessionKeyLen + 1]uint16
	ret, _, _ := rmStartSession.Call(
		uintptr(unsafe.Pointer(&sessionHandle)),
		0,
		uintptr(unsafe.Pointer(&sessionKey[0])),
	)
	if ret != 0 {
		return nil
	}
	defer rmEndSession.Call(uintptr(sessionHandle)) //nolint:errcheck

	// Register the file we want to probe.
	filePtr, err := windows.UTF16PtrFromString(filePath)
	if err != nil {
		return nil
	}
	fileArrayPtr := uintptr(unsafe.Pointer(&filePtr))
	ret, _, _ = rmRegisterResources.Call(
		uintptr(sessionHandle),
		1, // nFiles
		fileArrayPtr,
		0, 0, // nApplications, rgsApplications
		0, 0, // nServices, rgsServiceNames
	)
	if ret != 0 {
		return nil
	}

	// Query the list of processes using the resource.
	// First call with zero buffer to get the required count.
	var procInfoNeeded, procInfoCount, rebootReasons uint32
	const errorMoreData = 234 // ERROR_MORE_DATA
	ret, _, _ = rmGetList.Call(
		uintptr(sessionHandle),
		uintptr(unsafe.Pointer(&procInfoNeeded)),
		uintptr(unsafe.Pointer(&procInfoCount)),
		0,
		uintptr(unsafe.Pointer(&rebootReasons)),
	)
	if ret != 0 && ret != errorMoreData {
		return nil
	}
	if procInfoNeeded == 0 {
		return nil
	}

	// Cap to a reasonable number to avoid over-allocating.
	count := procInfoNeeded
	if count > ccmMaxProcesses {
		count = ccmMaxProcesses
	}

	// RM_PROCESS_INFO layout (simplified for our use — we only need
	// Process.dwProcessId and strAppName). The struct is 308 bytes on 64-bit.
	type rmProcessInfo struct {
		Process struct {
			PID       uint32
			StartTime windows.Filetime
		}
		AppName          [256]uint16
		ServiceShortName [63]uint16
		AppType          uint32
		AppStatus        uint32
		TSSessionID      uint32
		Restartable      uint8
		_                [3]byte // padding
	}

	buf := make([]rmProcessInfo, count)
	procInfoCount = count
	ret, _, _ = rmGetList.Call(
		uintptr(sessionHandle),
		uintptr(unsafe.Pointer(&procInfoNeeded)),
		uintptr(unsafe.Pointer(&procInfoCount)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&rebootReasons)),
	)
	if ret != 0 && ret != errorMoreData {
		return nil
	}

	holders := make([]ProcessHolder, 0, procInfoCount)
	for i := uint32(0); i < procInfoCount; i++ {
		pi := buf[i]
		name := windows.UTF16ToString(pi.AppName[:])
		holders = append(holders, ProcessHolder{
			PID:  pi.Process.PID,
			Name: name,
		})
	}
	return holders
}
