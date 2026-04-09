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
func startServer(t *testing.T, binPath string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()

	configDir := filepath.Join(testdataDir(), "config")
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), "AIMUX_CONFIG_DIR="+configDir)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	cmd.Stderr = os.Stderr // let server errors appear in test output

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

func TestE2E_Initialize(t *testing.T) {
	bin := buildBinary(t)
	_, stdin, reader := startServer(t, bin)

	// Send initialize request
	req := jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	})
	fmt.Fprint(stdin, req)

	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("initialize response: %v", err)
	}

	// Verify response structure
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}

	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo == nil {
		t.Fatal("missing serverInfo")
	}
	if serverInfo["name"] != "aimux" {
		t.Errorf("serverInfo.name = %v, want aimux", serverInfo["name"])
	}

	caps, _ := result["capabilities"].(map[string]any)
	if caps == nil {
		t.Fatal("missing capabilities")
	}
	if caps["tools"] == nil {
		t.Error("missing tools capability")
	}

	// Send initialized notification
	fmt.Fprint(stdin, jsonRPCNotification("notifications/initialized"))
}

func TestE2E_ToolsList(t *testing.T) {
	bin := buildBinary(t)
	_, stdin, reader := startServer(t, bin)

	// Initialize first
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	}))
	_, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Send tools/list
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

	// Should have 11 tools
	if len(tools) < 10 {
		t.Errorf("expected at least 10 tools, got %d", len(tools))
	}

	// Verify key tools exist
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		tm, _ := tool.(map[string]any)
		if name, ok := tm["name"].(string); ok {
			toolNames[name] = true
		}
	}

	required := []string{"exec", "status", "sessions", "dialog", "agents", "audit", "think", "consensus", "debate", "deepresearch"}
	for _, name := range required {
		if !toolNames[name] {
			t.Errorf("missing required tool: %s", name)
		}
	}
}

func TestE2E_ExecSync(t *testing.T) {
	bin := buildBinary(t)
	_, stdin, reader := startServer(t, bin)

	// Initialize
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	}))
	if _, err := readResponse(reader, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Call exec tool with echo CLI — should echo prompt back
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "e2e test payload",
			"cli":    "echo-cli",
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
	// Echo CLI should echo the prompt back as content
	if execResult["content"] == nil {
		t.Error("missing content in exec result")
	}
}

func TestE2E_SessionsList(t *testing.T) {
	bin := buildBinary(t)
	_, stdin, reader := startServer(t, bin)

	// Initialize
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	}))
	if _, err := readResponse(reader, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Run an exec first to create a session
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{"prompt": "create session", "cli": "echo-cli"},
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

	count, _ := sessResult["count"].(float64)
	if count < 1 {
		t.Errorf("expected at least 1 session, got %v", count)
	}
}

func TestE2E_ThinkTool(t *testing.T) {
	bin := buildBinary(t)
	_, stdin, reader := startServer(t, bin)

	// Initialize
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	}))
	if _, err := readResponse(reader, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Think tool — in-process, no CLI needed
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "think",
		"arguments": map[string]any{
			"pattern": "critical_thinking",
			"issue":   "is this e2e test working?",
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

	if thinkResult["pattern"] != "critical_thinking" {
		t.Errorf("pattern = %v, want critical_thinking", thinkResult["pattern"])
	}
	if thinkResult["mode"] != "solo" {
		t.Errorf("mode = %v, want solo", thinkResult["mode"])
	}
}

func TestE2E_InvalidTool(t *testing.T) {
	bin := buildBinary(t)
	_, stdin, reader := startServer(t, bin)

	// Initialize
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	}))
	if _, err := readResponse(reader, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}

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
