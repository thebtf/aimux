//go:build !short

// T010 — @critical NFR-1 TestPersistentSession_ColdStartUnderTwoSeconds.
//
// AIMUX-14 CR-001 Phase 1 — verifies the cold-start latency budget
// (subprocess fork + auth + first prompt parse + first response start) for a
// persistent CLI session stays under the spec NFR-1 ceiling of 2 seconds.
//
// The test spawns a real subprocess (cmd/persistent_testcli) — anti-stub
// requirement: replacing the binary with a no-op mock causes the test to time
// out because no process emits the sentinel line.

package critical

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/session"
)

// buildPersistentTestCLI compiles cmd/persistent_testcli into the test's temp
// directory and returns the absolute path of the produced binary.
func buildPersistentTestCLI(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	binName := "persistent_testcli"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(t.TempDir(), binName)
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/persistent_testcli")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build persistent_testcli: %v\n%s", err, out)
	}
	return binPath
}

func TestCritical_PersistentSession_ColdStartUnderTwoSeconds(t *testing.T) {
	bin := buildPersistentTestCLI(t)

	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	sess := session.New(
		"cold-start-test",
		stdin,
		stdout,
		5*time.Second, // inactivityTimeout
		nil, nil,
		`^===END===$`,
	)
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	res, err := sess.Send(ctx, "hello")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res == nil {
		t.Fatal("Send returned nil result")
	}
	if res.Content == "" {
		t.Fatal("Send returned empty content")
	}

	const nfr1Budget = 2 * time.Second
	if elapsed > nfr1Budget {
		t.Errorf("NFR-1: cold-start Send took %v, want ≤ %v (content: %q)",
			elapsed, nfr1Budget, res.Content)
	}

	t.Logf("cold-start latency: %v (budget %v)", elapsed, nfr1Budget)
}
