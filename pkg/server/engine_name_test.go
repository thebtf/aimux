package server

import (
	"os"
	"testing"
)

func TestResolveEngineName(t *testing.T) {
	t.Run("env-set returns env value", func(t *testing.T) {
		t.Setenv("AIMUX_ENGINE_NAME", "aimux-dev")
		got := ResolveEngineName()
		if got != "aimux-dev" {
			t.Fatalf("want aimux-dev, got %q", got)
		}
	})

	t.Run("env-whitespace falls back to basename or aimux", func(t *testing.T) {
		t.Setenv("AIMUX_ENGINE_NAME", "  \t  ")
		originalArgs := os.Args
		os.Args = []string{`C:\tools\aimux-dev.EXE`}
		t.Cleanup(func() { os.Args = originalArgs })

		got := ResolveEngineName()
		if got != "aimux-dev" {
			t.Fatalf("want aimux-dev, got %q", got)
		}
	})

	t.Run("env-empty falls back to binary basename", func(t *testing.T) {
		t.Setenv("AIMUX_ENGINE_NAME", "")
		originalArgs := os.Args
		os.Args = []string{`D:\bin\custom-engine.exe`}
		t.Cleanup(func() { os.Args = originalArgs })

		got := ResolveEngineName()
		if got != "custom-engine" {
			t.Fatalf("want custom-engine, got %q", got)
		}
	})
}
