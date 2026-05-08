package code

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
)

const testDriverDiff = "diff --git a/a.txt b/a.txt\n+hello\n"

func TestRunRoundVerdictShapes(t *testing.T) {
	tests := []struct {
		name            string
		navigatorOutput string
		wantAction      State
		wantDiff        string
	}{
		{
			name:            "apply",
			navigatorOutput: `{"verdict":"APPLY","confidence":0.91,"evidence":"criteria pass"}`,
			wantAction:      StateApply,
			wantDiff:        strings.TrimSpace(testDriverDiff),
		},
		{
			name:            "revise",
			navigatorOutput: `{"verdict":"REVISE","confidence":0.88,"diff":"diff --git a/a.txt b/a.txt\n+revised","evidence":"navigator revised"}`,
			wantAction:      StateRevise,
			wantDiff:        "diff --git a/a.txt b/a.txt\n+revised",
		},
		{
			name:            "retry",
			navigatorOutput: `{"verdict":"RETRY","confidence":0.42,"feedback":"needs smaller diff","evidence":"typecheck risk"}`,
			wantAction:      StateRetry,
		},
		{
			name:            "escalate",
			navigatorOutput: `{"verdict":"ESCALATE","confidence":0.10,"feedback":"spec contradiction","evidence":"cannot satisfy both ACs"}`,
			wantAction:      StateEscalate,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockLoom(tc.navigatorOutput)
			verdict, err := RunRound(context.Background(), "make the change", DefaultSuccessCriteria(false), testPairConfig(mock))
			if err != nil {
				t.Fatalf("RunRound returned error: %v", err)
			}
			if verdict.Action != tc.wantAction {
				t.Fatalf("action = %s, want %s", verdict.Action, tc.wantAction)
			}
			if verdict.Diff != tc.wantDiff {
				t.Fatalf("diff = %q, want %q", verdict.Diff, tc.wantDiff)
			}
			if verdict.DriverTaskID == "" || verdict.NavigatorTaskID == "" {
				t.Fatalf("task IDs missing: %#v", verdict)
			}
		})
	}
}

func TestRunRoundSubtaskMetadataAndNavigatorPrompt(t *testing.T) {
	mock := newMockLoom(`{"verdict":"APPLY","confidence":0.91,"evidence":"ok"}`)

	_, err := RunRound(context.Background(), "change README", DefaultSuccessCriteria(false), testPairConfig(mock))
	if err != nil {
		t.Fatalf("RunRound returned error: %v", err)
	}
	if len(mock.submissions) != 2 {
		t.Fatalf("submission count = %d, want 2", len(mock.submissions))
	}

	driver := mock.submissions[0]
	if driver.ParentTaskID != "parent-1" {
		t.Fatalf("driver ParentTaskID = %q, want parent-1", driver.ParentTaskID)
	}
	if driver.WorkerType != WorkerTypeCodeDriver {
		t.Fatalf("driver WorkerType = %s, want %s", driver.WorkerType, WorkerTypeCodeDriver)
	}
	if driver.CLI != "codex" {
		t.Fatalf("driver CLI = %q, want codex", driver.CLI)
	}
	assertMetadata(t, driver.Metadata, "driver_cli", "codex")
	assertMetadata(t, driver.Metadata, "navigator_cli", "claude")
	assertMetadata(t, driver.Metadata, "worker_type", string(WorkerTypeCodeDriver))

	navigator := mock.submissions[1]
	if navigator.ParentTaskID != "parent-1" {
		t.Fatalf("navigator ParentTaskID = %q, want parent-1", navigator.ParentTaskID)
	}
	if navigator.WorkerType != WorkerTypeCodeNavigator {
		t.Fatalf("navigator WorkerType = %s, want %s", navigator.WorkerType, WorkerTypeCodeNavigator)
	}
	if navigator.CLI != "claude" {
		t.Fatalf("navigator CLI = %q, want claude", navigator.CLI)
	}
	assertMetadata(t, navigator.Metadata, "navigator_cli", "claude")
	assertMetadata(t, navigator.Metadata, "driver_cli", "codex")
	normalizedNavigatorPrompt := strings.ReplaceAll(navigator.Prompt, "\r\n", "\n")
	if !strings.Contains(normalizedNavigatorPrompt, strings.TrimSpace(testDriverDiff)) {
		t.Fatalf("navigator prompt missing driver diff: %q", navigator.Prompt)
	}
	if !strings.Contains(navigator.Prompt, "BuildClean") {
		t.Fatalf("navigator prompt missing criteria summary: %q", navigator.Prompt)
	}
}

func TestRunRoundNavigatorRejectReturnsRetry(t *testing.T) {
	mock := newMockLoom(`{"verdict":"RETRY","confidence":0.20,"feedback":"reject: missing tests","evidence":"tests absent"}`)

	verdict, err := RunRound(context.Background(), "make the change", DefaultSuccessCriteria(false), testPairConfig(mock))
	if err != nil {
		t.Fatalf("RunRound returned error: %v", err)
	}
	if verdict.Action != StateRetry {
		t.Fatalf("navigator reject action = %s, want %s", verdict.Action, StateRetry)
	}
	if verdict.Feedback != "reject: missing tests" {
		t.Fatalf("feedback = %q, want navigator rejection feedback", verdict.Feedback)
	}
}

type mockLoom struct {
	navigatorOutput string
	submissions     []loom.TaskRequest
	tasks           map[string]*loom.Task
}

func newMockLoom(navigatorOutput string) *mockLoom {
	return &mockLoom{
		navigatorOutput: navigatorOutput,
		tasks:           map[string]*loom.Task{},
	}
}

func (m *mockLoom) Submit(_ context.Context, req loom.TaskRequest) (string, error) {
	id := fmt.Sprintf("task-%d", len(m.submissions)+1)
	m.submissions = append(m.submissions, req)

	result := testDriverDiff
	if req.WorkerType == WorkerTypeCodeNavigator {
		result = m.navigatorOutput
	}
	m.tasks[id] = &loom.Task{
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

func (m *mockLoom) Get(taskID string) (*loom.Task, error) {
	task, ok := m.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	return task, nil
}

func testPairConfig(client LoomClient) PairConfig {
	return PairConfig{
		Loom:         client,
		ParentTaskID: "parent-1",
		ProjectID:    "project-1",
		RequestID:    "request-1",
		CWD:          "/workspace",
		DriverCLI:    "codex",
		NavigatorCLI: "claude",
		TaskTimeout:  time.Second,
		PollInterval: time.Millisecond,
	}
}

func assertMetadata(t *testing.T, metadata map[string]any, key string, want any) {
	t.Helper()
	if metadata[key] != want {
		t.Fatalf("metadata[%s] = %#v, want %#v", key, metadata[key], want)
	}
}
