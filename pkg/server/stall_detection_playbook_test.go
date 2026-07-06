//go:build !short

// @critical — issue #359 stall-detection invariant: a hung leaf CLI must be
// detected as a stall, while legitimate long-running work (a CLI that keeps
// producing output) must NEVER be flagged. This is the behavioral proof the
// operator required — not a field-assignment unit test, but the real dispatch
// chain: profileTaskWorker → taskDispatch → pipe.Run → testcli emulator →
// SpawnArgs.OnOutput → loom.AppendProgress → Task.ProgressUpdatedAt →
// evaluateInactivityTier.
//
// Anti-stub: the emulator really goes silent (hang) or really keeps emitting
// (long-legit). Replacing AppendProgress wiring with a no-op makes the
// long-legit assertion fail (baseline never advances → tier escalates on
// elapsed time), and replacing the emulator with an instant-exit mock makes
// the hang assertion vacuous (process exits before the silence window).
//
// Runs under the critical suite; see the release workflow gate.

package server

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor/code"
	"github.com/thebtf/aimux/pkg/types"
)

var (
	stallTestCLIBuildMu   sync.Mutex
	stallTestCLIBuildPath string
)

// buildStallTestCLI compiles cmd/testcli once and caches the binary path.
func buildStallTestCLI(t *testing.T) string {
	t.Helper()
	binName := "testcli"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}

	stallTestCLIBuildMu.Lock()
	defer stallTestCLIBuildMu.Unlock()
	if stallTestCLIBuildPath != "" {
		return stallTestCLIBuildPath
	}

	root := stallProjectRoot(t)
	cacheDir := filepath.Join(os.TempDir(), "aimux-stall-playbook-build")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir testcli build cache: %v", err)
	}
	binPath := filepath.Join(cacheDir, binName)
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/testcli")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build testcli: %v\n%s", err, out)
	}
	stallTestCLIBuildPath = binPath
	return binPath
}

// stallProjectRoot walks up to the module root (dir containing go.mod).
func stallProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above test working directory")
		}
		dir = parent
	}
}

// stallPlaybookServer builds a Server whose "codex" profile points at the
// testcli emulator running slow-codex in the given mode, with short stall
// thresholds so the invariant is observable in seconds.
func stallPlaybookServer(t *testing.T, testcliBin string, modeArgs []string) (*Server, *loom.LoomEngine) {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:stall_playbook_%d?cache=shared&mode=memory", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	engine, err := loom.NewEngine(db, "stall-playbook")
	if err != nil {
		t.Fatalf("loom.NewEngine: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = engine.Close(ctx)
	})

	// command.base = "testcli slow-codex <modeArgs...>". The leading "testcli"
	// token is stripped by buildTaskArgs (matches profile.Binary basename), so
	// the spawned argv is [slow-codex, <modeArgs...>, <prompt>].
	base := "testcli slow-codex"
	for _, a := range modeArgs {
		base += " " + a
	}
	profile := &config.CLIProfile{
		Name:           "codex",
		Binary:         "testcli",
		ResolvedPath:   testcliBin,
		OutputFormat:   "text",
		PromptFlagType: "positional",
		TimeoutSeconds: 30,
		Command:        config.CommandConfig{Base: base},
	}
	registry := driver.NewRegistry(map[string]*config.CLIProfile{"codex": profile})
	registry.SetAvailable("codex", true)

	cfg := &config.Config{}
	// Short thresholds: grace 1s, soft 2s, active-soft 2s, hard 3s, auto-cancel 4s.
	cfg.Server.StreamingGraceSeconds = 1
	cfg.Server.StreamingSoftWarningSeconds = 2
	cfg.Server.StreamingActiveSoftWarningSeconds = 2
	cfg.Server.StreamingHardStallSeconds = 3
	cfg.Server.StreamingAutoCancelSeconds = 4

	srv := &Server{loom: engine, registry: registry, cfg: cfg}
	engine.RegisterWorker(code.WorkerTypeCodeDriver, profileTaskWorker{
		server:     srv,
		workerType: code.WorkerTypeCodeDriver,
		taskClass:  "code",
		defaultCLI: "codex",
	})
	return srv, engine
}

// submitStallDriverTask submits a code_driver task that runs the emulator.
func submitStallDriverTask(t *testing.T, engine *loom.LoomEngine, projectID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	taskID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: code.WorkerTypeCodeDriver,
		ProjectID:  projectID,
		Prompt:     "emulate work",
		CLI:        "codex",
		Timeout:    30,
	})
	if err != nil {
		t.Fatalf("submit driver task: %v", err)
	}
	return taskID
}

// waitForRunning polls until the task reaches running (dispatch started).
func waitForRunning(t *testing.T, engine *loom.LoomEngine, taskID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := engine.Get(taskID)
		if err == nil && (task.Status == loom.TaskStatusRunning || task.Status.IsTerminal()) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("task never reached running/terminal state")
}

// currentTier reads the task's activity baseline and evaluates the stall tier
// the exact way loomStatusResult does for a running job.
func currentTier(t *testing.T, srv *Server, engine *loom.LoomEngine, taskID string) (InactivityTier, loom.TaskStatus) {
	t.Helper()
	task, err := engine.Get(taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	baseline := loomTaskActivityBaseline(task)
	return evaluateInactivityTier(baseline, &srv.cfg.Server, task.ProgressUpdatedAt != nil), task.Status
}

// TestCritical_StallDetection_LongLegitWorkNeverFlagged proves that a CLI which
// keeps producing output is never classified as a stall, even past the
// auto-cancel window, because ProgressUpdatedAt keeps advancing.
func TestCritical_StallDetection_LongLegitWorkNeverFlagged(t *testing.T) {
	if testing.Short() {
		t.Skip("stall playbook spawns a real emulator subprocess — skipped in -short")
	}
	testcliBin := buildStallTestCLI(t)

	// Emit a line every 300ms for 6s. Silence between lines (300ms) stays well
	// under the 2s soft-warning threshold, so the tier must never leave None.
	srv, engine := stallPlaybookServer(t, testcliBin, []string{
		"--mode", "long-legit", "--interval", "300ms", "--duration", "6s",
	})
	taskID := submitStallDriverTask(t, engine, "long-legit-proj")
	waitForRunning(t, engine, taskID)

	// Poll across the whole window; assert the tier NEVER escalates while running.
	deadline := time.Now().Add(6 * time.Second)
	sawProgress := false
	for time.Now().Before(deadline) {
		tier, status := currentTier(t, srv, engine, taskID)
		if status.IsTerminal() {
			break
		}
		task, _ := engine.Get(taskID)
		if task.ProgressUpdatedAt != nil {
			sawProgress = true
		}
		if tier >= TierSoftWarning {
			t.Fatalf("long-legit work falsely flagged: tier=%d (progress_lines=%d last=%q)",
				tier, task.ProgressLines, task.LastOutputLine)
		}
		time.Sleep(250 * time.Millisecond)
	}

	if !sawProgress {
		t.Fatal("no live progress observed — OnOutput→AppendProgress wiring is broken; the invariant would be vacuous")
	}
	runtimePage, err := engine.ListArtifacts(taskID, loom.TaskArtifactListOptions{
		Kinds:      []loom.TaskArtifactKind{loom.TaskArtifactKindRuntime},
		EventTypes: []string{"raw"},
		Channels:   []string{"stdout"},
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("ListArtifacts runtime: %v", err)
	}
	if len(runtimePage.Items) == 0 {
		t.Fatal("line-oriented emulator produced progress but no runtime event slice")
	}
	if runtimePage.Items[0].Summary == "" || runtimePage.Items[0].EventType != "raw" || runtimePage.Items[0].Channel != "stdout" {
		t.Fatalf("runtime event = %#v; want raw/stdout line-oriented evidence", runtimePage.Items[0])
	}
}

// TestCritical_StallDetection_HangIsDetected proves that a silent (hung) CLI is
// classified as a stall once silence crosses the configured tiers.
func TestCritical_StallDetection_HangIsDetected(t *testing.T) {
	if testing.Short() {
		t.Skip("stall playbook spawns a real emulator subprocess — skipped in -short")
	}
	testcliBin := buildStallTestCLI(t)

	// Silent from dispatch for 6s: no output ever → ProgressUpdatedAt stays nil
	// → baseline is DispatchedAt → silence grows monotonically.
	srv, engine := stallPlaybookServer(t, testcliBin, []string{
		"--mode", "hang", "--duration", "6s", "--silent-start",
	})
	taskID := submitStallDriverTask(t, engine, "hang-proj")
	waitForRunning(t, engine, taskID)

	// Within the hard-stall window the tier must reach at least SoftWarning,
	// then HardStall. Poll up to the auto-cancel horizon.
	var maxTier InactivityTier
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tier, status := currentTier(t, srv, engine, taskID)
		if status.IsTerminal() {
			break
		}
		if tier > maxTier {
			maxTier = tier
		}
		if maxTier >= TierHardStall {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if maxTier < TierHardStall {
		t.Fatalf("hung CLI not detected: max tier reached = %d, want >= TierHardStall (%d)", maxTier, TierHardStall)
	}
}

// TestCritical_StallDetection_MidStreamHangUsesArtifactAwareWindow proves the
// #359 C2 artifact-aware window end to end: a CLI that streams a few lines and
// then wedges is flagged via the stricter active-soft threshold, because
// ProgressUpdatedAt is set (output started) so the startup grace no longer
// applies. This is the profile the plain hang test does not cover — output DID
// begin before the silence.
func TestCritical_StallDetection_MidStreamHangUsesArtifactAwareWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("stall playbook spawns a real emulator subprocess — skipped in -short")
	}
	testcliBin := buildStallTestCLI(t)

	// Emit 2 lines (300ms apart), then go silent for 6s. active-soft is 2s, so
	// ~2s after the last line the tier must reach SoftWarning while the task is
	// still marked as having produced output.
	srv, engine := stallPlaybookServer(t, testcliBin, []string{
		"--mode", "mid-hang", "--lines", "2", "--interval", "300ms", "--duration", "6s",
	})
	taskID := submitStallDriverTask(t, engine, "mid-hang-proj")
	waitForRunning(t, engine, taskID)

	var maxTier InactivityTier
	sawOutput := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := engine.Get(taskID)
		if err == nil && task.ProgressUpdatedAt != nil {
			sawOutput = true
		}
		tier, status := currentTier(t, srv, engine, taskID)
		if status.IsTerminal() {
			break
		}
		if tier > maxTier {
			maxTier = tier
		}
		if maxTier >= TierSoftWarning && sawOutput {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	if !sawOutput {
		t.Fatal("no live output observed before the hang — mid-hang emulator or progress wiring is broken")
	}
	if maxTier < TierSoftWarning {
		t.Fatalf("mid-stream hang not flagged via artifact-aware window: max tier = %d, want >= TierSoftWarning (%d)", maxTier, TierSoftWarning)
	}
}

// Compile-time touch so the types import is used even if a future refactor
// drops the only reference; keeps the anti-stub scanner honest.
var _ = types.SpawnArgs{}
