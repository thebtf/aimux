// Package e2e contains end-to-end tests that launch the aimux binary as a
// subprocess and communicate via MCP protocol (JSON-RPC over stdio).
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

// jsonRPCRequest builds a JSON-RPC 2.0 request as a single line (newline-delimited).
// mcp-go stdio transport uses newline-delimited JSON, not Content-Length framing.
func jsonRPCRequest(id int, method string, params any) string {
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}
	data, _ := json.Marshal(body)
	return string(data) + "\n"
}

// jsonRPCNotification builds a JSON-RPC notification (no id).
func jsonRPCNotification(method string) string {
	body := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	data, _ := json.Marshal(body)
	return string(data) + "\n"
}

// readResponse reads a newline-delimited JSON-RPC response from stdout.
func readResponse(reader *bufio.Reader, timeout time.Duration) (map[string]any, error) {
	done := make(chan map[string]any, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errCh <- fmt.Errorf("read line: %w", err)
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			var result map[string]any
			if err := json.Unmarshal([]byte(line), &result); err != nil {
				errCh <- fmt.Errorf("parse JSON: %w (line: %s)", err, line)
				return
			}
			// Skip MCP notifications (method present, no id = server-initiated)
			if _, hasMethod := result["method"]; hasMethod {
				if _, hasID := result["id"]; !hasID {
					continue // notification — skip, read next line
				}
			}
			done <- result
			return
		}
	}()

	select {
	case r := <-done:
		return r, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout after %v", timeout)
	}
}

// buildBinary compiles the aimux binary and returns the path.
func buildBinary(t *testing.T) string {
	t.Helper()

	binName := "aimux-test"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}

	binPath := filepath.Join(t.TempDir(), binName)

	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/aimux")
	cmd.Dir = projectRoot()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build aimux: %v\n%s", err, out)
	}

	return binPath
}

func testdataDir() string {
	// Find project root by looking for go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "test", "e2e", "testdata")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Fallback: try relative to source file
			_, file, _, _ := runtime.Caller(0)
			return filepath.Join(filepath.Dir(file), "testdata")
		}
		dir = parent
	}
}

func projectRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// startServer launches aimux with test config and returns stdin/stdout pipes.
//
// AIMUX-6 removed the AIMUX_NO_ENGINE=1 stdio-direct bypass. Tests now use
// the daemon+shim pair via startDaemonAndShim: aimux is spawned in daemon
// mode (control socket + IPC), a shim client bridges stdio to that daemon,
// and the test talks MCP over the shim's stdin/stdout just like a real
// MCP client. Signature is kept stable for call-site compatibility.
func startServer(t *testing.T, binPath string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()
	configDir := filepath.Join(testdataDir(), "config")
	return startDaemonAndShim(t, binPath, "", configDir)
}

func TestE2E_Initialize(t *testing.T) {
	// initTestCLIServer already performs initialize handshake.
	// We verify it succeeds by getting a working stdin/reader pair.
	stdin, reader := initTestCLIServer(t)

	// Verify the server responds to subsequent requests (confirms init worked).
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/list", map[string]any{}))
	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("tools/list after init: %v", err)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Error("no tools returned after initialize")
	}
}

func TestE2E_ToolsList(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Send tools/list on a fresh initialized server.
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/list", nil))

	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got %v", resp)
	}

	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %v", result["tools"])
	}
	if len(tools) == 0 {
		t.Fatal("expected tools/list to return at least one tool")
	}

	requiredTools := []string{"exec", "status", "sessions", "think", "investigate", "consensus", "debate", "dialog", "agents", "agent", "audit", "deepresearch", "workflow"}
	toolNames := make(map[string]bool, len(tools))
	var architectureAnalysis map[string]any
	for _, tool := range tools {
		tm, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tm["name"].(string)
		if name != "" {
			toolNames[name] = true
		}
		if name == "architecture_analysis" {
			architectureAnalysis = tm
		}
	}
	for _, name := range requiredTools {
		if !toolNames[name] {
			t.Fatalf("tools/list missing required tool: %s", name)
		}
	}

	if architectureAnalysis == nil {
		t.Fatal("tools/list missing architecture_analysis")
	}

	inputSchema, ok := architectureAnalysis["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("architecture_analysis missing inputSchema object: %v", architectureAnalysis["inputSchema"])
	}
	properties, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("architecture_analysis inputSchema missing properties object: %v", inputSchema["properties"])
	}
	components, ok := properties["components"].(map[string]any)
	if !ok {
		t.Fatalf("architecture_analysis missing components schema: %v", properties["components"])
	}
	if got := components["type"]; got != "array" {
		t.Fatalf("architecture_analysis components.type = %v, want array", got)
	}
	items, ok := components["items"].(map[string]any)
	if !ok {
		t.Fatalf("architecture_analysis components.items missing object schema: %v", components["items"])
	}
	if len(items) == 0 {
		t.Fatal("architecture_analysis components.items schema is empty")
	}
	if got := items["type"]; got == "object" {
		itemProps, ok := items["properties"].(map[string]any)
		if !ok || len(itemProps) == 0 {
			t.Fatalf("architecture_analysis components.items.properties missing/empty: %v", items["properties"])
		}
	} else {
		oneOf, ok := items["oneOf"].([]any)
		if !ok || len(oneOf) == 0 {
			t.Fatalf("architecture_analysis components.items must be object schema or oneOf, got: %v", items)
		}
		foundObject := false
		for _, candidate := range oneOf {
			obj, ok := candidate.(map[string]any)
			if !ok {
				continue
			}
			if obj["type"] == "object" {
				props, ok := obj["properties"].(map[string]any)
				if ok && len(props) > 0 {
					foundObject = true
					break
				}
			}
		}
		if !foundObject {
			t.Fatalf("architecture_analysis components.items.oneOf missing object schema with properties: %v", oneOf)
		}
	}
}

func TestE2E_ExecSync(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Call exec tool — should return CLI output
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "e2e test payload",
			"cli":    "codex",
			"async":  false,
		},
	}))

	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("exec response: %v", err)
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got %v", resp)
	}

	// Result should have content array with text
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content array, got %v", result)
	}

	firstContent, _ := content[0].(map[string]any)
	text, _ := firstContent["text"].(string)
	if text == "" {
		t.Fatal("expected non-empty text in response")
	}

	// Parse the JSON text to verify structure
	var execResult map[string]any
	if err := json.Unmarshal([]byte(text), &execResult); err != nil {
		t.Fatalf("exec result not JSON: %v (text: %s)", err, text)
	}

	if execResult["session_id"] == nil {
		t.Error("missing session_id in exec result")
	}
	if execResult["status"] != "completed" {
		t.Errorf("status = %v, want completed", execResult["status"])
	}
	// Budget policy: default-brief omits content; content_length + truncated hint appear instead.
	// This assertion validates the brief contract rather than the old full-payload shape.
	if execResult["content_length"] == nil {
		t.Error("missing content_length in exec result (brief default)")
	}
}

func TestE2E_SessionsList(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Run an exec first to create a session
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{"prompt": "create session", "cli": "codex"},
	}))
	if _, err := readResponse(reader, 10*time.Second); err != nil {
		t.Fatalf("exec: %v", err)
	}

	// List sessions — should have at least 1
	fmt.Fprint(stdin, jsonRPCRequest(3, "tools/call", map[string]any{
		"name":      "sessions",
		"arguments": map[string]any{"action": "list"},
	}))

	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("sessions list: %v", err)
	}

	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content in sessions response")
	}

	firstContent, _ := content[0].(map[string]any)
	text, _ := firstContent["text"].(string)

	var sessResult map[string]any
	json.Unmarshal([]byte(text), &sessResult)

	sessions, _ := sessResult["sessions"].([]any)
	if len(sessions) < 1 {
		t.Errorf("expected at least 1 session in sessions array, got %d", len(sessions))
	}
	sessionsPage, _ := sessResult["sessions_pagination"].(map[string]any)
	total, _ := sessionsPage["total"].(float64)
	if total < 1 {
		t.Errorf("expected sessions_pagination.total >= 1, got %v", total)
	}
}

func TestE2E_ThinkTool(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// critical_thinking is now a per-pattern tool — call by name directly.
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "critical_thinking",
		"arguments": map[string]any{
			"issue": "is this e2e test working?",
		},
	}))

	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("think: %v", err)
	}

	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content from think tool")
	}

	firstContent, _ := content[0].(map[string]any)
	text, _ := firstContent["text"].(string)

	var thinkResult map[string]any
	json.Unmarshal([]byte(text), &thinkResult)

	// think responses are wrapped in the guidance envelope; domain fields are
	// nested under the "result" key.
	inner, ok := thinkResult["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested result payload under 'result' key, got %T: %v", thinkResult["result"], thinkResult["result"])
	}
	if inner["pattern"] != "critical_thinking" {
		t.Errorf("pattern = %v, want critical_thinking", inner["pattern"])
	}
	if inner["mode"] != "solo" {
		t.Errorf("mode = %v, want solo", inner["mode"])
	}
}

func TestE2E_InvalidTool(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Call nonexistent tool
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name":      "nonexistent_tool",
		"arguments": map[string]any{},
	}))

	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("invalid tool: %v", err)
	}

	// Should get an error response
	if resp["error"] == nil {
		t.Error("expected error for nonexistent tool")
	}
}
