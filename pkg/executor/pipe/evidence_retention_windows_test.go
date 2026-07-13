//go:build windows

package pipe

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

const capacityRejectionHelperEnv = "AIMUX_PIPE_CAPACITY_REJECTION_HELPER"
const capacityRejectionReadyFileEnv = "AIMUX_PIPE_CAPACITY_REJECTION_READY_FILE"

func TestSendEventsCapacityRejectionHelper(t *testing.T) {
	if os.Getenv(capacityRejectionHelperEnv) != "1" {
		return
	}
	if readyFile := os.Getenv(capacityRejectionReadyFileEnv); readyFile != "" {
		if err := os.WriteFile(readyFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(2)
		}
	}
	select {}
}

type stdinCloseSpy struct {
	closes atomic.Int32
}

func (s *stdinCloseSpy) Write(p []byte) (int, error) { return len(p), nil }
func (s *stdinCloseSpy) Close() error {
	s.closes.Add(1)
	return nil
}

func TestSendEventsCapacityRejectionClosesOwnedStdin(t *testing.T) {
	e := New()
	baselineGoroutines := runtime.NumGoroutine()
	for i := 0; i < processEvidenceLimit; i++ {
		id := types.ExecutionID(fmt.Sprintf("live-%d", i))
		if err := e.retainProcessEvidence(id, &executor.ProcessHandle{PID: i + 1, StartedAt: time.Unix(0, int64(i+1))}); err != nil {
			t.Fatal(err)
		}
	}
	stdin := &stdinCloseSpy{}
	e.stdinPipe = func(*exec.Cmd) (io.WriteCloser, error) { return stdin, nil }
	readyFile := t.TempDir() + "\\ready"
	e.evidenceMu.Lock()
	locked := true
	defer func() {
		if locked {
			e.evidenceMu.Unlock()
		}
	}()
	result := make(chan error, 1)
	go func() {
		_, err := e.SendEvents(context.Background(), "rejected", types.Message{Spawn: &types.SpawnArgs{
			Command: os.Args[0],
			Args:    []string{"-test.run=^TestSendEventsCapacityRejectionHelper$", "-test.count=1"},
			Env: map[string]string{
				capacityRejectionHelperEnv:    "1",
				capacityRejectionReadyFileEnv: readyFile,
			},
			Stdin: "owned writer",
		}}, nil)
		result <- err
	}()

	pid := waitForCapacityRejectionHelperPID(t, readyFile)
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatalf("open spawned helper %d: %v", pid, err)
	}
	defer windows.CloseHandle(process)
	e.evidenceMu.Unlock()
	locked = false
	err = <-result
	if err == nil {
		t.Fatal("capacity rejection unexpectedly succeeded")
	}
	if closes := stdin.closes.Load(); closes != 1 {
		t.Fatalf("owned stdin close calls = %d, want 1", closes)
	}
	status, err := windows.WaitForSingleObject(process, uint32((5*time.Second)/time.Millisecond))
	if err != nil || status != uint32(windows.WAIT_OBJECT_0) {
		t.Fatalf("capacity rejection left spawned helper %d alive: status=%d err=%v", pid, status, err)
	}
	if goroutines := runtime.NumGoroutine(); goroutines > baselineGoroutines+1 {
		t.Fatalf("capacity rejection left goroutines: before=%d after=%d", baselineGoroutines, goroutines)
	}
}

func TestSendEventsSpawnFailureClosesOwnedStdin(t *testing.T) {
	e := New()
	stdin := &stdinCloseSpy{}
	e.stdinPipe = func(*exec.Cmd) (io.WriteCloser, error) { return stdin, nil }
	_, err := e.SendEvents(context.Background(), "spawn-failure", types.Message{Spawn: &types.SpawnArgs{
		Command: "aimux-command-that-does-not-exist",
		Stdin:   "owned writer",
	}}, nil)
	if err == nil {
		t.Fatal("spawn failure unexpectedly succeeded")
	}
	if closes := stdin.closes.Load(); closes != 1 {
		t.Fatalf("owned stdin close calls = %d, want 1", closes)
	}
}

func waitForCapacityRejectionHelperPID(t *testing.T, readyFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if content, err := os.ReadFile(readyFile); err == nil {
			pid, parseErr := strconv.Atoi(string(content))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("spawned helper did not report its PID")
	return 0
}
