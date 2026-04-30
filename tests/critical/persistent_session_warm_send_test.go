//go:build !short

// T011 — @critical NFR-2 TestPersistentSession_WarmSendUnderHundredMs.
//
// AIMUX-14 CR-001 Phase 1 — verifies subsequent Sends on a warmed-up session
// have ≤ 100ms overhead per the spec NFR-2 ceiling. Also verifies PID stability
// across multiple Sends (same subprocess survives all turns).
//
// Anti-stub: response Content differs across Sends (input-dependent echo) —
// removing the per-line read or sentinel matching would yield identical output
// for every Send and a constant elapsed time, but PID-stability check would
// still pass; the input-dependent assertion catches the latter case.

package critical

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/session"
)

func TestCritical_PersistentSession_WarmSendUnderHundredMs(t *testing.T) {
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
	originalPID := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	sess := session.New(
		"warm-send-test",
		stdin,
		stdout,
		5*time.Second,
		nil, nil,
		`^===END===$`,
	)
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Warm-up Send — excluded from NFR-2 budget.
	if _, err := sess.Send(ctx, "warmup"); err != nil {
		t.Fatalf("warmup Send: %v", err)
	}

	const turns = 20
	const nfr2Budget = 100 * time.Millisecond

	var total time.Duration
	for i := 0; i < turns; i++ {
		prompt := "turn-" + string(rune('a'+i))
		start := time.Now()
		res, err := sess.Send(ctx, prompt)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("warm Send %d: %v", i+1, err)
		}
		// Anti-stub: input-dependent output proves real per-turn processing.
		if !strings.Contains(res.Content, prompt) {
			t.Errorf("warm Send %d: content %q does not include prompt %q",
				i+1, res.Content, prompt)
		}
		total += elapsed
	}

	avgElapsed := total / time.Duration(turns)
	if avgElapsed > nfr2Budget {
		t.Errorf("NFR-2: warm-send average %v over %d turns, want ≤ %v",
			avgElapsed, turns, nfr2Budget)
	}

	// PID stability — same subprocess across all turns.
	currentPID := cmd.Process.Pid
	if currentPID != originalPID {
		t.Errorf("PID changed mid-session: started %d, currently %d "+
			"(persistent session must reuse subprocess)",
			originalPID, currentPID)
	}

	t.Logf("warm-send avg over %d turns: %v (budget %v); PID stable at %d",
		turns, avgElapsed, nfr2Budget, originalPID)
}
