// Package e2e: TestMultiProcessLogIntegrity — FR-10 multi-process write
// regression gate (AIMUX-11 centralized logging Phase 5).
//
// Spec: .agent/specs/centralized-logging/spec.md FR-10
// Spawns 1 daemon and N concurrent shims, each emitting >=100 log entries via
// the AIMUX_TEST_EMIT_LINES hook in cmd/aimux/shim.go. Asserts that EVERY
// emitted line lands in the daemon's aimux.log with the correct
// `[shim-?<id>-<id>]` prefix and no truncation or interleaving.
//
// This test would FAIL on the pre-AIMUX-11 codebase where shim opens the log
// file directly (engram#177): concurrent lumberjack writers would interleave
// or truncate lines when the multi-shim burst overlaps. Post-fix the daemon
// is the sole filesystem writer (FR-2) — every shim entry rides the IPC log
// forward path through LogIngester → LocalSink → lumberjack, serialised by
// LocalSink's single drain goroutine.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"
)

const (
	multiProcShims        = 3
	multiProcLinesPerShim = 100
	multiProcSpawnTimeout = 30 * time.Second
)

// testEmitLine matches a single forwarded log line emitted via the
// AIMUX_TEST_EMIT_LINES hook. Captures the session token (group 1) and the
// per-shim sequence "i/N" (groups 2,3) so the verifier can group lines by
// shim and check counts + completeness.
//
// Expected line shape (LocalSink format with [role-pid-sess] prefix):
//
//   <RFC3339> [INFO] [shim-?<sess>-<sess>] test-emit <i>/<N>
var testEmitLine = regexp.MustCompile(`\[shim-\?\S+?-([a-f0-9]{8,})\] test-emit (\d+)/(\d+)$`)

// TestMultiProcessLogIntegrity is the FR-10 regression gate. It ensures:
//
//  1. All N*M log entries from concurrent shims land in the daemon's log file
//     (no entries silently lost to multi-process race or buffer overflow).
//  2. Every line is well-formed (passes the testEmitLine regex) — no
//     truncation, no interleaving of two lines on one row.
//  3. Each shim contributes a distinct session token AND a complete sequence
//     1..M without gaps or duplicates (proves per-shim FIFO from emit through
//     LocalSink's drain goroutine).
func TestMultiProcessLogIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-process integrity test: skipped in -short mode (spawns 3 subprocesses, ~5s wall time)")
	}

	// Phase 1: build binary + minimal config.
	binPath := buildLatencyBinary(t)
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "aimux.log")
	configDir := writeLatencyTestConfig(t, tmpDir, logFile)

	// Phase 2: start daemon under a unique engine name to isolate from any
	// other test daemon and from any production aimux.
	daemonCmd := startMultiProcDaemon(t, binPath, configDir)
	t.Logf("daemon PID=%d", daemonCmd.Process.Pid)
	waitForLogPattern(t, logFile, daemonReadyPattern, 30*time.Second)
	t.Logf("daemon ready; spawning %d shims × %d lines", multiProcShims, multiProcLinesPerShim)

	// Phase 3: spawn shims concurrently. Every shim runs the test-emit hook
	// via AIMUX_TEST_EMIT_LINES, then exits (eng.Run returns when ctx is
	// cancelled by the emit-watcher goroutine in shim.go).
	var wg sync.WaitGroup
	shimErrs := make(chan error, multiProcShims)
	// Per-shim CLAUDE_SESSION_ID so the [shim-?<sess>-<sess>] tag in the
	// daemon log line is unique per shim. FR-5 derives sess from
	// CLAUDE_SESSION_ID first; without distinct values all three shims would
	// fall back to the same muxcore project-hash token (project ID is
	// hash(CWD), which is identical here).
	sessionIDs := []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-shim01",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-shim02",
		"cccccccccccccccccccccccccccccccccccc-shim03",
	}
	for i := 0; i < multiProcShims; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stderrPath := filepath.Join(tmpDir, fmt.Sprintf("shim-%02d.stderr", idx))
			cmd := spawnMultiProcShim(t, binPath, configDir, stderrPath, multiProcLinesPerShim, sessionIDs[idx])
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					shimErrs <- fmt.Errorf("shim %d wait: %v", idx, err)
				}
			case <-time.After(multiProcSpawnTimeout):
				cmd.Process.Kill()
				shimErrs <- fmt.Errorf("shim %d: did not exit within %v", idx, multiProcSpawnTimeout)
			}
		}(i)
	}
	wg.Wait()
	close(shimErrs)
	for err := range shimErrs {
		t.Fatalf("%v", err)
	}

	// Phase 4: small drain window so the daemon's LocalSink async channel
	// flushes the last forwarded entries to lumberjack before we read.
	time.Sleep(500 * time.Millisecond)

	// Phase 5: read the full daemon log and tally per-session counts.
	contents, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read daemon log: %v", err)
	}
	lines := splitLines(string(contents))

	type seqState struct {
		seen      map[int]bool
		total     int
		duplicate int
	}
	bySession := make(map[string]*seqState)
	malformed := 0
	emitTotal := 0
	for _, ln := range lines {
		// Quick filter: only test-emit lines are subject to FR-10 invariants.
		// The daemon log also contains daemon-side entries (CLI discovery,
		// session connect, etc.) — those are not part of this regression's
		// counting set.
		if !regexp.MustCompile(`test-emit \d+/\d+`).MatchString(ln) {
			continue
		}
		emitTotal++
		m := testEmitLine.FindStringSubmatch(ln)
		if m == nil {
			malformed++
			t.Logf("MALFORMED: %q", ln)
			continue
		}
		sess := m[1]
		seq := atoi(m[2])
		// Total (m[3]) is constant multiProcLinesPerShim — sanity check.
		if atoi(m[3]) != multiProcLinesPerShim {
			t.Errorf("session %s: line %q has unexpected total %s (want %d)",
				sess, ln, m[3], multiProcLinesPerShim)
		}
		state, ok := bySession[sess]
		if !ok {
			state = &seqState{seen: make(map[int]bool, multiProcLinesPerShim)}
			bySession[sess] = state
		}
		if state.seen[seq] {
			state.duplicate++
		} else {
			state.seen[seq] = true
		}
		state.total++
	}

	t.Logf("scan summary: total emit lines=%d, malformed=%d, distinct sessions=%d",
		emitTotal, malformed, len(bySession))

	// Assertions
	if malformed > 0 {
		t.Errorf("FR-10 FAIL: %d malformed test-emit lines (truncation or interleave) — daemon is NOT the sole writer or LocalSink drain serialisation broke",
			malformed)
	}
	if len(bySession) != multiProcShims {
		t.Errorf("FR-10 FAIL: expected %d distinct shim sessions, got %d (sessions=%v)",
			multiProcShims, len(bySession), keys(bySession))
	}
	for sess, state := range bySession {
		if state.duplicate > 0 {
			t.Errorf("FR-10 FAIL: session %s: %d duplicate sequence numbers (FIFO violated)",
				sess, state.duplicate)
		}
		if state.total != multiProcLinesPerShim {
			t.Errorf("FR-10 FAIL: session %s: got %d emit lines, want %d (lossy IPC forward)",
				sess, state.total, multiProcLinesPerShim)
		}
		// Gap check: every i in [1..N] should appear exactly once.
		missing := 0
		for i := 1; i <= multiProcLinesPerShim; i++ {
			if !state.seen[i] {
				missing++
			}
		}
		if missing > 0 {
			t.Errorf("FR-10 FAIL: session %s: %d missing sequence numbers (gap in 1..%d)",
				sess, missing, multiProcLinesPerShim)
		}
	}
	if !t.Failed() {
		t.Logf("FR-10 PASS: %d shims × %d lines = %d entries, all delivered with correct prefix and no race",
			multiProcShims, multiProcLinesPerShim, multiProcShims*multiProcLinesPerShim)
	}
}

// startMultiProcDaemon launches the daemon under a dedicated engine name and
// registers cleanup. Mirrors startLatencyDaemon but with isolated naming so
// concurrent test runs don't collide.
func startMultiProcDaemon(t *testing.T, binPath, configDir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binPath, "--muxcore-daemon")
	cmd.Env = append(filterEnv(os.Environ(), "MCP_MUX_SESSION_ID"),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME=aimux-multiproc-test",
		"AIMUX_WARMUP=false",
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start multiproc daemon: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	return cmd
}

// spawnMultiProcShim launches a shim with the AIMUX_TEST_EMIT_LINES hook
// active so it emits emitLines log entries after IPC handshake, then exits.
// claudeSessionID is propagated via CLAUDE_SESSION_ID so the daemon assigns a
// distinct session tag per shim (FR-5 derives sess from this env var).
func spawnMultiProcShim(t *testing.T, binPath, configDir, stderrPath string, emitLines int, claudeSessionID string) *exec.Cmd {
	t.Helper()
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create shim stderr file: %v", err)
	}
	t.Cleanup(func() { stderrFile.Close() })

	cmd := exec.Command(binPath)
	cmd.Env = append(filterEnv(os.Environ(), "MCP_MUX_SESSION_ID", "CLAUDE_SESSION_ID"),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME=aimux-multiproc-test",
		fmt.Sprintf("AIMUX_TEST_EMIT_LINES=%d", emitLines),
		"CLAUDE_SESSION_ID="+claudeSessionID,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("shim stdin pipe: %v", err)
	}
	cmd.Stdout = nil
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn shim: %v", err)
	}
	stdin.Close()
	return cmd
}

// splitLines splits s on '\n' boundaries while tolerating trailing '\r' from
// Windows line endings. Empty trailing element from final newline is dropped.
func splitLines(s string) []string {
	out := make([]string, 0, 256)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			end := i
			if end > start && s[end-1] == '\r' {
				end--
			}
			out = append(out, s[start:end])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// atoi is a simple panic-free integer parser for known-numeric regex captures.
// On unparseable input returns -1, which always fails the [1..N] sequence
// check below — making the failure mode loud and explicit.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// keys returns the keys of a map in undefined order — used only for error
// diagnostic output, so iteration order does not matter.
func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
