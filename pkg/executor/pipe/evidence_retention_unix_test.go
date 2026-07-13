//go:build !windows

package pipe

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

const unixCapacityRejectionHelperEnv = "AIMUX_PIPE_UNIX_CAPACITY_REJECTION_HELPER"
const unixCapacityRejectionReadyFileEnv = "AIMUX_PIPE_UNIX_CAPACITY_REJECTION_READY_FILE"

func TestSendEventsUnixCapacityRejectionHelper(t *testing.T) {
	if os.Getenv(unixCapacityRejectionHelperEnv) != "1" {
		return
	}
	if readyFile := os.Getenv(unixCapacityRejectionReadyFileEnv); readyFile != "" {
		if err := os.WriteFile(readyFile, []byte("started"), 0o600); err != nil {
			os.Exit(2)
		}
	}
	os.Exit(0)
}

func TestSendEventsUnixCapacityRejectionDoesNotStartHelper(t *testing.T) {
	e := New()
	for i := 0; i < processEvidenceLimit; i++ {
		id := types.ExecutionID(fmt.Sprintf("live-%d", i))
		if err := e.retainProcessEvidence(id, &executor.ProcessHandle{PID: i + 1, StartedAt: time.Unix(0, int64(i+1))}); err != nil {
			t.Fatal(err)
		}
	}
	readyFile := t.TempDir() + "/ready"
	_, err := e.SendEvents(context.Background(), "rejected", types.Message{Spawn: &types.SpawnArgs{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestSendEventsUnixCapacityRejectionHelper$", "-test.count=1"},
		Env: map[string]string{
			unixCapacityRejectionHelperEnv:    "1",
			unixCapacityRejectionReadyFileEnv: readyFile,
		},
	}}, nil)
	if err == nil {
		t.Fatal("capacity rejection unexpectedly succeeded")
	}
	if _, statErr := os.Stat(readyFile); !os.IsNotExist(statErr) {
		t.Fatalf("capacity-rejected helper performed user-code side effect: stat err=%v", statErr)
	}
}

func TestSendEventsUnixDuplicateRejectionDoesNotStartHelper(t *testing.T) {
	e := New()
	if err := e.retainProcessEvidence("duplicate", &executor.ProcessHandle{PID: 1, StartedAt: time.Unix(0, 1)}); err != nil {
		t.Fatal(err)
	}
	readyFile := t.TempDir() + "/ready"
	_, err := e.SendEvents(context.Background(), "duplicate", types.Message{Spawn: &types.SpawnArgs{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestSendEventsUnixCapacityRejectionHelper$", "-test.count=1"},
		Env: map[string]string{
			unixCapacityRejectionHelperEnv:    "1",
			unixCapacityRejectionReadyFileEnv: readyFile,
		},
	}}, nil)
	if err == nil {
		t.Fatal("duplicate rejection unexpectedly succeeded")
	}
	if _, statErr := os.Stat(readyFile); !os.IsNotExist(statErr) {
		t.Fatalf("duplicate-rejected helper performed user-code side effect: stat err=%v", statErr)
	}
}
