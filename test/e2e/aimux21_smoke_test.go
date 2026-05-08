package e2e

import (
	"bufio"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	serverdebug "github.com/thebtf/aimux/pkg/server/debug"
	_ "modernc.org/sqlite"
)

const aimux21SmokeComment = "<!-- smoke test for AIMUX-21 -->"

// @critical - release blocker per Constitution rule #10.
func TestE2E_AIMUX21IndependentSmoke(t *testing.T) {
	if os.Getenv("AIMUX21_E2E") != "1" {
		t.Skip("AIMUX21_E2E=1 not set - skipping AIMUX-21 independent smoke")
	}

	aimuxBin := buildBinaryVersion(t, "v5.11.0")
	helperBin := buildAIMUX21PairHelper(t)
	projectDir := aimux21SmokeProject(t)
	configDir, dbPath := aimux21SmokeConfigDir(t, filepath.Base(helperBin))

	_, stdin, reader := startDaemonAndShimWithEnv(t, aimuxBin, filepath.Dir(helperBin), configDir, []string{
		"AIMUX_SESSION_STORE=sqlite",
		"AIMUX21_E2E=0",
	})
	initializeMCP(t, stdin, reader)

	assertAIMUX21RemovedToolsAbsent(t, stdin, reader, 2)

	result := callAIMUX21ToolJSON(t, stdin, reader, 3, "task", map[string]any{
		"prompt":       "add a one-line comment to README.md saying \"smoke test for AIMUX-21\"",
		"task_class":   "code",
		"project_id":   "aimux21-smoke",
		"request_id":   "aimux21-independent-smoke",
		"cwd":          projectDir,
		"max_attempts": 1,
	}, 5*time.Minute)
	assertAIMUX21TaskResult(t, result)
	assertAIMUX21ReadmeModified(t, filepath.Join(projectDir, "README.md"))
	assertAIMUX21Subtree(t, dbPath, result["task_id"].(string))

	assertAIMUX21RemovedToolsAbsent(t, stdin, reader, 4)
}

func callAIMUX21ToolJSON(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int, name string, args map[string]any, timeout time.Duration) map[string]any {
	t.Helper()
	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})); err != nil {
		t.Fatalf("%s request write: %v", name, err)
	}
	resp, err := readResponse(reader, timeout)
	if err != nil {
		t.Fatalf("%s response: %v", name, err)
	}
	return extractToolJSON(t, resp)
}

func assertAIMUX21TaskResult(t *testing.T, result map[string]any) {
	t.Helper()
	if result["task_class"] != "code" {
		t.Fatalf("task_class = %#v, want code; result=%v", result["task_class"], result)
	}
	if result["worker_type"] != "code" {
		t.Fatalf("worker_type = %#v, want code; result=%v", result["worker_type"], result)
	}
	if result["status"] != "completed" {
		t.Fatalf("status = %#v, want completed; result=%v", result["status"], result)
	}
	taskID, _ := result["task_id"].(string)
	if taskID == "" {
		t.Fatalf("task_id missing: %v", result)
	}
	if rounds, ok := metadataNumber(result["rounds"]); !ok || rounds < 1 {
		t.Fatalf("rounds = %#v, want number >= 1", result["rounds"])
	}
	if confidence, ok := metadataNumber(result["confidence_score"]); !ok || confidence < 0 || confidence > 1 {
		t.Fatalf("confidence_score = %#v, want number in [0,1]", result["confidence_score"])
	}

	metadata, ok := result["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing: %v", result)
	}
	driver, _ := metadata["driver_cli"].(string)
	navigator, _ := metadata["navigator_cli"].(string)
	if driver != "codex" {
		t.Fatalf("driver_cli = %#v, want codex; metadata=%v", metadata["driver_cli"], metadata)
	}
	if navigator != "claude" {
		t.Fatalf("navigator_cli = %#v, want claude; metadata=%v", metadata["navigator_cli"], metadata)
	}
	if sameCLIFamily(driver, navigator) {
		t.Fatalf("driver_cli and navigator_cli must be cross-family: %s vs %s", driver, navigator)
	}
	if metadata["gate_result"] != "passed" {
		t.Fatalf("gate_result = %#v, want passed; metadata=%v", metadata["gate_result"], metadata)
	}
}

func assertAIMUX21ReadmeModified(t *testing.T, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(content), aimux21SmokeComment) {
		t.Fatalf("README.md missing smoke comment:\n%s", content)
	}
}

func assertAIMUX21Subtree(t *testing.T, dbPath string, taskID string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open loom db: %v", err)
	}
	defer db.Close()

	store, err := loom.NewTaskStore(db, "aimux21-smoke-reader")
	if err != nil {
		t.Fatalf("loom.NewTaskStore: %v", err)
	}
	engine := loom.New(store)

	text, err := serverdebug.FormatSubtree(engine, taskID, 4)
	if err != nil {
		t.Fatalf("FormatSubtree: %v", err)
	}
	for _, want := range []string{"worker=code", "worker=code_driver", "worker=code_navigator"} {
		if !strings.Contains(text, want) {
			t.Fatalf("subtree missing %q:\n%s", want, text)
		}
	}
}

func assertAIMUX21RemovedToolsAbsent(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int) {
	t.Helper()
	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "tools/list", nil)); err != nil {
		t.Fatalf("tools/list request write: %v", err)
	}
	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %v", result["tools"])
	}
	toolNames := make(map[string]bool, len(tools))
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		if name != "" {
			toolNames[name] = true
		}
	}
	for _, removed := range []string{"codex_task", "codex_review", "codex_status", "codex_cancel", "codex_review_gate"} {
		if toolNames[removed] {
			t.Fatalf("removed tool %q is still present in tools/list: %v", removed, sortedAIMUX21ToolNames(toolNames))
		}
	}
	if !toolNames["task"] {
		t.Fatalf("replacement task tool missing from tools/list: %v", sortedAIMUX21ToolNames(toolNames))
	}
}

func sameCLIFamily(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func aimux21SmokeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFileForTest(t, filepath.Join(dir, "README.md"), "# AIMUX-21 smoke\n")
	writeFileForTest(t, filepath.Join(dir, "go.mod"), "module aimux21smoke\n\ngo 1.25\n")
	writeFileForTest(t, filepath.Join(dir, "smoke.go"), "package aimux21smoke\n")
	writeFileForTest(t, filepath.Join(dir, "smoke_test.go"), "package aimux21smoke\n\nimport \"testing\"\n\nfunc TestSmoke(t *testing.T) {}\n")
	return dir
}

func aimux21SmokeConfigDir(t *testing.T, helperName string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.ToSlash(filepath.Join(dir, "sessions.db"))
	logPath := filepath.ToSlash(filepath.Join(dir, "aimux.log"))
	writeFileForTest(t, filepath.Join(dir, "default.yaml"), fmt.Sprintf(`server:
  log_level: error
  log_file: %q
  db_path: %q
  max_concurrent_jobs: 5
  default_timeout_seconds: 20
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
    cli: codex
  coding:
    cli: codex
  codereview:
    cli: claude
  thinkdeep:
    cli: codex

circuit_breaker:
  failure_threshold: 3
  cooldown_seconds: 5
  half_open_max_calls: 1
`, logPath, dbPath))
	writeAIMUX21Profile(t, dir, "codex", helperName, "driver")
	writeAIMUX21Profile(t, dir, "claude", helperName, "navigator")
	return dir, dbPath
}

func writeAIMUX21Profile(t *testing.T, configDir string, name string, helperName string, mode string) {
	t.Helper()
	profileDir := filepath.Join(configDir, "cli.d", name)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}
	writeFileForTest(t, filepath.Join(profileDir, "profile.yaml"), fmt.Sprintf(`name: %s
binary: %s
display_name: "%s AIMUX-21 smoke helper"
capabilities: [coding, review]

features:
  streaming: false
  headless: true
  read_only: false
  session_resume: false
  json: false
  jsonl: false
  stdin_pipe: false

output_format: text

command:
  base: "%s %s"

prompt_flag: ""
prompt_flag_type: positional
timeout_seconds: 30
`, name, helperName, aimux21DisplayName(name), helperName, mode))
}

func aimux21DisplayName(name string) string {
	if name == "" {
		return "CLI"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func buildAIMUX21PairHelper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binName := "aimux21paircli"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	sourcePath := filepath.Join(dir, "main.go")
	binPath := filepath.Join(dir, binName)
	writeFileForTest(t, sourcePath, aimux21PairHelperSource)
	cmd := exec.Command("go", "build", "-o", binPath, sourcePath)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build AIMUX-21 helper: %v\n%s", err, out)
	}
	return binPath
}

func writeFileForTest(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sortedAIMUX21ToolNames(toolNames map[string]bool) []string {
	names := make([]string, 0, len(toolNames))
	for name := range toolNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

const aimux21PairHelperSource = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const readmeDiff = "--- a/README.md\n+++ b/README.md\n@@ -1 +1,2 @@\n # AIMUX-21 smoke\n+<!-- smoke test for AIMUX-21 -->\n"

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: aimux21paircli <driver|navigator> <prompt>")
		os.Exit(2)
	}
	mode := os.Args[1]
	prompt := strings.Join(os.Args[2:], " ")
	switch mode {
	case "driver":
		if !strings.Contains(prompt, "README.md") || !strings.Contains(prompt, "smoke test for AIMUX-21") {
			fmt.Fprintf(os.Stderr, "driver prompt missing AIMUX-21 README task: %s\n", prompt)
			os.Exit(1)
		}
		fmt.Print(readmeDiff)
	case "navigator":
		if !strings.Contains(prompt, "Driver diff:") || !strings.Contains(prompt, "<!-- smoke test for AIMUX-21 -->") {
			fmt.Fprintf(os.Stderr, "navigator prompt missing AIMUX-21 driver diff: %s\n", prompt)
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"verdict":    "APPLY",
			"confidence": 0.95,
			"diff":       "",
			"feedback":   "",
			"evidence":   "README.md receives the requested AIMUX-21 smoke comment.",
		})
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", mode)
		os.Exit(2)
	}
}
`
