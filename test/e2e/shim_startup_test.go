// Package e2e contains end-to-end tests launched against the aimux binary.
package e2e

// TestShim_NoSQLiteWrites is the NFR-3 regression gate for AIMUX-6.
//
// Spec traceability: FR-2, FR-3, NFR-3, US1-AC4, US2-AC1, SC-2, C5
// Task: T011 (Phase 3 — test/e2e/shim_startup_test.go)
//
// What it verifies:
//
//	A shim-mode aimux process MUST NOT write to sessions.db, sessions.db-wal, or
//	sessions.db-shm during its entire lifetime. Any write event on those three
//	paths during shim lifetime is a hard failure — it means daemon-level init
//	(SQLite open, migration, ReconcileOnStartup) leaked into the shim code path,
//	exactly the bug class that caused active jobs to be aborted by shim-triggered
//	reconcile (reproducer documented in spec §Context "What exists today").
//
// Anti-stub guard (REQUIRED per tasks.md T011):
//
//	If runShim's body is modified to call session.NewStore(dbPath) at the start
//	of its execution, this test MUST detect a fsnotify.Write event and fail with
//	"NFR-3 VIOLATION: observed N write event(s) on sessions.db paths during shim
//	lifetime". Do NOT weaken this assertion without a spec AMEND on NFR-3.
//
// Cross-platform notes:
//
//	Windows (ReadDirectoryChangesW backend):
//	  SQLite WAL checkpointing by the *daemon* may emit Write events on
//	  sessions.db while the shim is alive. These events come from the daemon
//	  process, not the shim. Since fsnotify does not expose the originating PID,
//	  we cannot filter by PID directly.
//
//	  Mitigation applied: shimActive flag gates event counting to the exact
//	  window between shim spawn and shim exit. The shim performs 3 lightweight
//	  tools/list requests that do not touch SQLite in a correct implementation.
//	  The window is < 2 seconds. WAL auto-checkpoint fires every 1000 pages
//	  written (default) — unlikely during a 2-second idle window after daemon
//	  startup is complete and no new writes are occurring.
//
//	  If a daemon WAL checkpoint false-positive is observed in CI:
//	    TODO(UR-2): investigate if muxcore exposes a "shim connected" callback
//	    to narrow the measurement window further. Tracked as AIMUX-6 spec open
//	    question UR-2 (not yet filed as of 2026-04-21).
//
//	Linux (inotify backend):
//	  Inotify delivers events per-inode with no WAL checkpoint false positives
//	  during the measurement window.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TestShim_NoSQLiteWrites is the NFR-3 regression gate.
//
// Phases (per T011 AC):
//  1. Setup: create tempdir, write config with db_path + log_file pointing there.
//  2. Build binary once (shared via buildBinary helper).
//  3. Spawn daemon (with --muxcore-daemon flag), wait for "ready — serving MCP" log line.
//  4. Attach fsnotify.Watcher to tmpDir, filter events to sessions.db{,-wal,-shm}.
//  5. Spawn shim (no --muxcore-daemon), activate write counting, feed MCP requests.
//  6. Read 4 responses (initialize + 3 × tools/list).
//  7. Close shim stdin → shim exits. Deactivate write counting.
//  8. Wait 1 second for filesystem event drain.
//  9. ASSERT zero Write events on sessions.db paths.
//  10. Cleanup daemon via SIGTERM / kill fallback.
func TestShim_NoSQLiteWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("TestShim_NoSQLiteWrites requires a real daemon process — skipped in short mode")
	}

	// ---- Phase 1: Setup ----

	tmpDir := t.TempDir()
	binary := buildBinary(t)
	testcliBin := buildTestCLI(t)
	configDir, dbPath, logPath := shimTestWriteConfig(t, tmpDir)

	// Unique engine name prevents collisions with any production aimux daemon
	// or parallel test runs. Uses the random suffix from t.TempDir().
	engineName := "aimux-test-nfr3-" + filepath.Base(tmpDir)

	// Prepend testcli directory to PATH so registry.Probe() finds the "testcli"
	// binary (which our shimTestWriteConfig registers as the "codex" CLI profile).
	// This ensures EnabledCLIs() is non-empty and the daemon does not exit early.
	testcliDir := filepath.Dir(testcliBin)
	enrichedPath := testcliDir + string(os.PathListSeparator) + os.Getenv("PATH")

	// ---- Phase 3: Spawn daemon ----
	//
	// --muxcore-daemon makes detectMode return ModeDaemon, which runs the full
	// init path (SQLite open, migrations, ReconcileOnStartup, LoomEngine, etc.).
	daemonEnv := append(os.Environ(),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME="+engineName,
		"AIMUX_WARMUP=false", // skip CLI warmup to reduce startup time
		"PATH="+enrichedPath,
	)

	daemonCmd := exec.Command(binary, "--muxcore-daemon")
	daemonCmd.Env = daemonEnv
	// Redirect daemon stderr to test log so failures are diagnosable.
	daemonStderr, daemonStderrW, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("daemon stderr pipe: %v", pipeErr)
	}
	daemonCmd.Stderr = daemonStderrW

	if err := daemonCmd.Start(); err != nil {
		daemonStderr.Close()
		daemonStderrW.Close()
		t.Fatalf("start daemon: %v", err)
	}
	daemonStderrW.Close() // parent closes write end after Start
	go forwardLines(t, daemonStderr, "[daemon stderr]")

	t.Cleanup(func() {
		if daemonCmd.Process == nil {
			return
		}
		// Send interrupt signal (SIGINT/SIGTERM); fall back to kill on timeout.
		// On Windows, os.Interrupt maps to CTRL_BREAK_EVENT via os.Process.Signal.
		_ = daemonCmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			daemonCmd.Wait() //nolint:errcheck
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			daemonCmd.Process.Kill() //nolint:errcheck
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Log("daemon Wait() did not return within 2s after Kill")
			}
		}
	})

	// Wait for daemon readiness. "ready — serving MCP" is written to log_file
	// (pkg/logger) after engine.New succeeds. Timeout 20s: daemon needs time
	// for SQLite migration + registry probe even with AIMUX_WARMUP=false.
	t.Log("waiting for daemon readiness...")
	if err := shimTailLogForReady(t, logPath, "ready — serving MCP", 20*time.Second); err != nil {
		t.Fatalf("daemon readiness: %v\n(check log at %s)", err, logPath)
	}
	t.Log("daemon is ready")

	// ---- Phase 4: Attach fsnotify watcher ----
	//
	// The three sessions.db files may not exist yet (SQLite creates them lazily).
	// Watch the parent directory (tmpDir) and filter events by basename.
	// This is the recommended cross-platform approach in fsnotify docs.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("fsnotify.NewWatcher: %v", err)
	}
	defer watcher.Close()

	if err := watcher.Add(tmpDir); err != nil {
		t.Fatalf("watcher.Add(%q): %v", tmpDir, err)
	}

	// dbBaseNames is the set of file basenames we care about.
	dbBaseNames := map[string]bool{
		"sessions.db":     true,
		"sessions.db-wal": true,
		"sessions.db-shm": true,
	}

	// writeCount counts Write events observed during shim lifetime only.
	// writeLog accumulates event descriptions for failure diagnostics.
	var writeCount atomic.Int32
	type writeLogEntry struct {
		base string
		at   string
	}
	writeLogCh := make(chan writeLogEntry, 64)

	// shimActive gates event counting. Set true before shim spawn, false after.
	var shimActive atomic.Bool

	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if !shimActive.Load() {
					continue
				}
				base := filepath.Base(event.Name)
				if !dbBaseNames[base] {
					continue
				}
				if event.Has(fsnotify.Write) {
					writeCount.Add(1)
					select {
					case writeLogCh <- writeLogEntry{base: base, at: time.Now().Format("15:04:05.000")}:
					default:
					}
				}
			case watcherErr, ok := <-watcher.Errors:
				if !ok {
					return
				}
				t.Logf("fsnotify watcher error (non-fatal): %v", watcherErr)
			}
		}
	}()

	// ---- Phase 5: Spawn shim ----
	//
	// No --muxcore-daemon flag → detectMode returns ModeShim → runShim() is called.
	// runShim MUST NOT call session.NewStore, driver.NewRegistry, or any other
	// code path that opens sessions.db.
	shimEnv := append(os.Environ(),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME="+engineName,
	)

	shimStdinR, shimStdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("shim stdin pipe: %v", err)
	}
	defer shimStdinR.Close()

	shimStdoutR, shimStdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("shim stdout pipe: %v", err)
	}
	defer shimStdoutR.Close()

	shimCmd := exec.Command(binary) // no --muxcore-daemon = shim mode
	shimCmd.Env = shimEnv
	shimCmd.Stdin = shimStdinR
	shimCmd.Stdout = shimStdoutW

	shimStderrR, shimStderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("shim stderr pipe: %v", err)
	}
	shimCmd.Stderr = shimStderrW

	if err := shimCmd.Start(); err != nil {
		shimStdinR.Close()
		shimStdinW.Close()
		shimStdoutW.Close()
		shimStderrR.Close()
		shimStderrW.Close()
		t.Fatalf("start shim: %v", err)
	}
	shimStdoutW.Close() // parent closes write-end so our reads can detect EOF
	shimStderrW.Close()
	go forwardLines(t, shimStderrR, "[shim stderr]")

	// Activate write-event counting NOW — shim process is running.
	shimActive.Store(true)

	shimReader := bufio.NewReader(shimStdoutR)

	// ---- Phase 6: MCP handshake + 3 requests ----
	//
	// MCP protocol requires initialize before any tool calls.
	// notifications/initialized is a one-way notification (no id, no response).
	// We read 4 responses: the initialize response + 3 tools/list responses.
	mcpRequests := []string{
		jsonRPCRequest(1, "initialize", map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "nfr3-test", "version": "1.0"},
		}),
		jsonRPCNotification("notifications/initialized"),
		jsonRPCRequest(2, "tools/list", map[string]any{}),
		jsonRPCRequest(3, "tools/list", map[string]any{}),
		jsonRPCRequest(4, "tools/list", map[string]any{}),
	}
	for _, req := range mcpRequests {
		if _, err := fmt.Fprint(shimStdinW, req); err != nil {
			t.Fatalf("write to shim stdin: %v", err)
		}
	}

	// Read 4 responses (initialize + 3 × tools/list).
	// readResponse skips server-initiated notifications (no id field).
	for i := 1; i <= 4; i++ {
		resp, readErr := readResponse(shimReader, 10*time.Second)
		if readErr != nil {
			t.Fatalf("shim response %d of 4: %v", i, readErr)
		}
		t.Logf("shim response %d: id=%v", i, resp["id"])
	}

	// ---- Phase 7: Close shim stdin → shim exits ----
	shimStdinW.Close()

	shimExited := make(chan error, 1)
	go func() { shimExited <- shimCmd.Wait() }()
	select {
	case shimErr := <-shimExited:
		if shimErr != nil {
			t.Logf("shim exit: %v (context cancel is expected on stdin close)", shimErr)
		}
	case <-time.After(6 * time.Second):
		t.Log("shim did not exit within 6s after stdin close; sending kill")
		shimCmd.Process.Kill() //nolint:errcheck
		// Wait via the channel — calling shimCmd.Wait() here directly would
		// race with the goroutine on line 308 that already owns Wait().
		<-shimExited
	}

	// Deactivate write counting immediately after shim exits so subsequent
	// daemon background activity (WAL checkpoint, GC snapshot) is excluded.
	shimActive.Store(false)

	// ---- Phase 8: 1-second drain ----
	// Allow any in-flight OS/kernel filesystem events to arrive at the watcher.
	time.Sleep(1 * time.Second)

	// Close watcher and wait for event goroutine to flush.
	watcher.Close()
	select {
	case <-watcherDone:
	case <-time.After(3 * time.Second):
		t.Log("fsnotify event goroutine did not exit in 3s")
	}

	// Drain any remaining log entries from writeLogCh.
	close(writeLogCh)
	var writeLogLines []string
	for entry := range writeLogCh {
		writeLogLines = append(writeLogLines, fmt.Sprintf("Write on %s at %s", entry.base, entry.at))
	}

	// ---- Phase 9: ASSERT zero Write events ----
	//
	// ANTI-STUB GATE: this assertion MUST fire if runShim is modified to call
	// session.NewStore(dbPath) — SQLite open triggers a fsnotify.Write event on
	// sessions.db as migrations create/populate the schema.
	count := writeCount.Load()
	if count > 0 {
		t.Errorf("NFR-3 VIOLATION: observed %d write event(s) on sessions.db paths during shim lifetime.\n"+
			"Shim MUST NOT open SQLite. See spec FR-2, NFR-3, AIMUX-6.\n\n"+
			"Write events observed:\n  %s\n\n"+
			"Root cause: runShim (cmd/aimux/shim.go) or a code path it calls is\n"+
			"invoking daemon-level init that opens sessions.db. Verify that\n"+
			"detectMode returns ModeShim and that the shim branch in run() returns\n"+
			"via runShim() without touching aimuxServer.NewDaemon().",
			count, strings.Join(writeLogLines, "\n  "))
	}

	// Diagnostics on failure: dump daemon log tail + sessions.db stats.
	if t.Failed() {
		if logBytes, readErr := os.ReadFile(logPath); readErr == nil {
			lines := strings.Split(string(logBytes), "\n")
			start := len(lines) - 30
			if start < 0 {
				start = 0
			}
			t.Logf("daemon log (last 30 lines):\n%s", strings.Join(lines[start:], "\n"))
		}
		if fi, statErr := os.Stat(dbPath); statErr == nil {
			t.Logf("sessions.db: size=%d mtime=%s", fi.Size(), fi.ModTime().Format(time.RFC3339Nano))
		}
	}
}

// shimTestWriteConfig creates a minimal aimux config under tmpDir so the daemon
// writes sessions.db and log output to known locations we can observe.
// Returns: configDir (AIMUX_CONFIG_DIR), dbPath (sessions.db), logPath (log file).
func shimTestWriteConfig(t *testing.T, tmpDir string) (configDir, dbPath, logPath string) {
	t.Helper()

	configDir = filepath.Join(tmpDir, "config")
	cliDir := filepath.Join(configDir, "cli.d")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatalf("MkdirAll cli.d: %v", err)
	}

	dbPath = filepath.Join(tmpDir, "sessions.db")
	logPath = filepath.Join(tmpDir, "aimux.log")

	// log_level=info is required to capture the "ready" line.
	// db_path must point to our tmpDir for fsnotify to observe it.
	// On Windows the logger's isNullDevice check handles /dev/null gracefully,
	// but here we need a real file path for tailing.
	//
	// Use filepath.ToSlash so Windows backslash paths survive YAML parsing
	// as unambiguous forward-slash strings. gopkg.in/yaml.v3 accepts both,
	// but forward slashes are safer across all YAML spec interpretations.
	defaultYAML := fmt.Sprintf(`server:
  log_level: info
  log_file: %s
  db_path: %s
  max_concurrent_jobs: 2
  default_timeout_seconds: 10
  max_prompt_bytes: 1048576

roles:
  default:
    cli: codex

circuit_breaker:
  failure_threshold: 3
  cooldown_seconds: 5
  half_open_max_calls: 1
`, filepath.ToSlash(logPath), filepath.ToSlash(dbPath))

	if err := os.WriteFile(filepath.Join(configDir, "default.yaml"), []byte(defaultYAML), 0o644); err != nil {
		t.Fatalf("write default.yaml: %v", err)
	}

	// Write a minimal testcli profile so registry.Probe() finds the "testcli"
	// binary (which is a real executable on all platforms, built by buildTestCLI).
	// The testcli binary must be on PATH when the daemon starts — callers are
	// responsible for prepending its directory to the daemon's PATH env var.
	// We register it as "codex" so routing.NewRouterWithPriority finds a usable CLI.
	testcliDir := filepath.Join(cliDir, "codex")
	if err := os.MkdirAll(testcliDir, 0o755); err != nil {
		t.Fatalf("MkdirAll codex: %v", err)
	}
	testcliProfile := `name: codex
binary: testcli
display_name: Codex (testcli)
command:
  base: testcli codex --json --full-auto
prompt_flag: positional
`
	if err := os.WriteFile(filepath.Join(testcliDir, "profile.yaml"), []byte(testcliProfile), 0o644); err != nil {
		t.Fatalf("write codex profile.yaml: %v", err)
	}

	return configDir, dbPath, logPath
}

// shimTailLogForReady polls logPath waiting for a line containing readySubstring.
// Returns nil when found within timeout, error otherwise.
// Uses file polling (open/seek/scan) — no additional goroutines or watchers.
func shimTailLogForReady(t *testing.T, logPath, readySubstring string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var offset int64

	for time.Now().Before(deadline) {
		f, openErr := os.Open(logPath)
		if openErr != nil {
			// File may not exist yet; daemon is still initialising.
			time.Sleep(25 * time.Millisecond)
			continue
		}

		if _, seekErr := f.Seek(offset, io.SeekStart); seekErr != nil {
			f.Close()
			time.Sleep(25 * time.Millisecond)
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), readySubstring) {
				f.Close()
				return nil
			}
		}
		// Advance offset so the next iteration does not re-scan old lines.
		offset, _ = f.Seek(0, io.SeekCurrent)
		f.Close()

		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %v waiting for %q in %s", timeout, readySubstring, logPath)
}

// forwardLines reads lines from r and logs them via t.Logf with the given prefix.
// Intended for forwarding child-process stderr to the test output.
// Runs in a goroutine and returns when r is closed.
func forwardLines(t *testing.T, r io.Reader, prefix string) {
	t.Helper()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		t.Logf("%s %s", prefix, scanner.Text())
	}
}
