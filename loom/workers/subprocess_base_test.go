package workers

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
)

// platformEcho returns a cross-platform echo command and args for the given text.
func platformEcho(text string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", "echo", text}
	}
	return "sh", []string{"-c", "echo " + text}
}

// platformSleep returns a cross-platform long-running command.
func platformSleep() (string, []string) {
	if runtime.GOOS == "windows" {
		// ping -n 11 127.0.0.1 waits ~10 seconds (10 intervals of 1s between pings)
		return "ping", []string{"-n", "11", "127.0.0.1"}
	}
	return "sh", []string{"-c", "sleep 10"}
}

// staticResolver is a SpawnResolver that always returns the same SubprocessSpawn.
type staticResolver struct {
	spawn SubprocessSpawn
}

func (r *staticResolver) Resolve(_ context.Context, _ *loom.Task) (SubprocessSpawn, error) {
	return r.spawn, nil
}

// errorResolver returns a fixed error from Resolve.
type errorResolver struct {
	err error
}

func (r *errorResolver) Resolve(_ context.Context, _ *loom.Task) (SubprocessSpawn, error) {
	return SubprocessSpawn{}, r.err
}

// TestSubprocessBase_HappyPath verifies a simple echo command.
func TestSubprocessBase_HappyPath(t *testing.T) {
	cmd, args := platformEcho("hello")
	base := &SubprocessBase{
		Resolver: &staticResolver{spawn: SubprocessSpawn{Command: cmd, Args: args}},
	}
	task := &loom.Task{ID: "t1", Prompt: "test"}
	result, err := base.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	// echo output typically includes trailing newline/CR; trim before comparison.
	got := strings.TrimSpace(result.Content)
	if got != "hello" {
		t.Errorf("content: want %q, got %q", "hello", got)
	}
	if result.Metadata["exit_code"] != 0 {
		t.Errorf("exit_code: want 0, got %v", result.Metadata["exit_code"])
	}
	if result.DurationMS < 0 {
		t.Error("duration should be non-negative")
	}
}

// TestSubprocessBase_ContextCancellation verifies cancellation kills the subprocess.
func TestSubprocessBase_ContextCancellation(t *testing.T) {
	cmd, args := platformSleep()
	base := &SubprocessBase{
		Resolver: &staticResolver{spawn: SubprocessSpawn{Command: cmd, Args: args}},
	}
	task := &loom.Task{ID: "t2"}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay so the process starts.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	result, err := base.Run(ctx, task)
	// Result should be non-nil (partial output captured) and error should wrap ctx.Err().
	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should wrap context.Canceled, got: %v", err)
	}
	// result may be non-nil (partial output is acceptable)
	_ = result
}

// TestSubprocessBase_Timeout verifies task.Timeout triggers DeadlineExceeded.
func TestSubprocessBase_Timeout(t *testing.T) {
	cmd, args := platformSleep()
	base := &SubprocessBase{
		Resolver: &staticResolver{spawn: SubprocessSpawn{Command: cmd, Args: args}},
	}
	// Timeout=1 second; sleep is 10 seconds — should deadline before completion.
	task := &loom.Task{ID: "t3", Timeout: 1}

	result, err := base.Run(context.Background(), task)
	if err == nil {
		t.Fatal("expected error on timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error should wrap context.DeadlineExceeded, got: %v", err)
	}
	_ = result
}

// TestSubprocessBase_ResolverError verifies resolver errors propagate as-is.
func TestSubprocessBase_ResolverError(t *testing.T) {
	sentinel := errors.New("resolve boom")
	base := &SubprocessBase{
		Resolver: &errorResolver{err: sentinel},
	}
	task := &loom.Task{ID: "t4"}

	_, err := base.Run(context.Background(), task)
	if err == nil {
		t.Fatal("expected error from resolver")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error in chain, got: %v", err)
	}
}
