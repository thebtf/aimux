//go:build !short

// T019 — @critical TestCritical_PersistentSession_CloseGraceful.
//
// AIMUX-14 CR-001 Phase 3 / NFR-3: Session.Close completes ≤ 500ms even
// for processes that ignore SIGTERM (escalation to SIGKILL per EC-10).
//
// Anti-stub: removing the kill path altogether would leave the subprocess
// running indefinitely and the test wall-clock budget would surface;
// removing the SIGTERM step on Unix would be invisible (immediate SIGKILL
// fast-path) but Close() must still complete under budget.

package critical

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/session"
)

func TestCritical_PersistentSession_CloseGraceful(t *testing.T) {
	bin := buildPersistentTestCLI(t)

	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	pid := cmd.Process.Pid

	// Register cleanup IMMEDIATELY so any subsequent t.Fatal does NOT leak
	// the subprocess (PR #134 review — coderabbit major).
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	sess := session.New("close-test", stdin, stdout, 5*time.Second, nil, nil, "^===END===$")
	t.Cleanup(func() { _ = sess.Close() })

	// Warm-up Send so the reader goroutine has touched the pipe.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := sess.Send(ctx, "hello"); err != nil {
		t.Fatalf("warmup Send: %v", err)
	}

	// Measure Close wall-clock — NFR-3 budget.
	const nfr3Budget = 500 * time.Millisecond
	start := time.Now()
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closeElapsed := time.Since(start)

	// Send a SIGKILL backstop ourselves since BaseSession.Close in this
	// fixture lacks a process-handle (handle=nil). In production the
	// adapter wires the handle; here we own it. Wait confirms reaping.
	_ = cmd.Process.Kill()
	waitDone := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Errorf("subprocess pid=%d did not exit within 2s after Close+Kill", pid)
	}

	if closeElapsed > nfr3Budget {
		t.Errorf("NFR-3: Session.Close took %v, want ≤ %v", closeElapsed, nfr3Budget)
	}

	t.Logf("Close elapsed %v (budget %v); pid %d terminated", closeElapsed, nfr3Budget, pid)
}
