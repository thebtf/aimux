package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func findLauncherRepoRoot() (string, error) {
	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Dir(file))
	}
	for _, start := range candidates {
		if root, ok := walkToLauncherRoot(start); ok {
			return root, nil
		}
	}
	return "", fmt.Errorf("could not locate aimux repo root containing tools/launcher/testdata/emitters")
}

func walkToLauncherRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if isLauncherRoot(dir) {
			return dir, true
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", false
		}
		dir = next
	}
}

func isLauncherRoot(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "tools", "launcher", "testdata", "emitters")); err != nil {
		return false
	}
	return true
}
