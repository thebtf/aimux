//go:build windows

package pipe

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

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

func TestSendEventsCapacityRejectionDoesNotStartHelper(t *testing.T) {
	e := New()
	for i := 0; i < processEvidenceLimit; i++ {
		id := types.ExecutionID(fmt.Sprintf("live-%d", i))
		if err := e.retainProcessEvidence(id, &executor.ProcessHandle{PID: i + 1, StartedAt: time.Unix(0, int64(i+1))}); err != nil {
			t.Fatal(err)
		}
	}
	stdin := &stdinCloseSpy{}
	e.stdinPipe = func(*exec.Cmd) (io.WriteCloser, error) { return stdin, nil }
	readyFile := t.TempDir() + "\\ready"
	_, err := e.SendEvents(context.Background(), "rejected", types.Message{Spawn: &types.SpawnArgs{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestSendEventsCapacityRejectionHelper$", "-test.count=1"},
		Env: map[string]string{
			capacityRejectionHelperEnv:    "1",
			capacityRejectionReadyFileEnv: readyFile,
		},
		Stdin: "owned writer",
	}}, nil)
	if err == nil {
		t.Fatal("capacity rejection unexpectedly succeeded")
	}
	if _, statErr := os.Stat(readyFile); !os.IsNotExist(statErr) {
		t.Fatalf("capacity-rejected helper performed user-code side effect: stat err=%v", statErr)
	}
	if closes := stdin.closes.Load(); closes != 0 {
		t.Fatalf("capacity rejection opened stdin before admission: close calls=%d", closes)
	}
}

func TestSendEventsDuplicateRejectionDoesNotStartHelper(t *testing.T) {
	e := New()
	if err := e.retainProcessEvidence("duplicate", &executor.ProcessHandle{PID: 1, StartedAt: time.Unix(0, 1)}); err != nil {
		t.Fatal(err)
	}
	readyFile := t.TempDir() + "\\ready"
	_, err := e.SendEvents(context.Background(), "duplicate", types.Message{Spawn: &types.SpawnArgs{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestSendEventsCapacityRejectionHelper$", "-test.count=1"},
		Env: map[string]string{
			capacityRejectionHelperEnv:    "1",
			capacityRejectionReadyFileEnv: readyFile,
		},
	}}, nil)
	if err == nil {
		t.Fatal("duplicate rejection unexpectedly succeeded")
	}
	if _, statErr := os.Stat(readyFile); !os.IsNotExist(statErr) {
		t.Fatalf("duplicate-rejected helper performed user-code side effect: stat err=%v", statErr)
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
