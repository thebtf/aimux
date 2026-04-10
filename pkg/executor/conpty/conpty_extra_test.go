package conpty_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/conpty"
	"github.com/thebtf/aimux/pkg/types"
)

func TestConPTY_Start_ReturnsError(t *testing.T) {
	e := conpty.New()
	if !e.Available() {
		t.Skip("ConPTY not available on this platform — Start() returns a different error")
	}
	_, err := e.Start(context.Background(), types.SpawnArgs{
		Command: "echo",
		Args:    []string{"test"},
	})

	if err == nil {
		t.Fatal("expected error from Start() — ConPTY handles single-shot only")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "Pipe executor") {
		t.Errorf("error should mention Pipe executor, got: %s", errMsg)
	}
}

func TestConPTY_Run_Unavailable(t *testing.T) {
	e := conpty.New()
	if e.Available() {
		t.Skip("ConPTY available on this platform — test targets unavailable case")
	}

	_, err := e.Run(context.Background(), types.SpawnArgs{
		Command: "echo",
		Args:    []string{"test"},
	})

	if err == nil {
		t.Fatal("expected error when ConPTY unavailable")
	}
}

func TestConPTY_Run_Echo(t *testing.T) {
	e := conpty.New()
	if !e.Available() {
		t.Skip("ConPTY not available on this platform")
	}

	result, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "cmd",
		Args:           []string{"/c", "echo", "conpty_test_output"},
		TimeoutSeconds: 5,
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}

	if !strings.Contains(result.Content, "conpty_test_output") {
		t.Errorf("Content = %q, expected 'conpty_test_output'", result.Content)
	}
}

func TestConPTY_Run_Timeout(t *testing.T) {
	e := conpty.New()
	if !e.Available() {
		t.Skip("ConPTY not available on this platform")
	}

	result, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "ping",
		Args:           []string{"-n", "10", "127.0.0.1"},
		TimeoutSeconds: 1,
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 124 {
		t.Errorf("ExitCode = %d, want 124 (timeout)", result.ExitCode)
	}
}

func TestConPTY_Run_BadCommand(t *testing.T) {
	e := conpty.New()
	if !e.Available() {
		t.Skip("ConPTY not available on this platform")
	}

	_, err := e.Run(context.Background(), types.SpawnArgs{
		Command: "nonexistent_binary_xyz",
	})

	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
}
