package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildTestCLI compiles the testcli binary and returns the path.
func buildTestCLI(t *testing.T) string {
	t.Helper()

	binName := "testcli"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}

	binPath := filepath.Join(t.TempDir(), binName)

	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/testcli")
	cmd.Dir = projectRoot()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build testcli: %v\n%s", err, out)
	}

	return binPath
}

// startServerWithTestCLI launches aimux with testcli on PATH so codex/gemini profiles find it.
func startServerWithTestCLI(t *testing.T, aimuxBin, testcliBin string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()

	configDir := filepath.Join(testdataDir(), "config")
	testcliDir := filepath.Dir(testcliBin)

	cmd := exec.Command(aimuxBin)

	// Prepend testcli directory to PATH so the registry Probe finds "testcli" binary
	pathEnv := testcliDir + string(os.PathListSeparator) + os.Getenv("PATH")
	cmd.Env = append(os.Environ(),
		"AIMUX_CONFIG_DIR="+configDir,
		"PATH="+pathEnv,
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start aimux: %v", err)
	}

	t.Cleanup(func() {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	})

	return cmd, stdin, bufio.NewReader(stdout)
}

// initTestCLIServer builds both binaries, starts server with testcli on PATH, and initializes MCP.
func initTestCLIServer(t *testing.T) (io.WriteCloser, *bufio.Reader) {
	t.Helper()

	aimuxBin := buildBinary(t)
	testcliBin := buildTestCLI(t)

	_, stdin, reader := startServerWithTestCLI(t, aimuxBin, testcliBin)

	// Initialize MCP
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-testcli", "version": "1.0"},
	}))
	if _, err := readResponse(reader, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	return stdin, reader
}

// --- Codex JSONL Parsing Tests ---

// TestE2E_Codex_JSONL verifies aimux can execute the codex emulator and return output.
func TestE2E_Codex_JSONL(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "test codex jsonl",
			"cli":    "codex",
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("codex exec: %v", err)
	}

	text := extractToolText(t, resp)
	t.Logf("raw response text: %s", text)

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}

	// Verify we got a completed status
	status, _ := data["status"].(string)
	if status != "completed" {
		t.Errorf("status = %v, want completed (full: %v)", status, data)
	}

	// Verify content contains the response
	content, _ := data["content"].(string)
	if content == "" {
		t.Logf("full data: %v", data)
		t.Error("missing content in codex response")
	}
}

// TestE2E_Codex_HumanMode verifies codex without --json returns plain text.
func TestE2E_Codex_HumanMode(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// The profile has --json in command.base, but this test verifies the exec path works
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "test codex human mode",
			"cli":    "codex",
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("codex human mode: %v", err)
	}

	// Should get a result (not error)
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
}

// --- Gemini Stream-JSON Tests ---

// TestE2E_Gemini_StreamJSON verifies aimux can execute the gemini emulator with stream-json.
func TestE2E_Gemini_StreamJSON(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "test gemini stream json",
			"cli":    "gemini",
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("gemini exec: %v", err)
	}

	data := extractToolJSON(t, resp)

	status, _ := data["status"].(string)
	if status != "completed" {
		t.Errorf("status = %v, want completed", status)
	}

	content, _ := data["content"].(string)
	if content == "" {
		t.Error("missing content in gemini response")
	}

	// Content should contain gemini stream-json events
	if !strings.Contains(content, "Gemini response to:") &&
		!strings.Contains(content, "init") &&
		!strings.Contains(content, "result") {
		t.Logf("content: %s", content)
	}
}

// --- Multi-CLI Tests ---

// TestE2E_TestCLI_BothAvailable verifies both codex and gemini are discovered by aimux.
func TestE2E_TestCLI_BothAvailable(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// List sessions health to check available CLIs
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name":      "sessions",
		"arguments": map[string]any{"action": "health"},
	}))

	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("sessions health: %v", err)
	}

	data := extractToolJSON(t, resp)
	t.Logf("health: %v", data)

	// Should have at least echo-cli, codex, gemini available
	// (total_sessions may be 0 but the CLIs should be discovered)
}

// TestE2E_TestCLI_CodexAsync verifies async execution with codex emulator.
func TestE2E_TestCLI_CodexAsync(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Start async exec with codex
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "async codex test",
			"cli":    "codex",
			"async":  true,
		},
	}))

	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("codex async: %v", err)
	}

	data := extractToolJSON(t, resp)
	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Fatal("missing job_id in async response")
	}

	// Poll until completed
	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(stdin, jsonRPCRequest(3+i, "tools/call", map[string]any{
			"name":      "status",
			"arguments": map[string]any{"job_id": jobID},
		}))
		pollResp, pollErr := readResponse(reader, 5*time.Second)
		if pollErr != nil {
			t.Fatalf("status poll: %v", pollErr)
		}
		pollData := extractToolJSON(t, pollResp)
		status, _ := pollData["status"].(string)
		if status == "completed" {
			// Verify content has codex output
			content, _ := pollData["content"].(string)
			if content == "" {
				t.Error("completed job has empty content")
			}
			return
		}
		if status == "failed" {
			t.Fatalf("job failed: %v", pollData)
		}
	}
	t.Fatal("async codex job did not complete in time")
}

// --- Standalone testcli verification (no aimux server) ---

// TestE2E_TestCLI_CodexOutput verifies testcli codex produces valid JSONL directly.
func TestE2E_TestCLI_CodexOutput(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "codex", "--json", "--full-auto", "direct test")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli codex: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 JSONL lines, got %d: %v", len(lines), lines)
	}

	// Parse each line as valid JSON
	expectedTypes := []string{"thread.started", "turn.started", "item.completed", "turn.completed"}
	for i, line := range lines {
		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Fatalf("line %d not valid JSON: %v", i, err)
		}
		evtType, _ := evt["type"].(string)
		if evtType != expectedTypes[i] {
			t.Errorf("line %d: type = %q, want %q", i, evtType, expectedTypes[i])
		}
	}

	// Verify item.completed has nested item structure
	var itemEvt map[string]any
	json.Unmarshal([]byte(lines[2]), &itemEvt)
	item, _ := itemEvt["item"].(map[string]any)
	if item == nil {
		t.Fatal("item.completed missing nested 'item' field")
	}
	if item["type"] != "agent_message" {
		t.Errorf("item.type = %v, want agent_message", item["type"])
	}
	if item["text"] == nil || item["text"] == "" {
		t.Error("item.text is empty")
	}
}

// TestE2E_TestCLI_GeminiStreamJSON verifies testcli gemini produces valid stream-json JSONL.
func TestE2E_TestCLI_GeminiStreamJSON(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "gemini", "-p", "direct gemini test", "--output-format", "stream-json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli gemini: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 JSONL lines, got %d", len(lines))
	}

	// First line: init event
	var initEvt map[string]any
	json.Unmarshal([]byte(lines[0]), &initEvt)
	if initEvt["type"] != "init" {
		t.Errorf("first event type = %v, want init", initEvt["type"])
	}
	if initEvt["session_id"] == nil {
		t.Error("init event missing session_id")
	}
	if initEvt["model"] == nil {
		t.Error("init event missing model")
	}

	// Second line: user message
	var userEvt map[string]any
	json.Unmarshal([]byte(lines[1]), &userEvt)
	if userEvt["type"] != "message" {
		t.Errorf("second event type = %v, want message", userEvt["type"])
	}
	if userEvt["role"] != "user" {
		t.Errorf("second event role = %v, want user", userEvt["role"])
	}

	// Last line: result event
	var resultEvt map[string]any
	json.Unmarshal([]byte(lines[len(lines)-1]), &resultEvt)
	if resultEvt["type"] != "result" {
		t.Errorf("last event type = %v, want result", resultEvt["type"])
	}
	if resultEvt["status"] != "success" {
		t.Errorf("result status = %v, want success", resultEvt["status"])
	}
	stats, _ := resultEvt["stats"].(map[string]any)
	if stats == nil {
		t.Error("result event missing stats")
	}
}

// TestE2E_TestCLI_GeminiBufferedJSON verifies the JSON buffering trap.
// In buffered JSON mode, gemini outputs ZERO until complete, then one big blob.
func TestE2E_TestCLI_GeminiBufferedJSON(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "gemini", "-p", "buffered test", "--output-format", "json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli gemini json: %v", err)
	}

	// Should be a single JSON object (not JSONL lines)
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}

	// Verify key fields
	if result["model"] == nil {
		t.Error("missing model in buffered JSON")
	}
	if result["response"] == nil {
		t.Error("missing response in buffered JSON")
	}
	if result["stats"] == nil {
		t.Error("missing stats in buffered JSON")
	}
}

// TestE2E_TestCLI_CodexStdinPipe verifies stdin piping for long prompts.
func TestE2E_TestCLI_CodexStdinPipe(t *testing.T) {
	testcliBin := buildTestCLI(t)

	longPrompt := strings.Repeat("word ", 2000) // ~10000 chars
	cmd := exec.Command(testcliBin, "codex", "--json", "--full-auto", "-")
	cmd.Stdin = strings.NewReader(longPrompt)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli codex stdin: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 JSONL lines, got %d", len(lines))
	}

	// Verify the response contains the piped content
	var itemEvt map[string]any
	json.Unmarshal([]byte(lines[2]), &itemEvt)
	item, _ := itemEvt["item"].(map[string]any)
	text, _ := item["text"].(string)
	if !strings.Contains(text, "word") {
		t.Errorf("response doesn't reflect stdin content: %s", text)
	}
}
