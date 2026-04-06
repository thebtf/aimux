//go:build linux

package pty

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

func TestPTY_Run_Echo(t *testing.T) {
	e := New()
	if !e.Available() {
		t.Skip("PTY not available on this platform")
	}

	result, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "echo",
		Args:           []string{"pty_test_output"},
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Content, "pty_test_output") {
		t.Errorf("Content = %q, want to contain 'pty_test_output'", result.Content)
	}
}

func TestPTY_Run_Timeout(t *testing.T) {
	e := New()
	if !e.Available() {
		t.Skip("PTY not available on this platform")
	}

	result, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "sleep",
		Args:           []string{"10"},
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 124 {
		t.Errorf("ExitCode = %d, want 124 (timeout)", result.ExitCode)
	}
	if !result.Partial {
		t.Error("expected Partial=true for timeout")
	}
}

func TestPTY_Run_ContextCancel(t *testing.T) {
	e := New()
	if !e.Available() {
		t.Skip("PTY not available on this platform")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	result, err := e.Run(ctx, types.SpawnArgs{
		Command:        "sleep",
		Args:           []string{"10"},
		TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 130 {
		t.Errorf("ExitCode = %d, want 130 (cancelled)", result.ExitCode)
	}
	if !result.Partial {
		t.Error("expected Partial=true for cancel")
	}
}

func TestPTY_Run_WithEnv(t *testing.T) {
	e := New()
	if !e.Available() {
		t.Skip("PTY not available on this platform")
	}

	result, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "env",
		Env:            map[string]string{"TEST_PTY_VAR": "hello_pty"},
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Content, "TEST_PTY_VAR=hello_pty") {
		t.Errorf("Content should contain TEST_PTY_VAR=hello_pty, got %q", result.Content)
	}
}

func TestPTY_Run_ExitCode(t *testing.T) {
	e := New()
	if !e.Available() {
		t.Skip("PTY not available on this platform")
	}

	result, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "bash",
		Args:           []string{"-c", "exit 42"},
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", result.ExitCode)
	}
}

func TestPTY_Run_WithCWD(t *testing.T) {
	e := New()
	if !e.Available() {
		t.Skip("PTY not available on this platform")
	}

	result, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "pwd",
		CWD:            "/tmp",
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Content, "/tmp") {
		t.Errorf("Content = %q, want to contain '/tmp'", result.Content)
	}
}

func TestPTY_Run_BadCommand(t *testing.T) {
	e := New()
	if !e.Available() {
		t.Skip("PTY not available on this platform")
	}

	_, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "nonexistent_binary_xyz_123",
		TimeoutSeconds: 5,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
}

func TestPTY_Run_WithStdin(t *testing.T) {
	e := New()
	if !e.Available() {
		t.Skip("PTY not available on this platform")
	}

	result, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "cat",
		Stdin:          "hello from stdin",
		TimeoutSeconds: 3,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Content, "hello from stdin") {
		t.Errorf("Content = %q, want to contain 'hello from stdin'", result.Content)
	}
}
