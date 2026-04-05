package pty_test

import (
	"runtime"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/pty"
)

func TestPTY_Name(t *testing.T) {
	e := pty.New()
	if e.Name() != "pty" {
		t.Errorf("Name = %q, want pty", e.Name())
	}
}

func TestPTY_Available(t *testing.T) {
	e := pty.New()
	switch runtime.GOOS {
	case "linux", "darwin":
		if !e.Available() {
			t.Error("PTY should be available on Linux/macOS")
		}
	default:
		if e.Available() {
			t.Error("PTY should not be available on Windows")
		}
	}
}
