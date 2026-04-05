package conpty_test

import (
	"runtime"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/conpty"
)

func TestConPTY_Name(t *testing.T) {
	e := conpty.New()
	if e.Name() != "conpty" {
		t.Errorf("Name = %q, want conpty", e.Name())
	}
}

func TestConPTY_Available(t *testing.T) {
	e := conpty.New()
	if runtime.GOOS == "windows" {
		if !e.Available() {
			t.Error("ConPTY should be available on Windows")
		}
	} else {
		if e.Available() {
			t.Error("ConPTY should not be available on non-Windows")
		}
	}
}
