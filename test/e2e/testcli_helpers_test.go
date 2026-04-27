package e2e

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-testcli", "version": "1.0"},
	}))
	if _, err := readResponse(reader, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}
}

