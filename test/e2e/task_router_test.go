package e2e

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestE2E_TaskRouterMCPRoundTrip(t *testing.T) {
	if os.Getenv("AIMUX21_E2E") != "1" {
		t.Skip("AIMUX21_E2E=1 not set - skipping task router e2e")
	}

	stdin, reader := initTaskRouterServer(t)

	explicit := callTaskRouterToolJSON(t, stdin, reader, 2, map[string]any{
		"prompt":          "Run an explicit router wiring review.",
		"task_class":      "review",
		"target":          "HEAD",
		"timeout_seconds": 300,
	})
	assertTaskRouterResult(t, explicit, "review")

	classified := callTaskRouterToolJSON(t, stdin, reader, 3, map[string]any{
		"prompt":          "Review PR #152 diff against HEAD and block on security regressions.",
		"timeout_seconds": 300,
	})
	assertTaskRouterResult(t, classified, "review")

	ambiguous := callTaskRouterToolRaw(t, stdin, reader, 4, map[string]any{
		"prompt": "Help me make this better.",
	})
	expectError(t, ambiguous)
	errPayload := extractToolJSON(t, ambiguous)
	if errPayload["code"] != "ClassificationAmbiguous" {
		t.Fatalf("ambiguous code = %#v, want ClassificationAmbiguous; payload=%v", errPayload["code"], errPayload)
	}
	if candidates, ok := errPayload["candidates"].([]any); !ok || len(candidates) != 3 {
		t.Fatalf("ambiguous candidates = %#v, want 3 candidates", errPayload["candidates"])
	}
}

func initTaskRouterServer(t *testing.T) (io.WriteCloser, *bufio.Reader) {
	t.Helper()

	aimuxBin := buildBinary(t)
	testcliBin := buildTestCLI(t)
	configDir := taskRouterConfigDir(t)

	_, stdin, reader := startDaemonAndShimWithEnv(t, aimuxBin, filepath.Dir(testcliBin), configDir, []string{
		"AIMUX_SESSION_STORE=sqlite",
	})
	initializeMCP(t, stdin, reader)
	return stdin, reader
}

func taskRouterConfigDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	copyTaskRouterDir(t, filepath.Join(testdataDir(), "config", "cli.d"), filepath.Join(dir, "cli.d"))

	dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "sessions.db"))
	logPath := filepath.ToSlash(filepath.Join(t.TempDir(), "aimux.log"))
	config := fmt.Sprintf(`# Task router E2E config.
server:
  log_level: error
  log_file: %q
  db_path: %q
  max_concurrent_jobs: 5
  default_timeout_seconds: 10
  max_prompt_bytes: 1048576
  audit:
    scanner_role: default
    validator_role: default
    parallel_scanners: 1
  pair:
    max_rounds: 1
  consensus:
    max_turns: 2
  debate:
    max_turns: 2

roles:
  default:
    cli: echo-cli
  coding:
    cli: codex
  codereview:
    cli: codex
  thinkdeep:
    cli: echo-cli

circuit_breaker:
  failure_threshold: 3
  cooldown_seconds: 5
  half_open_max_calls: 1
`, logPath, dbPath)
	if err := os.WriteFile(filepath.Join(dir, "default.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write task router config: %v", err)
	}
	return dir
}

func copyTaskRouterDir(t *testing.T, src string, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read dir %s: %v", src, err)
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			copyTaskRouterDir(t, srcPath, dstPath)
			continue
		}
		copyFileForTest(t, srcPath, dstPath)
	}
}

func callTaskRouterToolJSON(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int, args map[string]any) map[string]any {
	t.Helper()
	return extractToolJSON(t, callTaskRouterToolRaw(t, stdin, reader, id, args))
}

func callTaskRouterToolRaw(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int, args map[string]any) map[string]any {
	t.Helper()
	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "tools/call", map[string]any{
		"name":      "task",
		"arguments": args,
	})); err != nil {
		t.Fatalf("task request write: %v", err)
	}
	resp, err := readResponse(reader, 30*time.Second)
	if err != nil {
		t.Fatalf("task response: %v", err)
	}
	return resp
}

func assertTaskRouterResult(t *testing.T, payload map[string]any, wantClass string) {
	t.Helper()
	if payload["task_class"] != wantClass {
		t.Fatalf("task_class = %#v, want %q; payload=%v", payload["task_class"], wantClass, payload)
	}
	if payload["worker_type"] != wantClass {
		t.Fatalf("worker_type = %#v, want %q; payload=%v", payload["worker_type"], wantClass, payload)
	}
	if payload["status"] != "completed" {
		t.Fatalf("status = %#v, want completed; payload=%v", payload["status"], payload)
	}
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("task_id missing: %v", payload)
	}
	metadata, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing: %v", payload)
	}
	if metadata["review_sub_mode"] != "aggregate" {
		t.Fatalf("review_sub_mode = %#v, want aggregate; metadata=%v", metadata["review_sub_mode"], metadata)
	}
	passes, ok := metadata["passes_completed"].([]any)
	if !ok || len(passes) != 3 {
		t.Fatalf("passes_completed = %#v, want 3 passes", metadata["passes_completed"])
	}
}
