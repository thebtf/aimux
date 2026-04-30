//go:build !short

// T013 — NFR-6 memory ceiling + NFR-7 sustained throughput tests for
// persistent CLI sessions (AIMUX-14 CR-001 Phase 1).
//
// NFR-6: aggregate live-session stdout buffer growth bounded ≤ 100MB on
// single-operator workstation deployment. Per-Session rolling cap (1MB,
// EC-5) provides primary defense; this test asserts aggregate growth stays
// well under the daemon-wide ceiling under realistic burst load.
//
// NFR-7: single Session sustained throughput ≥ 50 Sends/sec (limited by
// CLI processing speed, not aimux overhead). Multi-Session aggregate ≥
// 200 Sends/sec on 4-core dev workstation.
//
// Anti-stub: timing/memory measurements are real (not constant) — replacing
// the test body with `_ = io.Discard` would produce zero throughput and the
// test would fail.

package critical

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/session"
)

// spawnTestSession compiles persistent_testcli (cached via t.TempDir reuse
// across helpers in this package) and starts a fresh subprocess + session.
// Returned cleanup MUST be called via defer.
func spawnTestSession(t *testing.T, bin string, id string) (*session.BaseSession, *exec.Cmd, func()) {
	t.Helper()
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

	sess := session.New(id, stdin, stdout, 5*time.Second, nil, nil, `^===END===$`)
	cleanup := func() {
		_ = sess.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	return sess, cmd, cleanup
}

func TestCritical_PersistentSession_MemoryCeiling(t *testing.T) {
	const (
		sessionCount   = 5    // scaled-down from spec 10 for CI sanity
		sendsPerSess   = 200  // scaled-down from spec 1000
		nfr6CeilingMB  = 100
	)

	bin := buildPersistentTestCLI(t)

	var heapBefore runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&heapBefore)

	sessions := make([]*session.BaseSession, sessionCount)
	cleanups := make([]func(), sessionCount)
	for i := 0; i < sessionCount; i++ {
		sess, _, cleanup := spawnTestSession(t, bin, "mem-"+strconv.Itoa(i))
		sessions[i] = sess
		cleanups[i] = cleanup
	}
	defer func() {
		for _, c := range cleanups {
			c()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for j := 0; j < sendsPerSess; j++ {
		for i := 0; i < sessionCount; i++ {
			if _, err := sessions[i].Send(ctx, "mem-test-"+strconv.Itoa(j)); err != nil {
				t.Fatalf("Send sess[%d] turn %d: %v", i, j, err)
			}
		}
	}

	var heapAfter runtime.MemStats
	runtime.ReadMemStats(&heapAfter)

	growthBytes := int64(heapAfter.HeapInuse) - int64(heapBefore.HeapInuse)
	growthMB := growthBytes / (1024 * 1024)

	if growthMB > nfr6CeilingMB {
		t.Errorf("NFR-6: aggregate heap growth %d MB across %d sessions × %d sends, "+
			"want ≤ %d MB ceiling", growthMB, sessionCount, sendsPerSess, nfr6CeilingMB)
	}

	t.Logf("aggregate heap growth: %d MB (sessions: %d, sends/sess: %d, ceiling: %d MB)",
		growthMB, sessionCount, sendsPerSess, nfr6CeilingMB)
}

func TestCritical_PersistentSession_SustainedThroughput(t *testing.T) {
	bin := buildPersistentTestCLI(t)

	sess, _, cleanup := spawnTestSession(t, bin, "throughput-test")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Warm-up.
	if _, err := sess.Send(ctx, "warmup"); err != nil {
		t.Fatalf("warmup: %v", err)
	}

	const window = 5 * time.Second
	const nfr7MinSendsPerSec = 50

	deadline := time.Now().Add(window)
	count := 0
	for time.Now().Before(deadline) {
		if _, err := sess.Send(ctx, "tput-"+strconv.Itoa(count)); err != nil {
			t.Fatalf("Send #%d: %v", count, err)
		}
		count++
	}

	rate := float64(count) / window.Seconds()
	if rate < nfr7MinSendsPerSec {
		t.Errorf("NFR-7: single-Session throughput %.1f Sends/sec over %v, "+
			"want ≥ %d Sends/sec", rate, window, nfr7MinSendsPerSec)
	}

	t.Logf("single-Session throughput: %.1f Sends/sec (count %d in %v, min %d)",
		rate, count, window, nfr7MinSendsPerSec)
}

func TestCritical_PersistentSession_MultiSessionAggregateThroughput(t *testing.T) {
	const (
		sessionCount        = 4 // 4-core baseline per spec NFR-7
		nfr7AggregateMin    = 200
	)

	bin := buildPersistentTestCLI(t)

	sessions := make([]*session.BaseSession, sessionCount)
	cleanups := make([]func(), sessionCount)
	for i := 0; i < sessionCount; i++ {
		sess, _, cleanup := spawnTestSession(t, bin, "agg-"+strconv.Itoa(i))
		sessions[i] = sess
		cleanups[i] = cleanup
	}
	defer func() {
		for _, c := range cleanups {
			c()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Warm-up each.
	for i, s := range sessions {
		if _, err := s.Send(ctx, "warmup"); err != nil {
			t.Fatalf("warmup sess[%d]: %v", i, err)
		}
	}

	const window = 5 * time.Second
	deadline := time.Now().Add(window)

	var wg sync.WaitGroup
	counts := make([]int, sessionCount)
	for i := 0; i < sessionCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c := 0
			for time.Now().Before(deadline) {
				if _, err := sessions[idx].Send(ctx, "agg-"+strconv.Itoa(c)); err != nil {
					t.Errorf("sess[%d] Send #%d: %v", idx, c, err)
					return
				}
				c++
			}
			counts[idx] = c
		}(i)
	}
	wg.Wait()

	total := 0
	for _, c := range counts {
		total += c
	}
	rate := float64(total) / window.Seconds()
	if rate < nfr7AggregateMin {
		t.Errorf("NFR-7 aggregate: %d sessions sustained %.1f Sends/sec total, "+
			"want ≥ %d Sends/sec", sessionCount, rate, nfr7AggregateMin)
	}

	t.Logf("aggregate throughput across %d sessions: %.1f Sends/sec "+
		"(total %d in %v, min %d)", sessionCount, rate, total, window, nfr7AggregateMin)
}
