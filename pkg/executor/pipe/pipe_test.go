package pipe_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

func echoCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", "echo", "hello world"}
	}
	return "echo", []string{"hello world"}
}

func sleepCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		// ping -n 6 = ~5 seconds (1 per second + 1 initial)
		return "ping", []string{"-n", "6", "127.0.0.1"}
	}
	return "sleep", []string{"5"}
}

func TestPipeExecutor_Run_Echo(t *testing.T) {
	exec := pipe.New()

	if exec.Name() != "pipe" {
		t.Errorf("Name = %q, want pipe", exec.Name())
	}
	if !exec.Available() {
		t.Error("pipe executor should always be available")
	}

	cmd, args := echoCommand()

	result, err := exec.Run(context.Background(), types.SpawnArgs{
		Command: cmd,
		Args:    args,
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}

	if !strings.Contains(result.Content, "hello world") {
		t.Errorf("Content = %q, want to contain 'hello world'", result.Content)
	}

	if result.DurationMS <= 0 {
		t.Error("DurationMS should be positive")
	}
}

func TestPipeExecutor_Run_Timeout(t *testing.T) {
	exec := pipe.New()

	cmd, args := sleepCommand()

	result, err := exec.Run(context.Background(), types.SpawnArgs{
		Command:        cmd,
		Args:           args,
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

	if result.Error == nil {
		t.Error("expected Error to be set for timeout")
	}
}

func TestPipeExecutor_Run_ContextCancel(t *testing.T) {
	exec := pipe.New()

	ctx, cancel := context.WithCancel(context.Background())

	cmd, args := sleepCommand()

	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	result, err := exec.Run(ctx, types.SpawnArgs{
		Command: cmd,
		Args:    args,
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

func TestPipeExecutor_Run_BadCommand(t *testing.T) {
	exec := pipe.New()

	_, err := exec.Run(context.Background(), types.SpawnArgs{
		Command: "nonexistent_command_xyz",
	})

	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}

	if !types.IsTypedError(err, types.ErrorTypeExecutor) {
		t.Errorf("expected ExecutorError, got %T", err)
	}
}

func TestPipeSession_ProcessManagerTracking(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses cmd /c and ping -n — Windows-only syntax")
	}
	e := pipe.New()
	sess, err := e.Start(context.Background(), types.SpawnArgs{
		Command: "cmd",
		Args:    []string{"/c", "echo ready && ping -n 30 127.0.0.1 >nul"},
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer sess.Close()

	// Process should be tracked and alive.
	if !sess.Alive() {
		t.Error("expected session to be alive")
	}
	if sess.PID() <= 0 {
		t.Errorf("expected PID > 0, got %d", sess.PID())
	}

	// Close should kill and cleanup.
	sess.Close()
	time.Sleep(100 * time.Millisecond)
	if sess.Alive() {
		t.Error("expected session to be dead after Close")
	}
}

func TestPipeSession_ShutdownKillsSession(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses cmd /c and ping -n — Windows-only syntax")
	}
	e := pipe.New()
	sess, err := e.Start(context.Background(), types.SpawnArgs{
		Command: "cmd",
		Args:    []string{"/c", "ping -n 30 127.0.0.1 >nul"},
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	pid := sess.PID()
	if pid <= 0 {
		t.Fatal("expected PID > 0")
	}

	// Shutdown should kill all tracked sessions.
	t.Logf("before shutdown: alive=%v pid=%d", sess.Alive(), sess.PID())
	pipe.SessionProcessManager().Shutdown()
	t.Logf("after shutdown: alive=%v", sess.Alive())

	// Verify — after synchronous Shutdown+Kill, process must be dead.
	if sess.Alive() {
		t.Error("expected session to be dead after Shutdown")
	}
}

func TestPipeExecutor_Run_CancelReturnsPartialOutput(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses cmd /c and ping -n — Windows-only syntax")
	}
	e := pipe.New()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Output "line1", then sleep ~3s, then output "line2".
	// Context cancels after 1s — we expect partial output containing "line1" but not "line2".
	result, err := e.Run(ctx, types.SpawnArgs{
		Command:        "cmd",
		Args:           []string{"/c", "echo line1 && ping -n 4 127.0.0.1 >nul && echo line2"},
		TimeoutSeconds: 30,
	})

	if err != nil {
		t.Fatalf("expected result, got error: %v", err)
	}
	if !result.Partial {
		t.Error("expected Partial=true")
	}
	if result.Content == "" {
		t.Error("expected non-empty partial content")
	}
	if !strings.Contains(result.Content, "line1") {
		t.Errorf("expected content to contain 'line1', got: %q", result.Content)
	}
}
