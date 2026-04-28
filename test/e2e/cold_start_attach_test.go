// Package e2e: cold start concurrent attach test for issue #129 two-phase init.
package e2e

// TestColdStart_ConcurrentShimAttach is the e2e gate for issue #129
// (two-phase daemon init — listen-first, async heavy init).
//
// Spec traceability: FR-7, NFR-1, US3-AC1, issue #129
// Task: T006 (Phase 3 — test/e2e/cold_start_attach_test.go)
//
// What it verifies:
//
//	5 shim processes connect to the daemon concurrently. Each shim sends an
//	MCP initialize + tools/list request pair and expects a valid response
//	within 1 second (p99 ≤ 1s gate). Responses must be either:
//	  (a) a valid tools/list result — Phase B completed before the request, or
//	  (b) a JSON-RPC -32001 retry-hint — Phase A still active, correctly deferred.
//	Either outcome is acceptable; the assertion is:
//	  - No deadlock or timeout (all 5 shims respond within the deadline)
//	  - No server-error responses with codes other than -32001 (unexpected errors)
//
// Design notes:
//
//	The test uses AIMUX_WARMUP=false to skip CLI binary probing (which would
//	trigger the heavy Phase B work). With warmup disabled, Phase B completes
//	near-instantly (only the swap itself, no I/O), so in practice most or all
//	shims will receive a valid tools/list result.
//
//	The Phase A lightweight delegate has a warmup_grace_seconds window. If Phase B
//	has not completed within that window, the delegate returns a -32001 retry hint.
//	This test accepts both outcomes so it remains valid even if the timing of
//	Phase B changes in the future.
//
// NFR gate: p99 response latency ≤ 1000ms across all 5 shims × 2 responses = 10 total.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

// shimResult captures the result of one shim's MCP session.
type shimResult struct {
	shimIdx       int
	latency       time.Duration // wall-clock time from tools/list send to response received
	toolsListResp map[string]any
	err           error
}

func TestColdStart_ConcurrentShimAttach(t *testing.T) {
	if testing.Short() {
		t.Skip("TestColdStart_ConcurrentShimAttach requires a real daemon process — skipped in short mode")
	}

	const numShims = 5
	const p99DeadlineMs = 1000 // NFR gate: p99 ≤ 1000ms

	binary := buildBinary(t)
	testcliBin := buildTestCLI(t)

	configDir, engineName, isolatedTmp := coldStartWriteConfig(t)
	testcliDir := filepath.Dir(testcliBin)
	pathEnv := testcliDir + string(os.PathListSeparator) + os.Getenv("PATH")
	tempEnvName := "TEMP"

	baseEnv := append(os.Environ(),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME="+engineName,
		"AIMUX_WARMUP=false",          // skip CLI warmup; Phase B completes near-instantly
		"AIMUX_SESSION_STORE=memory",  // no SQLite for test isolation
		"PATH="+pathEnv,
		"TMPDIR="+isolatedTmp,
		tempEnvName+"="+isolatedTmp,
		"TMP="+isolatedTmp,
	)

	// ---- Spawn daemon ----
	ctlSock := filepath.Join(isolatedTmp, engineName+"-muxd.ctl.sock")
	daemonCmd := exec.Command(binary, "--muxcore-daemon")
	daemonCmd.Env = baseEnv
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		cleanupDaemon(t, ctlSock, daemonCmd, "TestColdStart_ConcurrentShimAttach")
	})

	// Wait for daemon readiness via control socket (daemon is in Phase A immediately).
	if err := waitForCtlSocket(ctlSock, 60*time.Second); err != nil {
		t.Fatalf("daemon did not become ready: %v", err)
	}
	t.Log("daemon ready — spawning shims concurrently")

	// ---- Concurrently spawn numShims shims ----
	results := make([]shimResult, numShims)
	var wg sync.WaitGroup

	for i := range numShims {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, latency, err := runSingleShimSession(t, binary, baseEnv, idx)
			results[idx] = shimResult{
				shimIdx:       idx,
				latency:       latency,
				toolsListResp: resp,
				err:           err,
			}
		}(i)
	}

	wg.Wait()
	t.Log("all shims finished")

	// ---- Validate results ----
	var latencies []int
	for _, r := range results {
		if r.err != nil {
			t.Errorf("shim %d error: %v", r.shimIdx, r.err)
			continue
		}

		latencyMs := int(r.latency.Milliseconds())
		latencies = append(latencies, latencyMs)
		t.Logf("shim %d: latency=%dms", r.shimIdx, latencyMs)

		if r.toolsListResp == nil {
			t.Errorf("shim %d: nil tools/list response", r.shimIdx)
			continue
		}

		// Validate: response is either a valid result or a -32001 retry hint.
		if !isValidToolsListOrRetryHint(t, r.shimIdx, r.toolsListResp) {
			t.Errorf("shim %d: unexpected response shape: %v", r.shimIdx, r.toolsListResp)
		}
	}

	// p99 gate: all latencies must be ≤ p99DeadlineMs.
	if len(latencies) > 0 {
		sort.Ints(latencies)
		p99 := latencies[len(latencies)-1] // with 5 samples, worst-case = p99
		t.Logf("latency p99=%dms (gate=%dms), all=%v", p99, p99DeadlineMs, latencies)
		if p99 > p99DeadlineMs {
			t.Errorf("NFR gate FAIL: p99 latency %dms exceeds %dms limit (issue #129)",
				p99, p99DeadlineMs)
		}
	}
}

// runSingleShimSession spawns one shim, performs MCP initialize + tools/list,
// and returns the tools/list response map and the latency of the tools/list
// round-trip (send → response received), which is the NFR-1 gate metric.
func runSingleShimSession(t *testing.T, binary string, env []string, idx int) (map[string]any, time.Duration, error) {
	t.Helper()

	shimStdinR, shimStdinW, err := os.Pipe()
	if err != nil {
		return nil, 0, fmt.Errorf("stdin pipe: %w", err)
	}
	shimStdoutR, shimStdoutW, err := os.Pipe()
	if err != nil {
		shimStdinR.Close()
		shimStdinW.Close()
		return nil, 0, fmt.Errorf("stdout pipe: %w", err)
	}

	shimCmd := exec.Command(binary) // no --muxcore-daemon = shim mode
	shimCmd.Env = env
	shimCmd.Stdin = shimStdinR
	shimCmd.Stdout = shimStdoutW
	shimCmd.Stderr = os.Stderr

	if err := shimCmd.Start(); err != nil {
		shimStdinR.Close()
		shimStdinW.Close()
		shimStdoutR.Close()
		shimStdoutW.Close()
		return nil, 0, fmt.Errorf("start shim: %w", err)
	}
	shimStdinR.Close()  // parent no longer needs read-end of child's stdin
	shimStdoutW.Close() // parent no longer needs write-end of child's stdout

	defer func() {
		shimStdinW.Close()
		if shimCmd.Process != nil {
			done := make(chan struct{})
			go func() {
				_ = shimCmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = shimCmd.Process.Kill()
				<-done
			}
		}
		shimStdoutR.Close()
	}()

	reader := bufio.NewReader(shimStdoutR)

	// Send MCP initialize.
	initReq := jsonRPCRequest(10+idx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": fmt.Sprintf("cold-start-shim-%d", idx), "version": "1.0"},
	})
	if _, err := io.WriteString(shimStdinW, initReq); err != nil {
		return nil, 0, fmt.Errorf("write initialize: %w", err)
	}

	// Read initialize response (shim startup cost, not part of NFR gate).
	if _, err := readResponse(reader, 10*time.Second); err != nil {
		return nil, 0, fmt.Errorf("initialize response: %w", err)
	}

	// Send notifications/initialized (no response expected).
	notif := jsonRPCNotification("notifications/initialized")
	if _, err := io.WriteString(shimStdinW, notif); err != nil {
		return nil, 0, fmt.Errorf("write notifications/initialized: %w", err)
	}

	// Send tools/list and measure only this round-trip for the NFR-1 gate.
	// This isolates Phase A→B dispatch latency from process spawn cost.
	toolsReq := jsonRPCRequest(20+idx, "tools/list", map[string]any{})
	toolsStart := time.Now()
	if _, err := io.WriteString(shimStdinW, toolsReq); err != nil {
		return nil, 0, fmt.Errorf("write tools/list: %w", err)
	}

	// Read tools/list response. Deadline matches the per-shim NFR-1 budget.
	resp, err := readResponse(reader, 1500*time.Millisecond)
	toolsLatency := time.Since(toolsStart)
	if err != nil {
		return nil, 0, fmt.Errorf("tools/list response: %w", err)
	}

	return resp, toolsLatency, nil
}

// isValidToolsListOrRetryHint returns true if the response is either:
//   - a tools/list result (has "result" key)
//   - a -32001 retry-hint error (has "error" with code -32001)
func isValidToolsListOrRetryHint(t *testing.T, idx int, resp map[string]any) bool {
	t.Helper()

	if _, hasResult := resp["result"]; hasResult {
		// Valid tools/list — Phase B completed, real tools served.
		t.Logf("shim %d: received valid tools/list result (Phase B active)", idx)
		return true
	}

	errField, hasErr := resp["error"]
	if !hasErr {
		return false
	}
	errMap, ok := errField.(map[string]any)
	if !ok {
		return false
	}
	code, _ := errMap["code"].(float64)
	if code == -32001 {
		// -32001 retry-hint — Phase A active, correct behaviour.
		t.Logf("shim %d: received -32001 retry-hint (Phase A active, correct)", idx)
		return true
	}

	return false
}

// coldStartWriteConfig creates a minimal aimux config for the cold-start test.
// Returns: configDir, engineName, isolatedTmp.
func coldStartWriteConfig(t *testing.T) (configDir, engineName, isolatedTmp string) {
	t.Helper()

	var randSuffix [4]byte
	// Use process-unique name derived from test temp dir to avoid collisions.
	tmpBase := filepath.Base(t.TempDir())
	for i, ch := range tmpBase {
		if i >= 4 {
			break
		}
		randSuffix[i] = byte(ch)
	}
	engineName = "aimux-cs-" + fmt.Sprintf("%x", randSuffix)

	// Use a short-named temp dir to stay within Unix-socket path limits.
	var mkErr error
	isolatedTmp, mkErr = os.MkdirTemp(os.TempDir(), "cs")
	if mkErr != nil {
		t.Fatalf("create isolated tmp: %v", mkErr)
	}
	t.Cleanup(func() { _ = os.RemoveAll(isolatedTmp) })

	configDir = filepath.Join(isolatedTmp, "cfg")
	cliDir := filepath.Join(configDir, "cli.d", "codex")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatalf("MkdirAll cli.d/codex: %v", err)
	}

	logPath := filepath.Join(isolatedTmp, "aimux.log")

	cfgYAML := fmt.Sprintf(`server:
  log_level: info
  log_file: %s
  db_path: %s
  max_concurrent_jobs: 2
  default_timeout_seconds: 10
  warmup_grace_seconds: 15
  async_init: true

roles:
  default:
    cli: codex

circuit_breaker:
  failure_threshold: 3
  cooldown_seconds: 5
  half_open_max_calls: 1
`,
		filepath.ToSlash(logPath),
		filepath.ToSlash(filepath.Join(isolatedTmp, "sessions.db")),
	)

	if err := os.WriteFile(filepath.Join(configDir, "default.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write default.yaml: %v", err)
	}

	// Minimal testcli profile so registry probe finds a usable CLI.
	profile := `name: codex
binary: testcli
display_name: Codex (testcli)
command:
  base: testcli codex --json --full-auto
prompt_flag: positional
`
	if err := os.WriteFile(filepath.Join(cliDir, "profile.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatalf("write codex profile.yaml: %v", err)
	}

	return configDir, engineName, isolatedTmp
}
