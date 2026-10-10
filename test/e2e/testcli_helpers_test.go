package e2e

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	e2eTestCLIBuildMu   sync.Mutex
	e2eTestCLIBuildPath string
)

// buildTestCLI compiles the testcli binary and returns the path.
func buildTestCLI(t *testing.T) string {
	t.Helper()

	binName := "testcli"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}

	e2eTestCLIBuildMu.Lock()
	cachedPath := e2eTestCLIBuildPath
	if cachedPath == "" {
		cacheDir := filepath.Join(os.TempDir(), "aimux-e2e-build-cache")
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			e2eTestCLIBuildMu.Unlock()
			t.Fatalf("mkdir testcli build cache: %v", err)
		}
		cachedPath = filepath.Join(cacheDir, binName)

		cmd := exec.Command("go", "build", "-o", cachedPath, "./cmd/testcli")
		cmd.Dir = projectRoot()
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

		out, err := cmd.CombinedOutput()
		if err != nil {
			e2eTestCLIBuildMu.Unlock()
			t.Fatalf("build testcli: %v\n%s", err, out)
		}
		e2eTestCLIBuildPath = cachedPath
	}
	e2eTestCLIBuildMu.Unlock()

	binPath := filepath.Join(t.TempDir(), binName)
	copyFileForTest(t, cachedPath, binPath)
	return binPath
}

// startServerWithTestCLI launches aimux in daemon+shim mode with testcli on
// PATH so CLI profiles find the testcli binary during probe.
func startServerWithTestCLI(t *testing.T, aimuxBin, testcliBin string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()
	configDir := filepath.Join(testdataDir(), "config")
	testcliDir := filepath.Dir(testcliBin)
	return startDaemonAndShim(t, aimuxBin, testcliDir, configDir)
}

// initTestCLIServer builds both binaries, starts the server with testcli on
// PATH, and initializes the MCP session.
func initTestCLIServer(t *testing.T) (io.WriteCloser, *bufio.Reader) {
	t.Helper()

	aimuxBin := buildBinary(t)
	testcliBin := buildTestCLI(t)

	_, stdin, reader := startServerWithTestCLI(t, aimuxBin, testcliBin)
	initializeMCP(t, stdin, reader)
	return stdin, reader
}

func initializeMCP(t *testing.T, stdin io.Writer, reader *bufio.Reader) {
	t.Helper()
	initializeMCPWhenReady(t, stdin, reader, map[string]any{
		"tasks": map[string]any{
			"requests": map[string]any{
				"tools": map[string]any{
					"call": map[string]any{},
				},
			},
		},
	})
}

func initializeMCPWithResponse(t *testing.T, stdin io.Writer, reader *bufio.Reader) map[string]any {
	t.Helper()
	return initializeMCPWhenReady(t, stdin, reader, map[string]any{})
}

func initializeMCPWhenReady(t *testing.T, stdin io.Writer, reader *bufio.Reader, capabilities map[string]any) map[string]any {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var response map[string]any
	for attempt := 1; ; attempt++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("initialize deadline exceeded: %#v", response)
		}
		if _, err := fmt.Fprint(stdin, jsonRPCRequest(100+attempt, "initialize", map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    capabilities,
			"clientInfo":      map[string]any{"name": "e2e-testcli", "version": "1.0"},
		})); err != nil {
			t.Fatalf("initialize request: %v", err)
		}
		remaining = time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("initialize deadline exceeded after request write: %#v", response)
		}
		var err error
		response, err = readResponse(reader, remaining)
		if err != nil {
			t.Fatalf("initialize: %v", err)
		}
		if _, ok := response["result"].(map[string]any); ok {
			if time.Now().After(deadline) {
				t.Fatalf("initialize succeeded after deadline: %#v", response)
			}
			return response
		}
		rpcError, _ := response["error"].(map[string]any)
		code, _ := rpcError["code"].(float64)
		message, _ := rpcError["message"].(string)
		if code != -32603 || !strings.HasPrefix(message, "project not connected: ") {
			t.Fatalf("initialize result missing: %#v", response)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
