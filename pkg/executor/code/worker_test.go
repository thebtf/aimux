package code

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thebtf/aimux/loom"
	applygate "github.com/thebtf/aimux/pkg/executor/code/gate"
	"github.com/thebtf/aimux/pkg/executor/types"
)

func TestCodeWorkerApplyPathRecordsMetadataAndTransitions(t *testing.T) {
	root := codeWorkerFixture(t)
	worker := newTestCodeWorker(t, workerTestDeps{
		pair: &mockWorkerPair{verdicts: []Verdict{{
			Action:     StateApply,
			Confidence: 0.91,
			Diff:       renameDiff("note.txt", "old", "new"),
			Evidence:   "criteria pass",
		}}},
		gate: &mockWorkerGate{result: applygate.Result{Status: applygate.StatusPassed}},
	})
	task := codeWorkerTask(root)

	result, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}
	assertFile(t, root, "note.txt", "new\n")
	assertTaskMetadata(t, task.Metadata, "driver_cli", "codex")
	assertTaskMetadata(t, task.Metadata, "navigator_cli", "claude")
	assertTaskMetadata(t, task.Metadata, "rounds", 1)
	assertTaskMetadata(t, task.Metadata, "confidence_score", 0.91)
	assertTaskMetadata(t, task.Metadata, "gate_result", "passed")
	assertTransitionLogContains(t, task.Metadata, StatePrep, StateDriver)
	assertTransitionLogContains(t, task.Metadata, StateGate, StateDone)
}

func TestCodeWorkerRecordsDriverThreadIDForResume(t *testing.T) {
	root := codeWorkerFixture(t)
	worker := newTestCodeWorker(t, workerTestDeps{
		pair: &mockWorkerPair{verdicts: []Verdict{{
			Action:     StateApply,
			Confidence: 0.91,
			Diff:       renameDiff("note.txt", "old", "new"),
			ThreadID:   "thread-from-driver",
		}}},
		gate: &mockWorkerGate{result: applygate.Result{Status: applygate.StatusPassed}},
	})
	task := codeWorkerTask(root)

	_, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	assertTaskMetadata(t, task.Metadata, MetadataThreadID, "thread-from-driver")
}

func TestCodeWorkerFailTaskHandlesNilError(t *testing.T) {
	worker := newTestCodeWorker(t, workerTestDeps{})
	task := &loom.Task{Metadata: map[string]any{}}

	_, err := worker.failTask(task, nil, nil)
	if err == nil {
		t.Fatal("failTask nil error returned nil, want CLIError")
	}
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != types.CLIErrorCodeUnknown {
		t.Fatalf("code = %s, want %s", cliErr.Code, types.CLIErrorCodeUnknown)
	}
	if task.Error == "" {
		t.Fatal("task.Error is empty, want recorded failure")
	}
}

func TestCodeWorkerRetryLoopIncrementsRounds(t *testing.T) {
	root := codeWorkerFixture(t)
	pair := &mockWorkerPair{verdicts: []Verdict{
		{Action: StateRetry, Confidence: 0.40, Feedback: "missing acceptance test"},
		{Action: StateApply, Confidence: 0.90, Diff: renameDiff("note.txt", "old", "new")},
	}}
	worker := newTestCodeWorker(t, workerTestDeps{
		pair: pair,
		gate: &mockWorkerGate{result: applygate.Result{Status: applygate.StatusSkipped}},
	})
	task := codeWorkerTask(root)

	_, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if pair.calls != 2 {
		t.Fatalf("pair calls = %d, want 2", pair.calls)
	}
	assertTaskMetadata(t, task.Metadata, "rounds", 2)
	assertTaskMetadata(t, task.Metadata, "gate_result", "skipped")
	assertTransitionLogContains(t, task.Metadata, StateRetry, StateDriver)
}

func TestCodeWorkerHonorsDriverCLIOverride(t *testing.T) {
	root := codeWorkerFixture(t)
	pair := &mockWorkerPair{verdicts: []Verdict{{
		Action:     StateApply,
		Confidence: 0.91,
		Diff:       renameDiff("note.txt", "old", "new"),
	}}}
	worker := newTestCodeWorker(t, workerTestDeps{
		pair: pair,
		gate: &mockWorkerGate{result: applygate.Result{Status: applygate.StatusPassed}},
	})
	task := codeWorkerTask(root)
	task.CLI = "gemini"

	_, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(pair.configs) != 1 {
		t.Fatalf("pair configs = %d, want 1", len(pair.configs))
	}
	if pair.configs[0].DriverCLI != "gemini" {
		t.Fatalf("DriverCLI = %q, want gemini", pair.configs[0].DriverCLI)
	}
	if pair.configs[0].NavigatorCLI != "claude" {
		t.Fatalf("NavigatorCLI = %q, want claude", pair.configs[0].NavigatorCLI)
	}
	assertTaskMetadata(t, task.Metadata, "driver_cli", "gemini")
}

func TestCodeWorkerEscalateReturnsTypedCLIError(t *testing.T) {
	root := codeWorkerFixture(t)
	worker := newTestCodeWorker(t, workerTestDeps{
		pair: &mockWorkerPair{verdicts: []Verdict{{
			Action:     StateEscalate,
			Confidence: 0.10,
			Evidence:   "spec contradiction",
		}}},
		gate: &mockWorkerGate{result: applygate.Result{Status: applygate.StatusPassed}},
	})
	task := codeWorkerTask(root)

	_, err := worker.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("Execute returned nil, want CLIError")
	}
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != types.CLIErrorCodeCapabilityMismatch {
		t.Fatalf("CLIError code = %s, want %s", cliErr.Code, types.CLIErrorCodeCapabilityMismatch)
	}
	if task.Error == "" {
		t.Fatal("task.Error is empty")
	}
	assertTransitionLogContains(t, task.Metadata, StateNavigator, StateEscalate)
}

func TestCodeWorkerApplyPathEscapePreservesSandboxDenial(t *testing.T) {
	root := codeWorkerFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	diff := strings.ReplaceAll(`--- a/file.txt
+++ ABSOLUTE
@@ -0,0 +1 @@
+owned
`, "ABSOLUTE", filepath.ToSlash(outside))
	gate := &mockWorkerGate{result: applygate.Result{Status: applygate.StatusPassed}}
	worker := newTestCodeWorker(t, workerTestDeps{
		pair: &mockWorkerPair{verdicts: []Verdict{{
			Action:     StateApply,
			Confidence: 0.91,
			Diff:       diff,
		}}},
		gate: gate,
	})
	task := codeWorkerTask(root)

	_, err := worker.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("Execute returned nil, want SandboxDenial")
	}
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != types.CLIErrorCodeSandboxDenial {
		t.Fatalf("CLIError code = %s, want %s", cliErr.Code, types.CLIErrorCodeSandboxDenial)
	}
	if gate.calls != 0 {
		t.Fatalf("gate calls = %d, want 0", gate.calls)
	}
	assertFile(t, root, "note.txt", "old\n")
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Fatalf("outside path exists after rejected diff: stat err=%v", statErr)
	}
}

func TestCodeWorkerReadOnlySandboxRejectsApplyBeforeWrite(t *testing.T) {
	root := codeWorkerFixture(t)
	gate := &mockWorkerGate{result: applygate.Result{Status: applygate.StatusPassed}}
	worker := newTestCodeWorker(t, workerTestDeps{
		pair: &mockWorkerPair{verdicts: []Verdict{{
			Action:     StateApply,
			Confidence: 0.91,
			Diff:       renameDiff("note.txt", "old", "new"),
		}}},
		gate: gate,
	})
	task := codeWorkerTask(root)
	task.Metadata["sandbox"] = "read-only"

	_, err := worker.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("Execute returned nil, want SandboxDenial")
	}
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != types.CLIErrorCodeSandboxDenial {
		t.Fatalf("CLIError code = %s, want %s", cliErr.Code, types.CLIErrorCodeSandboxDenial)
	}
	if gate.calls != 0 {
		t.Fatalf("gate calls = %d, want 0", gate.calls)
	}
	assertFile(t, root, "note.txt", "old\n")
}

type workerTestDeps struct {
	pair    PairRoundRunner
	gate    GateRunner
	resumer ResumeDelegate
	loom    LoomClient
}

func newTestCodeWorker(t *testing.T, deps workerTestDeps) *CodeWorker {
	t.Helper()
	if deps.pair == nil {
		deps.pair = &mockWorkerPair{verdicts: []Verdict{{Action: StateApply, Confidence: 1, Diff: renameDiff("note.txt", "old", "new")}}}
	}
	if deps.gate == nil {
		deps.gate = &mockWorkerGate{result: applygate.Result{Status: applygate.StatusPassed}}
	}
	loomClient := deps.loom
	if loomClient == nil {
		loomClient = newMockLoom(`{"verdict":"APPLY","confidence":1}`)
	}
	worker, err := NewCodeWorker(CodeWorkerConfig{
		Loom:          loomClient,
		PairRunner:    deps.pair,
		GateRunner:    deps.gate,
		DriverResumer: deps.resumer,
		DriverCLI:     "codex",
		NavigatorCLI:  "claude",
		MaxRounds:     3,
	})
	if err != nil {
		t.Fatalf("NewCodeWorker returned error: %v", err)
	}
	return worker
}

func codeWorkerFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return root
}

func codeWorkerTask(root string) *loom.Task {
	return &loom.Task{
		ID:         "root-task",
		Status:     loom.TaskStatusRunning,
		WorkerType: WorkerTypeCode,
		ProjectID:  "project-1",
		RequestID:  "request-1",
		Prompt:     "rename old to new",
		CWD:        root,
		Metadata:   map[string]any{},
	}
}

func renameDiff(path string, old string, next string) string {
	return "--- a/" + path + "\n+++ b/" + path + "\n@@ -1 +1 @@\n-" + old + "\n+" + next + "\n"
}

func assertFile(t *testing.T, root string, rel string, want string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if string(content) != want {
		t.Fatalf("%s content = %q, want %q", rel, string(content), want)
	}
}

func assertTaskMetadata(t *testing.T, metadata map[string]any, key string, want any) {
	t.Helper()
	if metadata[key] != want {
		t.Fatalf("metadata[%s] = %#v, want %#v", key, metadata[key], want)
	}
}

func assertTransitionLogContains(t *testing.T, metadata map[string]any, from State, to State) {
	t.Helper()
	raw := metadata[MetadataTransitionsKey]
	log, ok := raw.([]any)
	if !ok {
		t.Fatalf("transition log type = %T, want []any", raw)
	}
	for _, rawEntry := range log {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		if entry["from"] == string(from) && entry["to"] == string(to) {
			return
		}
	}
	t.Fatalf("transition log missing %s -> %s: %#v", from, to, log)
}

type mockWorkerPair struct {
	verdicts []Verdict
	calls    int
	configs  []PairConfig
}

func (m *mockWorkerPair) RunRound(_ context.Context, prompt string, _ SuccessCriteria, cfg PairConfig) (Verdict, error) {
	if m.calls >= len(m.verdicts) {
		return Verdict{}, errors.New("unexpected pair call")
	}
	verdict := m.verdicts[m.calls]
	m.configs = append(m.configs, cfg)
	m.calls++
	if verdict.Action == StateRetry && !strings.Contains(prompt, "Navigator feedback") && m.calls > 1 {
		return Verdict{}, errors.New("retry prompt missing feedback")
	}
	return verdict, nil
}

type mockWorkerGate struct {
	result applygate.Result
	calls  int
}

func (m *mockWorkerGate) Run(_ context.Context, _ applygate.Project) applygate.Result {
	m.calls++
	return m.result
}

type mockResumeDelegate struct {
	prevTaskID string
	meta       map[string]any
}

func (m *mockResumeDelegate) ResumeFromTask(_ context.Context, prevTaskID string) (map[string]any, error) {
	m.prevTaskID = prevTaskID
	return m.meta, nil
}
