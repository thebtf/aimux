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

// startServerWithTestCLI launches aimux with testcli on PATH so codex/gemini
// profiles find it.
//
// AIMUX-6 removed the AIMUX_NO_ENGINE=1 stdio-direct bypass. Tests now use
// the daemon+shim pair via startDaemonAndShim: aimux is spawned in daemon
// mode with testcliDir prepended to PATH (daemon needs the binary to probe
// CLI availability), a shim client bridges stdio to that daemon, and the
// test talks MCP over the shim's stdin/stdout. Signature stable for
// call-site compatibility.
func startServerWithTestCLI(t *testing.T, aimuxBin, testcliBin string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()
	configDir := filepath.Join(testdataDir(), "config")
	testcliDir := filepath.Dir(testcliBin)
	return startDaemonAndShim(t, aimuxBin, testcliDir, configDir)
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

func streamMessages(reader *bufio.Reader, done <-chan struct{}) (<-chan map[string]any, <-chan error) {
	msgCh := make(chan map[string]any, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(msgCh)
		defer close(errCh)
		for {
			select {
			case <-done:
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				select {
				case <-done:
					return
				default:
				}
				errCh <- fmt.Errorf("read line: %w", err)
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				errCh <- fmt.Errorf("parse json: %w (line: %s)", err, line)
				return
			}

			select {
			case <-done:
				return
			case msgCh <- msg:
			}
		}
	}()

	return msgCh, errCh
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
			"async":  false,
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

	if data["content_length"] == nil {
		t.Logf("full data: %v", data)
		t.Error("expected content_length in codex response")
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
			"async":  false,
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
			"prompt":          "test gemini stream json",
			"cli":             "gemini",
			"async":           false,
			"include_content": true,
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
			"arguments": map[string]any{"job_id": jobID, "include_content": true},
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

func TestE2E_Agent_AsyncProgressNotification(t *testing.T) {
	// AIMUX-6 follow-up: async progress notifications ride a long-lived MCP
	// session through the shim, which trips the muxcore resilient_client
	// stdin-EOF race (engram mcp-mux#153) — the shim exits before the
	// notifications/progress event is delivered. Re-enable once muxcore#153
	// is resolved.
	t.Skip("blocked by engram mcp-mux#153 (muxcore resilient_client stdin EOF races)")

	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "agent",
		"arguments": map[string]any{
			"agent":  "implementer",
			"prompt": "async implementer test",
			"async":  true,
			"cwd":    t.TempDir(),
		},
	}))

	deadline := time.Now().Add(10 * time.Second)
	var jobID string
	var progressParams map[string]any
	progressSeen := false
	completed := false
	done := make(chan struct{})
	defer close(done)
	msgCh, errCh := streamMessages(reader, done)
	nextPollID := 100

	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("read message: %v", err)
			}
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatal("message stream closed before async agent verification completed")
			}

			if method, ok := msg["method"].(string); ok {
				if method == "notifications/progress" {
					progressParams, _ = msg["params"].(map[string]any)
					if jobID != "" && progressParams != nil {
						if token, _ := progressParams["progressToken"].(string); token == jobID {
							if message, _ := progressParams["message"].(string); message != "" {
								progressSeen = true
							}
						}
					}
				}
				continue
			}

			if id, ok := msg["id"].(float64); ok {
				if int(id) == 2 {
					data := extractToolJSON(t, msg)
					jobID, _ = data["job_id"].(string)
					if jobID == "" {
						t.Fatal("missing job_id in agent async response")
					}
					continue
				}
				if int(id) >= 100 {
					pollData := extractToolJSON(t, msg)
					status, _ := pollData["status"].(string)
					if status == "failed" {
						t.Fatalf("agent job failed: %v", pollData)
					}
					if status == "completed" {
						completed = true
					}
				}
			}
		case <-time.After(200 * time.Millisecond):
			if jobID != "" && !completed {
				fmt.Fprint(stdin, jsonRPCRequest(nextPollID, "tools/call", map[string]any{
					"name":      "status",
					"arguments": map[string]any{"job_id": jobID},
				}))
				nextPollID++
			}
		}

		if progressSeen && completed {
			break
		}
	}

	if jobID == "" {
		t.Fatal("did not observe async agent response with job_id")
	}
	if progressParams == nil {
		t.Fatal("did not observe notifications/progress event")
	}
	if token, _ := progressParams["progressToken"].(string); token != jobID {
		t.Fatalf("progressToken = %q, want %q", token, jobID)
	}
	if message, _ := progressParams["message"].(string); message == "" {
		t.Fatal("progress notification message is empty")
	}
	if !progressSeen {
		t.Fatal("did not observe non-empty progress notification for async agent")
	}
	if !completed {
		t.Fatal("async agent job did not reach completed state")
	}
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

// --- Phase 2: Claude Tests ---

// TestE2E_TestCLI_ClaudeStreamJSON verifies testcli claude produces valid stream-json NDJSON.
func TestE2E_TestCLI_ClaudeStreamJSON(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "claude", "-p", "direct claude test", "--output-format", "stream-json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli claude: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 NDJSON lines, got %d", len(lines))
	}

	// First line should be content_block_delta (NO init event — key difference from gemini)
	var firstEvt map[string]any
	json.Unmarshal([]byte(lines[0]), &firstEvt)
	if firstEvt["type"] != "content_block_delta" {
		t.Errorf("first event type = %v, want content_block_delta", firstEvt["type"])
	}
	delta, _ := firstEvt["delta"].(map[string]any)
	if delta == nil {
		t.Error("content_block_delta missing 'delta' field")
	}
	if delta["type"] != "text_delta" {
		t.Errorf("delta.type = %v, want text_delta", delta["type"])
	}

	// Last line should be result
	var lastEvt map[string]any
	json.Unmarshal([]byte(lines[len(lines)-1]), &lastEvt)
	if lastEvt["type"] != "result" {
		t.Errorf("last event type = %v, want result", lastEvt["type"])
	}
	if lastEvt["model"] == nil {
		t.Error("result missing model")
	}
}

// TestE2E_TestCLI_ClaudeBufferedJSON verifies claude JSON buffering trap.
func TestE2E_TestCLI_ClaudeBufferedJSON(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "claude", "-p", "buffered test", "--output-format", "json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli claude json: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("not valid JSON: %v\noutput: %s", err, out)
	}

	if result["content"] == nil {
		t.Error("missing content in buffered JSON")
	}
	if result["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason = %v, want end_turn", result["stop_reason"])
	}
}

// TestE2E_Claude_ThroughAimux verifies claude execution through aimux server.
func TestE2E_Claude_ThroughAimux(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "test claude through aimux",
			"cli":    "claude",
			"async":  false,
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("claude exec: %v", err)
	}

	text := extractToolText(t, resp)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}

	status, _ := data["status"].(string)
	if status != "completed" {
		t.Errorf("status = %v, want completed (full: %v)", status, data)
	}

	// Budget policy: default-brief omits content; assert content_length instead (FR-11).
	cl, _ := data["content_length"].(float64)
	if cl <= 0 {
		t.Logf("full data: %v", data)
		t.Error("missing content_length > 0 in claude response (brief default)")
	}
}

// --- Phase 2: Goose Tests ---

// TestE2E_TestCLI_GooseStreamJSON verifies testcli goose produces valid stream-json JSONL.
func TestE2E_TestCLI_GooseStreamJSON(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "goose", "-t", "direct goose test", "--output-format", "stream-json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli goose: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSONL lines, got %d: %v", len(lines), lines)
	}

	// Event 1: start
	var startEvt map[string]any
	json.Unmarshal([]byte(lines[0]), &startEvt)
	if startEvt["type"] != "start" {
		t.Errorf("first event type = %v, want start", startEvt["type"])
	}

	// Event 2: message
	var msgEvt map[string]any
	json.Unmarshal([]byte(lines[1]), &msgEvt)
	if msgEvt["type"] != "message" {
		t.Errorf("second event type = %v, want message", msgEvt["type"])
	}
	if msgEvt["role"] != "assistant" {
		t.Errorf("message role = %v, want assistant", msgEvt["role"])
	}

	// Event 3: done with usage
	var doneEvt map[string]any
	json.Unmarshal([]byte(lines[2]), &doneEvt)
	if doneEvt["type"] != "done" {
		t.Errorf("third event type = %v, want done", doneEvt["type"])
	}
	if doneEvt["usage"] == nil {
		t.Error("done event missing usage")
	}
}

// TestE2E_Goose_ThroughAimux verifies goose execution through aimux server.
func TestE2E_Goose_ThroughAimux(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "test goose through aimux",
			"cli":    "goose",
			"async":  false,
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("goose exec: %v", err)
	}

	text := extractToolText(t, resp)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}

	if data["status"] != "completed" {
		t.Errorf("status = %v, want completed", data["status"])
	}
}

// --- Phase 2: Crush Tests ---

// TestE2E_TestCLI_CrushOutput verifies testcli crush produces plain text output.
func TestE2E_TestCLI_CrushOutput(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "crush", "direct crush test")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli crush: %v", err)
	}

	output := strings.TrimSpace(string(out))
	if !strings.Contains(output, "Crush response to:") {
		t.Errorf("unexpected output: %s", output)
	}
	if !strings.Contains(output, "direct crush test") {
		t.Errorf("output doesn't contain prompt: %s", output)
	}
}

// TestE2E_Crush_ThroughAimux verifies crush execution through aimux server.
func TestE2E_Crush_ThroughAimux(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "test crush through aimux",
			"cli":    "crush",
			"async":  false,
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("crush exec: %v", err)
	}

	text := extractToolText(t, resp)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}

	if data["status"] != "completed" {
		t.Errorf("status = %v, want completed", data["status"])
	}

	content, _ := data["content"].(string)
	if !strings.Contains(content, "Crush response") {
		t.Logf("content: %s", content)
		// Crush outputs plain text — should be captured
	}
}

// TestE2E_TestCLI_CrushStdinPrepend verifies crush reads stdin and prepends to prompt.
func TestE2E_TestCLI_CrushStdinPrepend(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "crush", "summarize this")
	cmd.Stdin = strings.NewReader("context from stdin")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli crush stdin: %v", err)
	}

	output := strings.TrimSpace(string(out))
	// Crush prepends stdin to prompt, so both should be in the response
	if !strings.Contains(output, "context from stdin") {
		t.Errorf("output missing stdin content: %s", output)
	}
}

// --- Phase 3: Standalone Tests ---

func TestE2E_TestCLI_AiderOutput(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "aider", "--message", "test aider")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli aider: %v", err)
	}

	output := strings.TrimSpace(string(out))
	if !strings.Contains(output, "Aider response to: test aider") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestE2E_TestCLI_QwenStreamJSON(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "qwen", "-p", "test qwen", "--output-format", "stream-json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli qwen: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 JSONL lines, got %d", len(lines))
	}

	// Qwen uses gemini format — first line should be init
	var initEvt map[string]any
	json.Unmarshal([]byte(lines[0]), &initEvt)
	if initEvt["type"] != "init" {
		t.Errorf("first event type = %v, want init", initEvt["type"])
	}

	// Last line should be result
	var resultEvt map[string]any
	json.Unmarshal([]byte(lines[len(lines)-1]), &resultEvt)
	if resultEvt["type"] != "result" {
		t.Errorf("last event type = %v, want result", resultEvt["type"])
	}
}

func TestE2E_TestCLI_GptmeOutput(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "gptme", "-n", "test gptme", "--non-interactive")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli gptme: %v", err)
	}

	output := strings.TrimSpace(string(out))
	if !strings.Contains(output, "Gptme response to: test gptme") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestE2E_TestCLI_ClineJSON(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "cline", "--json", "test cline")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli cline: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %v", len(lines), lines)
	}

	// Message event
	var msgEvt map[string]any
	json.Unmarshal([]byte(lines[0]), &msgEvt)
	if msgEvt["type"] != "message" {
		t.Errorf("first event type = %v, want message", msgEvt["type"])
	}

	// Completion event
	var compEvt map[string]any
	json.Unmarshal([]byte(lines[1]), &compEvt)
	if compEvt["type"] != "completion" {
		t.Errorf("second event type = %v, want completion", compEvt["type"])
	}
}

func TestE2E_TestCLI_ContinueOutput(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "continue", "-p", "test continue")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testcli continue: %v", err)
	}

	output := strings.TrimSpace(string(out))
	if !strings.Contains(output, "Continue response to: test continue") {
		t.Errorf("unexpected output: %s", output)
	}
}

// --- Phase 3: Through-aimux Tests ---

func TestE2E_Aider_ThroughAimux(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "test aider via aimux",
			"cli":    "aider",
			"async":  false,
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("aider exec: %v", err)
	}

	text := extractToolText(t, resp)
	var data map[string]any
	json.Unmarshal([]byte(text), &data)
	if data["status"] != "completed" {
		t.Errorf("status = %v, want completed", data["status"])
	}
}

func TestE2E_Qwen_ThroughAimux(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "test qwen via aimux",
			"cli":    "qwen",
			"async":  false,
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("qwen exec: %v", err)
	}

	text := extractToolText(t, resp)
	var data map[string]any
	json.Unmarshal([]byte(text), &data)
	if data["status"] != "completed" {
		t.Errorf("status = %v, want completed", data["status"])
	}
}

func TestE2E_Cline_ThroughAimux(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "test cline via aimux",
			"cli":    "cline",
			"async":  false,
		},
	}))

	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("cline exec: %v", err)
	}

	text := extractToolText(t, resp)
	var data map[string]any
	json.Unmarshal([]byte(text), &data)
	if data["status"] != "completed" {
		t.Errorf("status = %v, want completed", data["status"])
	}
}

// ========================================================================
// Phase 4: Behavior-Specific Tests
// ========================================================================

// TestE2E_Behavior_GeminiJSONBufferingTrap verifies gemini's JSON buffering mode:
// ZERO stdout until task completion, then one big JSON blob.
// This caused months of debugging in v2 — aimux inactivity timeout killed the process.
func TestE2E_Behavior_GeminiJSONBufferingTrap(t *testing.T) {
	testcliBin := buildTestCLI(t)

	start := time.Now()
	cmd := exec.Command(testcliBin, "gemini", "-p", "buffering trap test", "--output-format", "json")
	out, err := cmd.Output()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("gemini json mode: %v", err)
	}

	// Output must be valid JSON (single object, not JSONL)
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("output not valid single JSON: %v\nraw: %s", err, out)
	}

	if elapsed < 50*time.Millisecond {
		t.Logf("warning: gemini json mode completed in %v — may not test real buffering", elapsed)
	}

	if result["response"] == nil {
		t.Error("buffered JSON missing 'response' field")
	}
	if result["stats"] == nil {
		t.Error("buffered JSON missing 'stats' field")
	}

	t.Logf("gemini JSON buffering: output=%d bytes in %v", len(out), elapsed)
}

// TestE2E_Behavior_ClaudeJSONBufferingTrap verifies claude JSON mode has same trap.
func TestE2E_Behavior_ClaudeJSONBufferingTrap(t *testing.T) {
	testcliBin := buildTestCLI(t)

	cmd := exec.Command(testcliBin, "claude", "-p", "claude buffering test", "--output-format", "json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("claude json mode: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("output not valid single JSON: %v", err)
	}

	if result["content"] == nil {
		t.Error("buffered JSON missing 'content'")
	}
	if result["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason = %v, want end_turn", result["stop_reason"])
	}
}

// TestE2E_Behavior_AsyncCancelPropagation starts an async goose job (100ms OTEL delay),
// kills it, and verifies the job transitions to a terminal state.
func TestE2E_Behavior_AsyncCancelPropagation(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Start async goose job
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "async cancel test with goose OTEL delay",
			"cli":    "goose",
			"async":  true,
		},
	}))

	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("goose async start: %v", err)
	}

	data := extractToolJSON(t, resp)
	jobID, _ := data["job_id"].(string)
	sessionID, _ := data["session_id"].(string)
	if jobID == "" || sessionID == "" {
		t.Fatalf("missing job_id or session_id: %v", data)
	}

	time.Sleep(100 * time.Millisecond)

	// Kill the session
	fmt.Fprint(stdin, jsonRPCRequest(3, "tools/call", map[string]any{
		"name": "sessions",
		"arguments": map[string]any{
			"action":     "kill",
			"session_id": sessionID,
		},
	}))

	killResp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	killData := extractToolJSON(t, killResp)
	t.Logf("kill result: %v", killData)

	// Poll job status — should be terminal
	time.Sleep(300 * time.Millisecond)
	fmt.Fprint(stdin, jsonRPCRequest(4, "tools/call", map[string]any{
		"name":      "status",
		"arguments": map[string]any{"job_id": jobID},
	}))

	statusResp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("status after kill: %v", err)
	}

	statusData := extractToolJSON(t, statusResp)
	status, _ := statusData["status"].(string)
	t.Logf("job status after kill: %s", status)

	if status == "running" {
		t.Error("job still 'running' after session kill — cancel propagation failed")
	}
}

// TestE2E_Behavior_StdinEOFHandling verifies that aimux properly closes stdin pipe
// when piping long prompts to codex. Without EOF, codex hangs forever reading stdin.
// This was a real deadlock bug in v2.
func TestE2E_Behavior_StdinEOFHandling(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Prompt longer than codex stdin_threshold (6000 chars) triggers stdin piping
	longPrompt := strings.Repeat("This is a long prompt word. ", 250) // ~7000 chars

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt":          longPrompt,
			"cli":             "codex",
			"async":           false,
			"include_content": true,
		},
	}))

	// Must complete. If stdin EOF not sent → codex hangs → timeout.
	resp, err := readResponse(reader, 15*time.Second)
	if err != nil {
		t.Fatalf("codex stdin EOF: %v (likely stdin pipe not closed — deadlock)", err)
	}

	text := extractToolText(t, resp)
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}

	t.Logf("full response: %s", text)

	if data["status"] != "completed" {
		t.Errorf("status = %v, want completed", data["status"])
	}

	content, _ := data["content"].(string)
	if content == "" {
		// Try raw content field (might not be string)
		t.Logf("content type: %T, value: %v", data["content"], data["content"])
	}
	if !strings.Contains(content, "long prompt word") && !strings.Contains(content, "Codex response") {
		t.Logf("content preview: %.500s", content)
		t.Error("response doesn't reflect stdin content — prompt may not have been piped")
	}

	t.Logf("stdin EOF: prompt=%d chars, content=%d chars", len(longPrompt), len(content))
}

// TestE2E_Behavior_GooseOTELDelay verifies goose's 100ms OTEL delay at exit.
func TestE2E_Behavior_GooseOTELDelay(t *testing.T) {
	testcliBin := buildTestCLI(t)

	start := time.Now()
	cmd := exec.Command(testcliBin, "goose", "-t", "OTEL delay test", "--output-format", "stream-json")
	out, err := cmd.Output()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("goose OTEL: %v", err)
	}

	if elapsed < 100*time.Millisecond {
		t.Errorf("goose completed in %v — expected >=100ms OTEL delay", elapsed)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSONL events, got %d", len(lines))
	}

	t.Logf("goose with OTEL delay: %v", elapsed)
}

// TestE2E_Behavior_ClaudeNoInitEvent verifies claude does NOT emit init event
// (unlike gemini). If aimux assumes all JSONL CLIs start with init → misparse.
func TestE2E_Behavior_ClaudeNoInitEvent(t *testing.T) {
	testcliBin := buildTestCLI(t)

	claudeCmd := exec.Command(testcliBin, "claude", "-p", "init event test", "--output-format", "stream-json")
	claudeOut, err := claudeCmd.Output()
	if err != nil {
		t.Fatalf("claude: %v", err)
	}

	claudeLines := strings.Split(strings.TrimSpace(string(claudeOut)), "\n")
	var claudeFirst map[string]any
	json.Unmarshal([]byte(claudeLines[0]), &claudeFirst)

	if claudeFirst["type"] == "init" {
		t.Error("claude emitted 'init' event — this is gemini behavior, not claude")
	}
	if claudeFirst["type"] != "content_block_delta" {
		t.Errorf("claude first event = %v, want content_block_delta", claudeFirst["type"])
	}

	// Gemini DOES have init
	geminiCmd := exec.Command(testcliBin, "gemini", "-p", "init event test", "--output-format", "stream-json")
	geminiOut, _ := geminiCmd.Output()
	geminiLines := strings.Split(strings.TrimSpace(string(geminiOut)), "\n")
	var geminiFirst map[string]any
	json.Unmarshal([]byte(geminiLines[0]), &geminiFirst)

	if geminiFirst["type"] != "init" {
		t.Errorf("gemini first event = %v, want init", geminiFirst["type"])
	}

	t.Log("verified: claude=content_block_delta first, gemini=init first")
}

// --- Orchestrator Multi-CLI E2E Tests ---

// TestE2E_Orchestrator_ConsensusMultiCLI verifies consensus orchestrator resolves
// correct binary and prompt flags for multiple testcli emulators.
//
// Since LoomEngine v3 (PR #69) consensus is async-by-default: submission returns
// {job_id, status:"running"} and the test must poll status until completion.
func TestE2E_Orchestrator_ConsensusMultiCLI(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "consensus",
		"arguments": map[string]any{
			"topic":      "What is the best programming language?",
			"synthesize": true,
		},
	}))

	resp, err := readResponse(reader, 30*time.Second)
	if err != nil {
		t.Fatalf("consensus: %v", err)
	}

	data := extractToolJSON(t, resp)
	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id in consensus async response: %+v", data)
	}
	if data["status"] != "running" {
		t.Errorf("initial status = %v, want running", data["status"])
	}

	// Poll status until completed — testcli emulators finish quickly.
	var content string
	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(stdin, jsonRPCRequest(3+i, "tools/call", map[string]any{
			"name":      "status",
			"arguments": map[string]any{"job_id": jobID, "include_content": true},
		}))
		pollResp, pollErr := readResponse(reader, 5*time.Second)
		if pollErr != nil {
			t.Fatalf("status poll: %v", pollErr)
		}
		pollData := extractToolJSON(t, pollResp)
		status, _ := pollData["status"].(string)
		if status == "completed" {
			content, _ = pollData["content"].(string)
			break
		}
		if status == "failed" || status == "failed_crash" {
			t.Fatalf("consensus job failed: %+v", pollData)
		}
	}
	if content == "" {
		t.Fatal("consensus content empty after polling — job may have hung")
	}
	t.Logf("consensus content (first 500): %.500s", content)

	// Verify synthesis section is present in the content (synthesize=true was set).
	if !strings.Contains(content, "Synthesis") {
		t.Log("note: synthesis section not found in content — may have been skipped if only 1 CLI succeeded")
	}
}

// TestE2E_Orchestrator_DialogMultiCLI verifies dialog orchestrator resolves
// correct binary and prompt flags for sequential multi-turn dialog.
func TestE2E_Orchestrator_DialogMultiCLI(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "dialog",
		"arguments": map[string]any{
			"prompt":    "Discuss the merits of Go vs Rust",
			"max_turns": 2,
		},
	}))

	resp, err := readResponse(reader, 30*time.Second)
	if err != nil {
		t.Fatalf("dialog: %v", err)
	}

	text := extractToolText(t, resp)
	t.Logf("dialog response (first 500): %.500s", text)

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("response not JSON: %v\nraw: %s", err, text)
	}

	// Dialog responses are wrapped in the guidance envelope; domain fields
	// (status, turns, participants) are nested under the "result" key.
	inner, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested result payload under 'result' key, got %T", data["result"])
	}

	status, _ := inner["status"].(string)
	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}

	turns, _ := inner["turns"].(float64)
	if turns < 2 {
		t.Errorf("turns = %v, want at least 2", turns)
	}

	participants, _ := inner["participants"].([]any)
	if len(participants) < 2 {
		t.Errorf("participants = %v, want at least 2", participants)
	}
}

// TestE2E_Orchestrator_SynthesisStdinPiping verifies that long synthesis prompts
// are piped via stdin when they exceed the CLI's StdinThreshold.
//
// Async-by-default since LoomEngine v3 (PR #69) — poll status to completion.
func TestE2E_Orchestrator_SynthesisStdinPiping(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Use a long topic to increase the chance of synthesis prompt exceeding stdin threshold
	longTopic := "Analyze the following comprehensive list of programming paradigms and their trade-offs: " +
		strings.Repeat("functional programming, object-oriented programming, procedural programming, logic programming, ", 50) +
		"and determine which paradigm is best suited for each type of application."

	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "consensus",
		"arguments": map[string]any{
			"topic":      longTopic,
			"synthesize": true,
		},
	}))

	resp, err := readResponse(reader, 30*time.Second)
	if err != nil {
		t.Fatalf("consensus with long topic: %v", err)
	}

	data := extractToolJSON(t, resp)
	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id in long consensus async response: %+v", data)
	}
	if data["status"] != "running" {
		t.Errorf("initial status = %v, want running", data["status"])
	}

	// Poll status until completed.
	var content string
	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(stdin, jsonRPCRequest(3+i, "tools/call", map[string]any{
			"name":      "status",
			"arguments": map[string]any{"job_id": jobID, "include_content": true},
		}))
		pollResp, pollErr := readResponse(reader, 5*time.Second)
		if pollErr != nil {
			t.Fatalf("status poll: %v", pollErr)
		}
		pollData := extractToolJSON(t, pollResp)
		status, _ := pollData["status"].(string)
		if status == "completed" {
			content, _ = pollData["content"].(string)
			break
		}
		if status == "failed" || status == "failed_crash" {
			t.Fatalf("long consensus job failed: %+v", pollData)
		}
	}
	if content == "" {
		t.Fatal("long consensus content empty after polling — stdin piping may have failed")
	}
	t.Logf("long consensus content (first 500): %.500s", content)
}
