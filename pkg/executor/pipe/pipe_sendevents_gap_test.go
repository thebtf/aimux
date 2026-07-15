package pipe_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

// These tests cover accepted T018 CR-002 review findings for the native
// (EventExecutor) SendEvents path:
//   - CompletionPattern must be honored with byte-safe incremental stdout
//     line matching equivalent to legacy IOManager semantics, killing the
//     owned tree and returning before the configured timeout while
//     retaining pre-terminal bytes.
//   - Message.SystemPrompt must reach parity with Send/SendStream: prepended
//     when stdin still carries the default message content, left
//     byte-identical when the caller supplied an explicit stdin override.

const (
	markerThenLingerHelperEnv = "AIMUX_PIPE_MARKER_LINGER_HELPER"
	stdinCaptureHelperEnv     = "AIMUX_PIPE_STDIN_CAPTURE_HELPER"
)

// TestPipeExecutorMarkerThenLingerHelper is re-executed in an isolated
// subprocess (never run directly by `go test`). It writes a completion
// marker to stdout immediately, then keeps running far past any
// completion-pattern timeout under test, so a correct SendEvents
// implementation must detect the marker and terminate the owned process
// tree early rather than waiting for a natural exit or a timeout.
func TestPipeExecutorMarkerThenLingerHelper(t *testing.T) {
	if os.Getenv(markerThenLingerHelperEnv) != "1" {
		return
	}
	if _, err := fmt.Fprintln(os.Stdout, "MARKER:done"); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	time.Sleep(30 * time.Second)
}

// TestPipeExecutorStdinCaptureHelper is re-executed in an isolated
// subprocess (never run directly by `go test`). It echoes stdin back to
// stderr unchanged so the parent test can assert on the exact bytes
// SendEvents delivered. stderr is used instead of stdout because the
// re-exec'd test binary's own testing.Main writes a trailing "PASS\n"
// summary line to stdout after this function returns, which would
// otherwise contaminate a stdout-based byte comparison.
func TestPipeExecutorStdinCaptureHelper(t *testing.T) {
	if os.Getenv(stdinCaptureHelperEnv) != "1" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if _, err := os.Stderr.Write(data); err != nil {
		t.Fatalf("write stderr: %v", err)
	}
}

func TestPipeExecutorSendEventsHonorsCompletionPatternBeforeTimeout(t *testing.T) {
	e := pipe.New()
	var mu sync.Mutex
	var stdoutBytes []byte
	sink := types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Channel == "stdout" {
			mu.Lock()
			stdoutBytes = append(stdoutBytes, event.Content...)
			mu.Unlock()
		}
		return true
	})

	started := time.Now()
	resp, err := e.SendEvents(context.Background(), "marker-linger", types.Message{Spawn: &types.SpawnArgs{
		Command: os.Args[0],
		Args: []string{
			"-test.run=^TestPipeExecutorMarkerThenLingerHelper$",
			"-test.count=1",
		},
		Env: map[string]string{
			markerThenLingerHelperEnv: "1",
		},
		CompletionPattern: "MARKER:done",
		TimeoutSeconds:    6,
	}}, sink)
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("SendEvents: %v", err)
	}
	if resp == nil {
		t.Fatal("response = nil")
	}
	if resp.Error != nil {
		t.Fatalf("resp.Error = %v, want nil — a CompletionPattern match must end the call before the configured timeout", resp.Error)
	}
	if elapsed >= 3*time.Second {
		t.Fatalf("SendEvents took %s, want well under the 6s timeout via CompletionPattern early termination", elapsed)
	}
	if !strings.Contains(resp.Content, "MARKER:done") {
		t.Fatalf("resp.Content = %q, want the marker bytes emitted before termination retained", resp.Content)
	}
	mu.Lock()
	rawStdout := string(stdoutBytes)
	mu.Unlock()
	if !strings.Contains(rawStdout, "MARKER:done") {
		t.Fatalf("raw stdout events = %q, want marker bytes retained via the raw event sink before the owned tree was killed", rawStdout)
	}
}

func TestPipeExecutorSendEventsPrependsSystemPromptForDefaultStdin(t *testing.T) {
	e := pipe.New()
	resp, err := e.SendEvents(context.Background(), "sysprompt-default", types.Message{
		Content:      "hello",
		SystemPrompt: "follow the rules",
		Spawn: &types.SpawnArgs{
			Command: os.Args[0],
			Args: []string{
				"-test.run=^TestPipeExecutorStdinCaptureHelper$",
				"-test.count=1",
			},
			Env: map[string]string{
				stdinCaptureHelperEnv: "1",
			},
			Stdin:          "hello",
			TimeoutSeconds: 5,
		},
	}, nil)
	if err != nil {
		t.Fatalf("SendEvents: %v", err)
	}
	want := "System: follow the rules\n\nhello"
	if resp.Stderr != want {
		t.Fatalf("stdin received by the child = %q, want %q (native SendEvents must prepend SystemPrompt for default stdin, matching Send/SendStream parity)", resp.Stderr, want)
	}
}

func TestPipeExecutorSendEventsPreservesExplicitStdinOverrideByteIdentical(t *testing.T) {
	e := pipe.New()
	resp, err := e.SendEvents(context.Background(), "sysprompt-override", types.Message{
		Content:      "hello",
		SystemPrompt: "follow the rules",
		Spawn: &types.SpawnArgs{
			Command: os.Args[0],
			Args: []string{
				"-test.run=^TestPipeExecutorStdinCaptureHelper$",
				"-test.count=1",
			},
			Env: map[string]string{
				stdinCaptureHelperEnv: "1",
			},
			Stdin:          "explicit override payload",
			TimeoutSeconds: 5,
		},
	}, nil)
	if err != nil {
		t.Fatalf("SendEvents: %v", err)
	}
	if resp.Stderr != "explicit override payload" {
		t.Fatalf("stdin received by the child = %q, want the explicit override unchanged (byte-identical, no SystemPrompt prepend)", resp.Stderr)
	}
}
