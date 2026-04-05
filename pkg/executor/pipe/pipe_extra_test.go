package pipe_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

func TestPipeExecutor_Run_StdinPiping(t *testing.T) {
	exec := pipe.New()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		// findstr reads from stdin and echoes matching lines
		cmd = "findstr"
		args = []string{"."}
	} else {
		cmd = "cat"
		args = nil
	}

	result, err := exec.Run(context.Background(), types.SpawnArgs{
		Command:        cmd,
		Args:           args,
		Stdin:          "stdin content here",
		TimeoutSeconds: 5,
	})

	if err != nil {
		t.Fatalf("Run with stdin: %v", err)
	}

	if !strings.Contains(result.Content, "stdin content here") {
		t.Errorf("Content = %q, expected stdin content echoed back", result.Content)
	}
}

func TestPipeExecutor_Run_CompletionPattern(t *testing.T) {
	exec := pipe.New()

	// Use a command that outputs a known pattern, then would hang
	// On Windows: echo with delay simulation
	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		// cmd /c echo outputs immediately and exits — good enough for pattern test
		cmd = "cmd"
		args = []string{"/c", "echo", "DONE:completed"}
	} else {
		cmd = "echo"
		args = []string{"DONE:completed"}
	}

	result, err := exec.Run(context.Background(), types.SpawnArgs{
		Command:           cmd,
		Args:              args,
		CompletionPattern: "DONE:.*",
		TimeoutSeconds:    5,
	})

	if err != nil {
		t.Fatalf("Run with completion pattern: %v", err)
	}

	if !strings.Contains(result.Content, "DONE:completed") {
		t.Errorf("Content = %q, expected pattern match", result.Content)
	}
}

func TestPipeExecutor_Run_InvalidCompletionPattern(t *testing.T) {
	exec := pipe.New()

	cmd, args := echoCommand()

	// Invalid regex should be handled gracefully — process runs normally
	result, err := exec.Run(context.Background(), types.SpawnArgs{
		Command:           cmd,
		Args:              args,
		CompletionPattern: "[invalid",
		TimeoutSeconds:    5,
	})

	if err != nil {
		t.Fatalf("Run with invalid pattern: %v", err)
	}

	// Should still complete normally (pattern skipped)
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestPipeExecutor_Start_PersistentSession(t *testing.T) {
	exec := pipe.New()

	cmd, args := echoCommand()

	session, err := exec.Start(context.Background(), types.SpawnArgs{
		Command: cmd,
		Args:    args,
	})

	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if session == nil {
		t.Fatal("expected non-nil session")
	}

	// Send data and close
	if _, sendErr := session.Send(context.Background(), "test input"); sendErr != nil {
		t.Logf("Send: %v (may be expected if process exited)", sendErr)
	}
	session.Close()
}

func TestPipeExecutor_Run_EnvVars(t *testing.T) {
	exec := pipe.New()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "cmd"
		args = []string{"/c", "echo", "%TEST_VAR%"}
	} else {
		cmd = "sh"
		args = []string{"-c", "echo $TEST_VAR"}
	}

	result, err := exec.Run(context.Background(), types.SpawnArgs{
		Command:        cmd,
		Args:           args,
		Env:            map[string]string{"TEST_VAR": "hello_env"},
		TimeoutSeconds: 5,
	})

	if err != nil {
		t.Fatalf("Run with env: %v", err)
	}

	if !strings.Contains(result.Content, "hello_env") {
		t.Errorf("Content = %q, expected env var value", result.Content)
	}
}
