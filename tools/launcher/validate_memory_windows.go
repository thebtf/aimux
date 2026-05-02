//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const processQueryMemory = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.PROCESS_VM_READ

var getProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func sampleProcessMemory(pid int) (uint64, string, bool) {
	handle, err := windows.OpenProcess(processQueryMemory, false, uint32(pid))
	if err != nil {
		return 0, "", false
	}
	defer windows.CloseHandle(handle)

	counters := processMemoryCounters{CB: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	ret, _, _ := getProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.CB),
	)
	if ret == 0 {
		return 0, "", false
	}
	return uint64(counters.PeakWorkingSetSize), "windows GetProcessMemoryInfo PeakWorkingSetSize", true
}
