//go:build !windows

package main

func sampleProcessMemory(pid int) (uint64, string, bool) {
	return 0, "", false
}
