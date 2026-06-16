package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/mcp-mux/muxcore/registry"
)

func TestE2E_MuxcoreRegistryDescriptor(t *testing.T) {
	aimuxBin := buildBinary(t)
	testcliBin := buildTestCLI(t)
	configDir := filepath.Join(testdataDir(), "config")

	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	engineName := "aimux-reg-" + hex.EncodeToString(suffix[:])

	isolatedTmp, err := os.MkdirTemp(os.TempDir(), "ae-reg")
	if err != nil {
		t.Fatalf("create isolated tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(isolatedTmp) })

	tempEnvName := strings.Join([]string{"TE", "MP"}, "")
	pathEnv := filepath.Dir(testcliBin) + string(os.PathListSeparator) + os.Getenv("PATH")
	env := append(os.Environ(),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME="+engineName,
		"AIMUX_WARMUP=false",
		"AIMUX_SESSION_STORE=memory",
		"PATH="+pathEnv,
		"TMPDIR="+isolatedTmp,
		tempEnvName+"="+isolatedTmp,
		"TMP="+isolatedTmp,
	)

	ctlSock := filepath.Join(isolatedTmp, engineName+"-muxd.ctl.sock")
	daemonCmd := exec.Command(aimuxBin, "--muxcore-daemon")
	daemonCmd.Env = env
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		cleanupDaemon(t, ctlSock, daemonCmd, "TestE2E_MuxcoreRegistryDescriptor")
	})

	if err := waitForCtlSocket(ctlSock, 30*time.Second); err != nil {
		t.Fatalf("daemon readiness: %v (name=%s)", err, engineName)
	}

	rec := waitForRegistryDescriptor(t, isolatedTmp, engineName, 5*time.Second)
	d := rec.Descriptor
	if d.ProductName != "aimux" {
		t.Fatalf("ProductName = %q, want aimux; descriptor=%+v", d.ProductName, d)
	}
	if d.MuxcoreVersion == "" {
		t.Fatalf("MuxcoreVersion is empty; descriptor=%+v", d)
	}
	if !d.Capabilities.ListOwners {
		t.Fatalf("Capabilities.ListOwners = false, want true; descriptor=%+v", d)
	}
	if d.EngineName != engineName {
		t.Fatalf("EngineName = %q, want %q; descriptor=%+v", d.EngineName, engineName, d)
	}
	if d.DaemonControlPath != ctlSock {
		t.Fatalf("DaemonControlPath = %q, want %q; descriptor=%+v", d.DaemonControlPath, ctlSock, d)
	}

	verified := registry.VerifyDescriptor(rec)
	if verified.State != registry.StateHealthy || !verified.Reachable {
		t.Fatalf("VerifyDescriptor = state=%q reachable=%v reason=%q record=%+v", verified.State, verified.Reachable, verified.Reason, rec)
	}
}

func waitForRegistryDescriptor(t *testing.T, baseDir, engineName string, timeout time.Duration) registry.Record {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		records, err := registry.ListDescriptors(baseDir)
		if err != nil {
			lastErr = err
		} else if rec, err := registry.ResolveEngine(records, engineName); err == nil {
			return rec
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("registry descriptor for %q not found within %v: %v", engineName, timeout, lastErr)
	return registry.Record{}
}
