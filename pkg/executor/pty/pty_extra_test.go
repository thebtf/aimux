package pty_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/types"
)

func TestPTY_Start_ReturnsError(t *testing.T) {
	e := pty.New()
	_, err := e.Start(context.Background(), types.SpawnArgs{
		Command: "echo",
		Args:    []string{"test"},
	})

	if err == nil {
		t.Fatal("expected error from Start()")
	}

	errMsg := err.Error()
	// On platforms where PTY is available, error mentions Pipe executor
	// On platforms where PTY is unavailable, error mentions platform unavailability
	if !strings.Contains(errMsg, "Pipe executor") && !strings.Contains(errMsg, "not available") {
		t.Errorf("error should mention Pipe executor or platform unavailability, got: %s", errMsg)
	}
}

func TestPTY_Run_Unavailable(t *testing.T) {
	e := pty.New()
	if e.Available() {
		t.Skip("PTY available on this platform — test targets unavailable case")
	}

	_, err := e.Run(context.Background(), types.SpawnArgs{
		Command: "echo",
		Args:    []string{"test"},
	})

	if err == nil {
		t.Fatal("expected error when PTY unavailable")
	}
}
