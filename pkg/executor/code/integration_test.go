package code

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	applygate "github.com/thebtf/aimux/pkg/executor/code/gate"
	"github.com/thebtf/aimux/pkg/executor/types"
)

func TestCodeWorkerIntegrationApplyGateDone(t *testing.T) {
	root := copyCodeTestProject(t, "go-ok")
	client := newIntegrationLoom(
		[]string{messageDiff("new")},
		[]string{navigatorJSON(StateApply, 0.93, "", "criteria pass")},
	)
	worker := newIntegrationCodeWorker(t, client, 3)
	task := integrationCodeTask(root)

	result, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}
	assertFile(t, root, "message.go", "package sample\n\nconst Message = \"new\"\n")
	assertTaskMetadata(t, task.Metadata, "gate_result", "passed")
	assertTaskMetadata(t, task.Metadata, "verdict", string(StateApply))
	assertTransitionLogContains(t, task.Metadata, StatePrep, StateDriver)
	assertTransitionLogContains(t, task.Metadata, StateDriver, StateNavigator)
	assertTransitionLogContains(t, task.Metadata, StateNavigator, StateApply)
	assertTransitionLogContains(t, task.Metadata, StateApply, StateGate)
	assertTransitionLogContains(t, task.Metadata, StateGate, StateDone)
}

func TestCodeWorkerIntegrationEscalatePath(t *testing.T) {
	root := copyCodeTestProject(t, "go-ok")
	client := newIntegrationLoom(
		[]string{messageDiff("new")},
		[]string{navigatorJSON(StateEscalate, 0.10, "spec contradiction", "spec contradiction")},
	)
	worker := newIntegrationCodeWorker(t, client, 3)
	task := integrationCodeTask(root)

	_, err := worker.Execute(context.Background(), task)
	assertCLIErrorCode(t, err, types.CLIErrorCodeCapabilityMismatch)
	assertTransitionLogContains(t, task.Metadata, StateNavigator, StateEscalate)
	if content := readFixtureFile(t, root, "message.go"); strings.Contains(content, `"new"`) {
		t.Fatalf("escalate path modified file unexpectedly:\n%s", content)
	}
}

func TestCodeWorkerIntegrationRetryThenApply(t *testing.T) {
	root := copyCodeTestProject(t, "go-ok")
	client := newIntegrationLoom(
		[]string{messageDiff("new"), messageDiff("new")},
		[]string{
			navigatorJSON(StateRetry, 0.40, "add acceptance evidence", "missing evidence"),
			navigatorJSON(StateApply, 0.91, "", "criteria pass after retry"),
		},
	)
	worker := newIntegrationCodeWorker(t, client, 3)
	task := integrationCodeTask(root)

	if _, err := worker.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if client.driverCalls != 2 || client.navigatorCalls != 2 {
		t.Fatalf("round calls driver=%d navigator=%d, want 2/2", client.driverCalls, client.navigatorCalls)
	}
	assertTaskMetadata(t, task.Metadata, "rounds", 1)
	assertFile(t, root, "message.go", "package sample\n\nconst Message = \"new\"\n")
	assertTransitionLogContains(t, task.Metadata, StateNavigator, StateRetry)
	assertTransitionLogContains(t, task.Metadata, StateRetry, StateDriver)
	assertTransitionLogContains(t, task.Metadata, StateGate, StateDone)
}

func TestCodeWorkerIntegrationResumeContinuation(t *testing.T) {
	root := copyCodeTestProject(t, "go-ok")
	client := newIntegrationLoom(nil, nil)
	client.tasks["prior-code-task"] = &loom.Task{
		ID:         "prior-code-task",
		WorkerType: WorkerTypeCode,
		Metadata: map[string]any{
			MetadataThreadID:   "thread-prior",
			MetadataWorkerType: string(WorkerTypeCode),
		},
	}
	worker := newIntegrationCodeWorker(t, client, 3)

	meta, err := worker.ResumeFromTask(context.Background(), "prior-code-task")
	if err != nil {
		t.Fatalf("ResumeFromTask returned error: %v", err)
	}
	resumedTask := integrationCodeTask(root)
	for key, value := range meta {
		resumedTask.Metadata[key] = value
	}
	if resumedTask.Metadata[MetadataThreadID] != "thread-prior" {
		t.Fatalf("resumed metadata = %#v, want thread-prior", resumedTask.Metadata)
	}
	if resumedTask.Metadata[MetadataWorkerType] != string(WorkerTypeCode) {
		t.Fatalf("resumed metadata = %#v, want worker_type=code", resumedTask.Metadata)
	}
}

type integrationLoom struct {
	driverOutputs    []string
	navigatorOutputs []string
	driverCalls      int
	navigatorCalls   int
	submissions      []loom.TaskRequest
	tasks            map[string]*loom.Task
}

func newIntegrationLoom(driverOutputs, navigatorOutputs []string) *integrationLoom {
	return &integrationLoom{
		driverOutputs:    driverOutputs,
		navigatorOutputs: navigatorOutputs,
		tasks:            map[string]*loom.Task{},
	}
}

func (l *integrationLoom) Submit(_ context.Context, req loom.TaskRequest) (string, error) {
	id := fmt.Sprintf("integration-task-%d", len(l.submissions)+1)
	l.submissions = append(l.submissions, req)

	result := ""
	switch req.WorkerType {
	case WorkerTypeCodeDriver:
		result = sequenceValue(l.driverOutputs, l.driverCalls)
		l.driverCalls++
	case WorkerTypeCodeNavigator:
		result = sequenceValue(l.navigatorOutputs, l.navigatorCalls)
		l.navigatorCalls++
	default:
		return "", types.NewCapabilityMismatch(fmt.Sprintf("unexpected worker type %s", req.WorkerType), nil)
	}
	l.tasks[id] = &loom.Task{
		ID:           id,
		Status:       loom.TaskStatusCompleted,
		WorkerType:   req.WorkerType,
		ProjectID:    req.ProjectID,
		ParentTaskID: req.ParentTaskID,
		CLI:          req.CLI,
		Metadata:     req.Metadata,
		Result:       result,
	}
	return id, nil
}

func (l *integrationLoom) Get(taskID string) (*loom.Task, error) {
	task, ok := l.tasks[taskID]
	if !ok {
		return nil, types.NewUserInputError("task not found: "+taskID, nil)
	}
	return task, nil
}

func sequenceValue(values []string, index int) string {
	if len(values) == 0 {
		return ""
	}
	if index >= len(values) {
		return values[len(values)-1]
	}
	return values[index]
}

func newIntegrationCodeWorker(t *testing.T, client LoomClient, maxRounds int) *CodeWorker {
	t.Helper()
	worker, err := NewCodeWorker(CodeWorkerConfig{
		Loom:          client,
		DriverCLI:     "codex",
		NavigatorCLI:  "claude",
		MaxRounds:     maxRounds,
		PairRunner:    nil,
		GateRunner:    nil,
		Apply:         nil,
		DriverResumer: nil,
	})
	if err != nil {
		t.Fatalf("NewCodeWorker returned error: %v", err)
	}
	return worker
}

func integrationCodeTask(root string) *loom.Task {
	return &loom.Task{
		ID:         "root-code-task",
		Status:     loom.TaskStatusRunning,
		WorkerType: WorkerTypeCode,
		ProjectID:  "project-integration",
		RequestID:  "request-integration",
		Prompt:     "change Message from old to new",
		CWD:        root,
		Metadata:   map[string]any{},
	}
}

func messageDiff(next string) string {
	return fmt.Sprintf(
		"--- a/message.go\n"+
			"+++ b/message.go\n"+
			"@@ -1,3 +1,3 @@\n"+
			" package sample\n"+
			" \n"+
			"-const Message = \"old\"\n"+
			"+const Message = %q\n",
		next,
	)
}

func navigatorJSON(action State, confidence float64, feedback string, evidence string) string {
	return fmt.Sprintf(`{"verdict":%q,"confidence":%.2f,"feedback":%q,"evidence":%q}`, action, confidence, feedback, evidence)
}

func copyCodeTestProject(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", name)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
	if err != nil {
		t.Fatalf("copy test project %s: %v", name, err)
	}
	return dst
}

func readFixtureFile(t *testing.T, root string, rel string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(content)
}

func TestIntegrationFixtureHasGoProject(t *testing.T) {
	root := copyCodeTestProject(t, "go-ok")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("fixture missing go.mod: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "message_test.go")); err != nil {
		t.Fatalf("fixture missing message_test.go: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := newIntegrationCodeWorker(t, newIntegrationLoom(
		[]string{messageDiff("new")},
		[]string{navigatorJSON(StateApply, 0.91, "", "ok")},
	), 3).gateRunner.Run(ctx, applygateProject(root))
	if result.Status != "passed" {
		t.Fatalf("fixture gate status = %s reason=%s phases=%#v", result.Status, result.Reason, result.Phases)
	}
}

func applygateProject(root string) applygate.Project {
	return applygate.Project{CWD: root, PhaseTimeout: 30 * time.Second}
}
