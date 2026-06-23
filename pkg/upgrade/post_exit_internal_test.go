package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	muxengine "github.com/thebtf/mcp-mux/muxcore/engine"
)

func TestCoordinatorApply_AutoUsesPostExitInstallWhenRequired(t *testing.T) {
	restore := setPostExitInstallRequiredForTest(func() bool { return true })
	defer restore()

	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-next.exe")
	writeTestFile(t, binaryPath, "v1")
	writeTestFile(t, sourcePath, "v2")

	mock := &testSessionHandler{}
	var postExitCalled bool
	var got PostExitInstallOptions
	var muxcoreCalled bool
	coord := &Coordinator{
		Version:        "5.14.2",
		BinaryPath:     binaryPath,
		Source:         sourcePath,
		EngineMode:     true,
		SessionHandler: mock,
		PostExitInstall: func(ctx context.Context, opts PostExitInstallOptions) error {
			postExitCalled = true
			got = opts
			return nil
		},
		ApplyUpdateAndRestart: func(ctx context.Context, opts muxengine.UpdateAndRestartOptions) (muxengine.UpdateAndRestartResult, error) {
			muxcoreCalled = true
			return muxengine.UpdateAndRestartResult{}, nil
		},
	}

	result, err := coord.Apply(context.Background(), ModeAuto, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !postExitCalled {
		t.Fatal("expected post-exit installer to be called")
	}
	if muxcoreCalled {
		t.Fatal("did not expect muxcore swap-first helper on post-exit platform")
	}
	if got.CurrentExe != binaryPath {
		t.Fatalf("CurrentExe = %q, want %q", got.CurrentExe, binaryPath)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatalf("EvalSymlinks source: %v", err)
	}
	if got.StagedExe == resolvedSource {
		t.Fatalf("StagedExe = %q, want copied staging path distinct from source", got.StagedExe)
	}
	if filepath.Dir(got.StagedExe) != filepath.Dir(binaryPath) {
		t.Fatalf("StagedExe dir = %q, want binary dir %q", filepath.Dir(got.StagedExe), filepath.Dir(binaryPath))
	}
	gotStaged, err := os.ReadFile(got.StagedExe)
	if err != nil {
		t.Fatalf("ReadFile StagedExe: %v", err)
	}
	if string(gotStaged) != "v2" {
		t.Fatalf("StagedExe payload = %q, want v2", gotStaged)
	}
	if _, err := os.Stat(resolvedSource); err != nil {
		t.Fatalf("source path should remain available after staging: %v", err)
	}
	if got.DaemonFlag == "" {
		t.Fatal("expected daemon flag for replacement start")
	}
	if !mock.pendingCalled {
		t.Fatal("expected SetUpdatePending after scheduling post-exit install")
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred", result.Method)
	}
	if result.HandoffError == "" {
		t.Fatal("expected handoff error explaining post-exit reconnect")
	}
}

func TestCoordinatorApply_ActiveEngineFileSkipsPostExitInstallWhenRequired(t *testing.T) {
	restore := setPostExitInstallRequiredForTest(func() bool { return true })
	defer restore()

	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux-launcher.exe")
	sourcePath := filepath.Join(dir, "aimux-engine-next.exe")
	activeEngineFile := filepath.Join(dir, "active.txt")
	writeTestFile(t, binaryPath, "launcher")
	writeTestFile(t, sourcePath, "engine-v2")

	var successorCalled bool
	var postExitCalled bool
	coord := &Coordinator{
		Version:          "5.14.2",
		BinaryPath:       binaryPath,
		Source:           sourcePath,
		EngineMode:       true,
		ActiveEngineFile: activeEngineFile,
		SessionHandler:   &testSessionHandler{},
		RestartWithSuccessor: func(ctx context.Context, opts muxengine.RestartWithSuccessorOptions) (muxengine.UpdateAndRestartResult, error) {
			successorCalled = true
			if opts.SuccessorExe == "" {
				t.Fatal("SuccessorExe is empty")
			}
			return muxengine.UpdateAndRestartResult{
				DaemonWasRunning:   true,
				GracefulRestarted:  true,
				ReplacementStarted: true,
				ReplacementReady:   true,
			}, nil
		},
		PostExitInstall: func(context.Context, PostExitInstallOptions) error {
			postExitCalled = true
			return nil
		},
	}

	result, err := coord.Apply(context.Background(), ModeAuto, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !successorCalled {
		t.Fatal("expected RestartWithSuccessor to be called")
	}
	if postExitCalled {
		t.Fatal("active-pointer topology must not use post-exit install")
	}
	if result.Method != "hot_swap" {
		t.Fatalf("Method = %q, want hot_swap", result.Method)
	}
}

func TestWriteActiveEnginePointerReplacesExistingPointer(t *testing.T) {
	dir := t.TempDir()
	pointerPath := filepath.Join(dir, "active.txt")
	firstSuccessor := filepath.Join(dir, "aimux-engine-v1.exe")
	nextSuccessor := filepath.Join(dir, "aimux-engine-v2.exe")
	writeTestFile(t, pointerPath, firstSuccessor+"\n")

	if err := writeActiveEnginePointer(pointerPath, nextSuccessor); err != nil {
		t.Fatalf("writeActiveEnginePointer: %v", err)
	}
	got, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("ReadFile pointer: %v", err)
	}
	want, err := filepath.Abs(nextSuccessor)
	if err != nil {
		t.Fatalf("Abs nextSuccessor: %v", err)
	}
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("pointer = %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

// TestWriteActiveEnginePointer_RestoresPriorPointerOnDoubleRenameFailure proves
// the PRC 2026-06-23 F1 fix: when the initial rename fails AND the post-remove
// retry rename also fails, the prior active-engine pointer content is restored
// rather than left permanently missing.
func TestWriteActiveEnginePointer_RestoresPriorPointerOnDoubleRenameFailure(t *testing.T) {
	dir := t.TempDir()
	pointerPath := filepath.Join(dir, "active.txt")
	priorSuccessor := filepath.Join(dir, "aimux-engine-prior.exe")
	nextSuccessor := filepath.Join(dir, "aimux-engine-next.exe")
	priorAbs, err := filepath.Abs(priorSuccessor)
	if err != nil {
		t.Fatalf("Abs prior: %v", err)
	}
	writeTestFile(t, pointerPath, priorAbs+"\n")

	// Force every rename attempt to fail, reproducing the Windows
	// rename-then-remove-then-retry double-failure window.
	origRename := renameActiveEnginePointer
	renameActiveEnginePointer = func(_, _ string) error {
		return errors.New("simulated rename failure")
	}
	defer func() { renameActiveEnginePointer = origRename }()

	wErr := writeActiveEnginePointer(pointerPath, nextSuccessor)
	if wErr == nil {
		t.Fatalf("writeActiveEnginePointer = nil, want error on double rename failure")
	}

	// The live pointer must still exist and still hold the PRIOR content.
	got, readErr := os.ReadFile(pointerPath)
	if readErr != nil {
		t.Fatalf("prior pointer was destroyed (ReadFile: %v) — F1 regression", readErr)
	}
	if strings.TrimSpace(string(got)) != priorAbs {
		t.Fatalf("pointer = %q, want prior %q restored", strings.TrimSpace(string(got)), priorAbs)
	}
	// The error must wrap the rename failure for diagnosability (F5).
	if !strings.Contains(wErr.Error(), "rename") && !strings.Contains(wErr.Error(), "promote") {
		t.Fatalf("error = %q, want it to mention the rename/promote failure", wErr.Error())
	}
}

// TestWriteActiveEnginePointer_AbortsBeforeRemoveWhenPriorPointerUnreadable
// proves the adversarial-verify 2026-06-23 MUST-FIX: when the existing pointer
// exists but cannot be read (non-ErrNotExist error), the function aborts BEFORE
// os.Remove rather than deleting an unrecoverable pointer. The live file must
// survive untouched and the read error must be surfaced.
func TestWriteActiveEnginePointer_AbortsBeforeRemoveWhenPriorPointerUnreadable(t *testing.T) {
	dir := t.TempDir()
	pointerPath := filepath.Join(dir, "active.txt")
	priorSuccessor := filepath.Join(dir, "aimux-engine-prior.exe")
	nextSuccessor := filepath.Join(dir, "aimux-engine-next.exe")
	priorAbs, err := filepath.Abs(priorSuccessor)
	if err != nil {
		t.Fatalf("Abs prior: %v", err)
	}
	writeTestFile(t, pointerPath, priorAbs+"\n")

	// First rename always fails -> enters the recovery path.
	origRename := renameActiveEnginePointer
	renameActiveEnginePointer = func(_, _ string) error {
		return errors.New("simulated rename failure")
	}
	defer func() { renameActiveEnginePointer = origRename }()

	// The prior pointer exists but is unreadable (EACCES-like), NOT absent.
	origRead := readActiveEnginePointer
	readActiveEnginePointer = func(_ string) ([]byte, error) {
		return nil, fmt.Errorf("simulated unreadable pointer: %w", os.ErrPermission)
	}
	defer func() { readActiveEnginePointer = origRead }()

	wErr := writeActiveEnginePointer(pointerPath, nextSuccessor)
	if wErr == nil {
		t.Fatalf("writeActiveEnginePointer = nil, want abort error on unreadable prior pointer")
	}

	// The live pointer must be untouched — abort happened BEFORE os.Remove.
	got, readErr := os.ReadFile(pointerPath)
	if readErr != nil {
		t.Fatalf("prior pointer was destroyed (ReadFile: %v) — MUST-FIX regression", readErr)
	}
	if strings.TrimSpace(string(got)) != priorAbs {
		t.Fatalf("pointer = %q, want prior %q untouched", strings.TrimSpace(string(got)), priorAbs)
	}
	if !errors.Is(wErr, os.ErrPermission) {
		t.Fatalf("error = %q, want it to wrap the read permission error", wErr.Error())
	}
}

func TestCoordinatorApply_PostExitInstallStartsWatchdogAndRegistersDrainFallback(t *testing.T) {
	restore := setPostExitInstallRequiredForTest(func() bool { return true })
	defer restore()

	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-next.exe")
	writeTestFile(t, binaryPath, "v1")
	writeTestFile(t, sourcePath, "v2")

	mock := &testLaunchSessionHandler{}
	var postExitCalls int
	var got PostExitInstallOptions
	coord := &Coordinator{
		Version:        "5.14.2",
		BinaryPath:     binaryPath,
		Source:         sourcePath,
		EngineMode:     true,
		SessionHandler: mock,
		GracefulRestart: func(context.Context, int) error {
			t.Fatal("post-exit install must not use muxcore graceful restart; it re-execs the old locked binary")
			return nil
		},
		PostExitInstall: func(ctx context.Context, opts PostExitInstallOptions) error {
			postExitCalls++
			got = opts
			return ctx.Err()
		},
	}

	result, err := coord.Apply(context.Background(), ModeAuto, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if postExitCalls != 1 {
		t.Fatalf("post-exit installer calls after Apply = %d, want 1 watchdog launch", postExitCalls)
	}
	if !mock.pendingCalled {
		t.Fatal("expected SetUpdatePending after scheduling post-exit install")
	}
	if mock.launcher == nil {
		t.Fatal("expected idempotent delayed post-exit launcher fallback to be registered")
	}
	if !mock.stopScheduled {
		t.Fatal("expected post-exit path to schedule daemon stop after watchdog launch")
	}
	if mock.stopDelay != postExitStopDelay {
		t.Fatalf("stop delay = %s, want %s", mock.stopDelay, postExitStopDelay)
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred", result.Method)
	}

	if err := mock.launcher(); err != nil {
		t.Fatalf("delayed launcher fallback: %v", err)
	}
	if postExitCalls != 1 {
		t.Fatalf("post-exit installer calls after fallback = %d, want idempotent 1", postExitCalls)
	}
	if got.CurrentExe != binaryPath {
		t.Fatalf("CurrentExe = %q, want %q", got.CurrentExe, binaryPath)
	}
	if filepath.Dir(got.StagedExe) != filepath.Dir(binaryPath) {
		t.Fatalf("StagedExe dir = %q, want binary dir %q", filepath.Dir(got.StagedExe), filepath.Dir(binaryPath))
	}
	if got.WaitTimeout != postExitInstallWatchdogTimeout {
		t.Fatalf("WaitTimeout = %s, want %s", got.WaitTimeout, postExitInstallWatchdogTimeout)
	}
}

func TestCoordinatorApply_HotSwapRejectsPostExitInstall(t *testing.T) {
	restore := setPostExitInstallRequiredForTest(func() bool { return true })
	defer restore()

	coord := &Coordinator{
		Version:         "5.14.2",
		BinaryPath:      filepath.Join(t.TempDir(), "aimux.exe"),
		EngineMode:      true,
		PostExitInstall: func(context.Context, PostExitInstallOptions) error { return nil },
	}

	_, err := coord.Apply(context.Background(), ModeHotSwap, true)
	if err == nil {
		t.Fatal("expected hot_swap to reject post-exit install")
	}
	if got := err.Error(); got == "" || !containsAll(got, "hot-swap", "post-exit") {
		t.Fatalf("error = %q, want hot-swap/post-exit detail", got)
	}
}

func TestPrepareStagedUpdate_LocalSourceCopiesBesideCurrentBinary(t *testing.T) {
	dir := t.TempDir()
	binaryDir := filepath.Join(dir, "bin")
	sourceDir := filepath.Join(dir, "sources")
	if err := os.MkdirAll(binaryDir, 0o700); err != nil {
		t.Fatalf("MkdirAll binaryDir: %v", err)
	}
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sourceDir: %v", err)
	}
	binaryPath := filepath.Join(binaryDir, "aimux.exe")
	sourcePath := filepath.Join(sourceDir, "aimux-next.exe")
	staleStagedPath := filepath.Join(binaryDir, "aimux-update-1-2-0.bin")
	staleSafeStagedPath := filepath.Join(binaryDir, "aimux-stage-1-2-0.exe")
	staleLegacyTempPath := filepath.Join(binaryDir, "aimux-update-1-2-0.exe.123.tmp")
	staleDotLegacyTempPath := filepath.Join(binaryDir, ".aimux-update-1-2-0.exe.456.tmp")
	writeTestFile(t, binaryPath, "v1")
	writeTestFile(t, sourcePath, "v2")
	writeTestFile(t, staleStagedPath, "stale")
	writeTestFile(t, staleSafeStagedPath, "stale")
	writeTestFile(t, staleLegacyTempPath, "stale")
	writeTestFile(t, staleDotLegacyTempPath, "stale")
	t.Setenv(allowSourceOutsideBinDirEnv, "1")

	coord := &Coordinator{
		BinaryPath: binaryPath,
		Source:     sourcePath,
	}
	release, stagedPath, cleanupStaged, err := coord.prepareStagedUpdate(context.Background(), true)
	if err != nil {
		t.Fatalf("prepareStagedUpdate: %v", err)
	}
	if release == nil {
		t.Fatal("expected local-dev release")
	}
	if !cleanupStaged {
		t.Fatal("expected copied staged path to be cleaned on scheduling failure")
	}
	if filepath.Dir(stagedPath) != binaryDir {
		t.Fatalf("staged dir = %q, want %q", filepath.Dir(stagedPath), binaryDir)
	}
	got, err := os.ReadFile(stagedPath)
	if err != nil {
		t.Fatalf("ReadFile stagedPath: %v", err)
	}
	if string(got) != "v2" {
		t.Fatalf("staged payload = %q, want v2", got)
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile sourcePath: %v", err)
	}
	if string(source) != "v2" {
		t.Fatalf("source payload = %q, want v2", source)
	}
	if _, err := os.Stat(staleStagedPath); !os.IsNotExist(err) {
		t.Fatalf("stale staged update should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(staleSafeStagedPath); !os.IsNotExist(err) {
		t.Fatalf("stale safe staged update should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(staleLegacyTempPath); !os.IsNotExist(err) {
		t.Fatalf("stale legacy temp update should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(staleDotLegacyTempPath); !os.IsNotExist(err) {
		t.Fatalf("stale dot-prefixed legacy temp update should be removed, stat err=%v", err)
	}
}

func TestCreatePostExitHelperCopy_UsesDistinctExecutable(t *testing.T) {
	dir := t.TempDir()
	stagedPath := filepath.Join(dir, "aimux-next.exe")
	stagedContent := "replacement-payload"
	writeTestFile(t, stagedPath, stagedContent)

	helperDir := filepath.Join(dir, "helpers")
	t.Setenv(postExitHelperDirEnv, helperDir)

	staleHelper := filepath.Join(helperDir, filepath.Base(stagedPath)+".post-exit-helper.1.2.exe")
	if err := os.MkdirAll(helperDir, 0o700); err != nil {
		t.Fatalf("MkdirAll helperDir: %v", err)
	}
	writeTestFile(t, staleHelper, "stale-helper")

	helperPath, err := createPostExitHelperCopy(stagedPath)
	if err != nil {
		t.Fatalf("createPostExitHelperCopy: %v", err)
	}
	defer func() { _ = os.Remove(helperPath) }()

	if helperPath == stagedPath {
		t.Fatal("helper copy must not be the staged payload itself")
	}
	if filepath.Dir(helperPath) != helperDir {
		t.Fatalf("helper dir = %q, want %q", filepath.Dir(helperPath), helperDir)
	}
	if filepath.Ext(helperPath) != ".exe" {
		t.Fatalf("helper extension = %q, want .exe", filepath.Ext(helperPath))
	}
	if !strings.HasPrefix(filepath.Base(helperPath), postExitHelperPrefix) {
		t.Fatalf("helper path %q does not include post-exit-helper marker", helperPath)
	}

	got, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatalf("ReadFile helper: %v", err)
	}
	if string(got) != stagedContent {
		t.Fatalf("helper content = %q, want staged payload", got)
	}
	if _, err := os.Stat(staleHelper); !os.IsNotExist(err) {
		t.Fatalf("stale helper should be removed, stat err=%v", err)
	}
}

func TestCleanupStalePostExitHelpers_HandlesGlobSpecialChars(t *testing.T) {
	helperDir := t.TempDir()
	stagedPath := filepath.Join(t.TempDir(), "aimux[next].exe")
	staleHelper := filepath.Join(helperDir, filepath.Base(stagedPath)+".post-exit-helper.1.2.exe")
	stalePrefixedHelper := filepath.Join(helperDir, postExitHelperPrefix+"1.2.exe")
	writeTestFile(t, staleHelper, "stale-helper")
	writeTestFile(t, stalePrefixedHelper, "stale-helper")

	cleanupStalePostExitHelpers(helperDir, stagedPath)

	if _, err := os.Stat(staleHelper); !os.IsNotExist(err) {
		t.Fatalf("stale helper should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(stalePrefixedHelper); !os.IsNotExist(err) {
		t.Fatalf("prefixed stale helper should be removed, stat err=%v", err)
	}
}

func TestPostExitGenerationRejectsSupersededStagedPath(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "aimux.exe")
	oldStagedPath := filepath.Join(dir, "aimux-old-staged.exe")
	newStagedPath := filepath.Join(dir, "aimux-new-staged.exe")
	writeTestFile(t, currentPath, "current")
	writeTestFile(t, oldStagedPath, "old-staged")
	writeTestFile(t, newStagedPath, "new-staged")

	oldOpts := PostExitInstallOptions{CurrentExe: currentPath, StagedExe: oldStagedPath}
	newOpts := PostExitInstallOptions{CurrentExe: currentPath, StagedExe: newStagedPath}

	if err := writePostExitGeneration(oldOpts); err != nil {
		t.Fatalf("write old generation: %v", err)
	}
	if err := ensurePostExitGenerationCurrent(oldOpts); err != nil {
		t.Fatalf("old generation should initially be current: %v", err)
	}
	if err := writePostExitGeneration(newOpts); err != nil {
		t.Fatalf("write new generation: %v", err)
	}

	err := ensurePostExitGenerationCurrent(oldOpts)
	if err == nil {
		t.Fatal("expected old generation to be rejected after newer install marker")
	}
	if !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("error = %q, want superseded detail", err)
	}
	if _, statErr := os.Stat(oldStagedPath); !os.IsNotExist(statErr) {
		t.Fatalf("old staged payload should be removed after superseded check, stat err=%v", statErr)
	}
	if err := ensurePostExitGenerationCurrent(newOpts); err != nil {
		t.Fatalf("new generation should remain current: %v", err)
	}
}

func TestRunPostExitInstallRequiresGenerationMarker(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "aimux.exe")
	stagedPath := filepath.Join(dir, "aimux-staged.exe")
	writeTestFile(t, currentPath, "current")
	writeTestFile(t, stagedPath, "staged")

	err := RunPostExitInstall(PostExitInstallOptions{
		CurrentExe:  currentPath,
		StagedExe:   stagedPath,
		DaemonFlag:  defaultPostExitDaemonFlag,
		WaitTimeout: time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected missing generation marker to fail")
	}
	if !strings.Contains(err.Error(), "generation marker") {
		t.Fatalf("error = %q, want generation marker detail", err)
	}
	if got := readTestFile(t, currentPath); got != "current" {
		t.Fatalf("current path = %q, want current", got)
	}
	if got := readTestFile(t, stagedPath); got != "staged" {
		t.Fatalf("staged path = %q, want staged", got)
	}
}

func TestRunPostExitInstallBootstrapsLegacyHelperCopyMissingMarker(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "aimux.exe")
	stagedPath := filepath.Join(dir, "aimux-staged.exe")
	helperPath := filepath.Join(dir, filepath.Base(stagedPath)+".post-exit-helper.123.456.exe")
	writeTestFile(t, currentPath, "current")
	writeTestFile(t, stagedPath, "staged")

	oldExecutablePath := executablePath
	t.Cleanup(func() { executablePath = oldExecutablePath })
	executablePath = func() (string, error) { return helperPath, nil }

	oldMove := moveStagedBinary
	t.Cleanup(func() { moveStagedBinary = oldMove })
	moveCalls := 0
	opts := PostExitInstallOptions{
		CurrentExe:  currentPath,
		StagedExe:   stagedPath,
		DaemonFlag:  defaultPostExitDaemonFlag,
		WaitTimeout: time.Millisecond,
	}
	moveStagedBinary = func(currentPath, stagedPath string) error {
		moveCalls++
		if err := ensurePostExitGenerationCurrent(opts); err != nil {
			t.Fatalf("generation marker should be bootstrapped before move: %v", err)
		}
		return os.ErrInvalid
	}

	err := RunPostExitInstall(opts)
	if err == nil {
		t.Fatal("expected terminal move error")
	}
	if !strings.Contains(err.Error(), os.ErrInvalid.Error()) {
		t.Fatalf("error = %q, want terminal move error", err)
	}
	if moveCalls != 1 {
		t.Fatalf("move calls = %d, want 1", moveCalls)
	}
	if _, statErr := os.Stat(stagedPath); !os.IsNotExist(statErr) {
		t.Fatalf("staged payload should be removed after terminal failure, stat err=%v", statErr)
	}
}

func TestPrepareGenerationForPostExitHelperRelaunchWritesLegacyMissingMarker(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "aimux.exe")
	stagedPath := filepath.Join(dir, "aimux-staged.exe")
	writeTestFile(t, currentPath, "current")
	writeTestFile(t, stagedPath, "staged")

	opts := PostExitInstallOptions{
		CurrentExe:  currentPath,
		StagedExe:   stagedPath,
		DaemonFlag:  defaultPostExitDaemonFlag,
		WaitTimeout: time.Millisecond,
	}
	if err := prepareGenerationForPostExitHelperRelaunch(opts); err != nil {
		t.Fatalf("prepareGenerationForPostExitHelperRelaunch: %v", err)
	}
	if err := ensurePostExitGenerationCurrent(opts); err != nil {
		t.Fatalf("generation marker should point to staged payload: %v", err)
	}
	if got := readTestFile(t, currentPath); got != "current" {
		t.Fatalf("current path = %q, want current", got)
	}
	if got := readTestFile(t, stagedPath); got != "staged" {
		t.Fatalf("staged path = %q, want staged", got)
	}
}

func TestRunPostExitInstallRechecksGenerationBeforeEachMoveAttempt(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "aimux.exe")
	oldStagedPath := filepath.Join(dir, "aimux-old-staged.exe")
	newStagedPath := filepath.Join(dir, "aimux-new-staged.exe")
	writeTestFile(t, currentPath, "current")
	writeTestFile(t, oldStagedPath, "old-staged")
	writeTestFile(t, newStagedPath, "new-staged")

	oldOpts := PostExitInstallOptions{
		CurrentExe:  currentPath,
		StagedExe:   oldStagedPath,
		DaemonFlag:  defaultPostExitDaemonFlag,
		WaitTimeout: 100 * time.Millisecond,
	}
	newOpts := PostExitInstallOptions{CurrentExe: currentPath, StagedExe: newStagedPath}
	if err := writePostExitGeneration(oldOpts); err != nil {
		t.Fatalf("write old generation: %v", err)
	}

	oldMove := moveStagedBinary
	t.Cleanup(func() { moveStagedBinary = oldMove })
	moveCalls := 0
	moveStagedBinary = func(currentPath, stagedPath string) error {
		moveCalls++
		if moveCalls == 1 {
			if err := writePostExitGeneration(newOpts); err != nil {
				t.Fatalf("write new generation from move hook: %v", err)
			}
			return &ErrCurrentBinaryLocked{BinaryPath: currentPath, Cause: os.ErrPermission}
		}
		t.Fatalf("move called after generation was superseded")
		return nil
	}

	err := RunPostExitInstall(oldOpts)
	if err == nil {
		t.Fatal("expected superseded generation error")
	}
	if !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("error = %q, want superseded detail", err)
	}
	if moveCalls != 1 {
		t.Fatalf("move calls = %d, want 1", moveCalls)
	}
	if _, statErr := os.Stat(oldStagedPath); !os.IsNotExist(statErr) {
		t.Fatalf("old staged payload should be removed after superseded retry, stat err=%v", statErr)
	}
	if got := readTestFile(t, currentPath); got != "current" {
		t.Fatalf("current path = %q, want current", got)
	}
	if err := ensurePostExitGenerationCurrent(newOpts); err != nil {
		t.Fatalf("new generation should remain current: %v", err)
	}
}

func TestRunPostExitInstallRetriesStagedBinaryLock(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "aimux.exe")
	stagedPath := filepath.Join(dir, "aimux-staged.exe")
	writeTestFile(t, currentPath, "current")
	writeTestFile(t, stagedPath, "staged")

	opts := PostExitInstallOptions{
		CurrentExe:  currentPath,
		StagedExe:   stagedPath,
		DaemonFlag:  defaultPostExitDaemonFlag,
		WaitTimeout: time.Second,
	}
	if err := writePostExitGeneration(opts); err != nil {
		t.Fatalf("write generation marker: %v", err)
	}

	oldMove := moveStagedBinary
	t.Cleanup(func() { moveStagedBinary = oldMove })
	moveCalls := 0
	moveStagedBinary = func(currentPath, stagedPath string) error {
		moveCalls++
		if moveCalls == 1 {
			return &ErrStagedBinaryLocked{StagedPath: stagedPath, Cause: os.ErrPermission}
		}
		return os.ErrInvalid
	}

	err := RunPostExitInstall(opts)
	if err == nil {
		t.Fatal("expected terminal move error")
	}
	if !strings.Contains(err.Error(), os.ErrInvalid.Error()) {
		t.Fatalf("error = %q, want terminal move error", err)
	}
	if moveCalls != 2 {
		t.Fatalf("move calls = %d, want staged-lock retry then terminal failure", moveCalls)
	}
	if _, statErr := os.Stat(stagedPath); !os.IsNotExist(statErr) {
		t.Fatalf("staged payload should be removed after terminal failure, stat err=%v", statErr)
	}
}

func TestSameExecutablePath_NormalizesEquivalentPaths(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aimux-next.exe")
	writeTestFile(t, exePath, "payload")

	equivalent := filepath.Join(dir, ".", "aimux-next.exe")
	if !sameExecutablePath(exePath, equivalent) {
		t.Fatalf("sameExecutablePath(%q, %q) = false, want true", exePath, equivalent)
	}
	if sameExecutablePath(exePath, filepath.Join(dir, "other.exe")) {
		t.Fatal("sameExecutablePath returned true for different paths")
	}
}

func TestSameExecutablePath_RespectsUnixCaseSensitivity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows paths are case-insensitive")
	}
	dir := t.TempDir()
	upper := filepath.Join(dir, "Aimux.exe")
	lower := filepath.Join(dir, "aimux.exe")
	writeTestFile(t, upper, "upper")
	writeTestFile(t, lower, "lower")

	if sameExecutablePath(upper, lower) {
		t.Fatalf("sameExecutablePath(%q, %q) = true, want false on case-sensitive platforms", upper, lower)
	}
}

func TestMoveStagedBinaryIntoPlace_ConsumesStagedPayload(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "aimux.exe")
	stagedPath := filepath.Join(dir, "aimux-next.exe")

	writeTestFile(t, currentPath, "current-v1")
	writeTestFile(t, stagedPath, "staged-v2")

	if err := moveStagedBinaryIntoPlace(currentPath, stagedPath); err != nil {
		t.Fatalf("moveStagedBinaryIntoPlace: %v", err)
	}

	got, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("ReadFile currentPath: %v", err)
	}
	if string(got) != "staged-v2" {
		t.Fatalf("currentPath = %q, want staged-v2", got)
	}

	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatalf("staged payload should be consumed by move, stat err=%v", err)
	}

	old, err := os.ReadFile(currentPath + ".old")
	if err != nil {
		t.Fatalf("ReadFile old rollback slot: %v", err)
	}
	if string(old) != "current-v1" {
		t.Fatalf("old rollback slot = %q, want current-v1", old)
	}
}

type testSessionHandler struct {
	pendingCalled bool
}

func (h *testSessionHandler) SetUpdatePending() {
	h.pendingCalled = true
}

type testLaunchSessionHandler struct {
	pendingCalled bool
	launcher      func() error
	stopScheduled bool
	stopDelay     time.Duration
}

func (h *testLaunchSessionHandler) SetUpdatePending() {
	h.pendingCalled = true
}

func (h *testLaunchSessionHandler) SetUpdatePendingLauncher(launcher func() error) {
	h.launcher = launcher
}

func (h *testLaunchSessionHandler) StopForUpdatePendingRestartAfter(delay time.Duration) {
	h.stopScheduled = true
	h.stopDelay = delay
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return string(data)
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
