package pty_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/types"
)

// TestPTY_Start_ReturnsError verifies error propagation from Start().
//
// Two error paths exist:
//   - Windows: Start() rejects up front with "PTY not available on this platform"
//     (e.available == false).
//   - Linux/macOS: Start() runs pty.Start(cmd) which returns an error when the
//     command is missing or invalid. We use a deliberately non-existent command
//     to force the underlying exec.LookPath / pty.Start to fail.
func TestPTY_Start_ReturnsError(t *testing.T) {
	e := pty.New()
	_, err := e.Start(context.Background(), types.SpawnArgs{
		Command: "this-binary-does-not-exist-aimux-ci-stability-fr1",
		Args:    []string{},
	})

	if err == nil {
		t.Fatal("expected error from Start() with non-existent command")
	}

	errMsg := err.Error()
	// Windows path: "not available". Unix path: "PTY session start failed".
	if !strings.Contains(errMsg, "not available") && !strings.Contains(errMsg, "PTY session start failed") {
		t.Errorf("error should mention platform unavailability or session start failure, got: %s", errMsg)
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
