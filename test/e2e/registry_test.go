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
	engineName := "ar-" + hex.EncodeToString(suffix[:])

	isolatedTmp := newMuxcoreIsolatedTemp(t, "rg")

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

	var ctlSock string
	daemonCmd := exec.Command(aimuxBin, "--muxcore-daemon")
	daemonCmd.Env = env
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		cleanupDaemon(t, ctlSock, daemonCmd, "TestE2E_MuxcoreRegistryDescriptor")
	})

	rec := waitForHealthyRegistryDescriptor(t, isolatedTmp, engineName, 30*time.Second)
	d := rec.Descriptor
	ctlSock = d.DaemonControlPath
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
	if d.DaemonControlPath == filepath.Join(isolatedTmp, engineName+"-muxd.ctl.sock") {
		t.Fatalf("DaemonControlPath = %q uses display EngineName as transport namespace; descriptor=%+v", d.DaemonControlPath, d)
	}

	verified := registry.VerifyDescriptor(rec)
	if verified.State != registry.StateHealthy || !verified.Reachable {
		t.Fatalf("VerifyDescriptor = state=%q reachable=%v reason=%q record=%+v", verified.State, verified.Reachable, verified.Reason, rec)
	}
}

func TestE2E_DefaultEngineNameIsStableAimux(t *testing.T) {
	aimuxBin := buildBinary(t)
	testcliBin := buildTestCLI(t)
	configDir := filepath.Join(testdataDir(), "config")

	customBin := filepath.Join(t.TempDir(), "aimux-display-label-smoke.exe")
	copyFileForTest(t, aimuxBin, customBin)

	isolatedTmp := newMuxcoreIsolatedTemp(t, "rn")

	tempEnvName := strings.Join([]string{"TE", "MP"}, "")
	pathEnv := filepath.Dir(testcliBin) + string(os.PathListSeparator) + os.Getenv("PATH")
	env := append(os.Environ(),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME=",
		"AIMUX_WARMUP=false",
		"AIMUX_SESSION_STORE=memory",
		"PATH="+pathEnv,
		"TMPDIR="+isolatedTmp,
		tempEnvName+"="+isolatedTmp,
		"TMP="+isolatedTmp,
	)

	var ctlSock string
	daemonCmd := exec.Command(customBin, "--muxcore-daemon")
	daemonCmd.Env = env
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		cleanupDaemon(t, ctlSock, daemonCmd, "TestE2E_DefaultEngineNameIsStableAimux")
	})

	rec := waitForHealthyRegistryDescriptor(t, isolatedTmp, "aimux", 30*time.Second)
	d := rec.Descriptor
	ctlSock = d.DaemonControlPath
	if d.EngineName != "aimux" {
		t.Fatalf("EngineName = %q, want aimux; descriptor=%+v", d.EngineName, d)
	}
	if d.DaemonControlPath == filepath.Join(isolatedTmp, "aimux-display-label-smoke-muxd.ctl.sock") {
		t.Fatalf("DaemonControlPath = %q used binary basename as transport namespace; descriptor=%+v", d.DaemonControlPath, d)
	}
	if d.DaemonControlPath == filepath.Join(isolatedTmp, "aimux-muxd.ctl.sock") {
		t.Fatalf("DaemonControlPath = %q used raw display label as transport namespace; descriptor=%+v", d.DaemonControlPath, d)
	}

	verified := registry.VerifyDescriptor(rec)
	if verified.State != registry.StateHealthy || !verified.Reachable || verified.StatusEngineName != "aimux" {
		t.Fatalf("VerifyDescriptor = state=%q reachable=%v status_engine_name=%q reason=%q record=%+v",
			verified.State, verified.Reachable, verified.StatusEngineName, verified.Reason, rec)
	}
}
