package server

import (
	"context"
	"strings"
	"testing"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/workflow"
)

// TestWorkflowRecipeWorkerRoutesProductionOutputThroughBoundedEventPathAndRefreshesProgress
// covers an accepted T018 CR-002 review finding: production workflow-backed
// recipe dispatch must route native CLI output through the bounded
// EventWriter/EventSink plumbing and refresh task progress, so long-running
// workflow steps do not look stalled (ProgressUpdatedAt must advance, and
// runtime output artifacts must persist) the same way profileTaskWorker's
// production dispatch already does.
//
// Today workflowRecipeExecutorSender.Send only wires SpawnArgs.OnOutput,
// which the native EventExecutor SendEvents path never invokes — so neither
// progress nor runtime artifacts are ever recorded for a production
// workflow-backed recipe task, even though the underlying CLI call
// completes and produces real output.
func TestWorkflowRecipeWorkerRoutesProductionOutputThroughBoundedEventPathAndRefreshesProgress(t *testing.T) {
	const workflowID = "t018-cr002-finality-probe"
	workflow.Registry[workflowID] = func() []workflow.WorkflowStep {
		return []workflow.WorkflowStep{
			{
				Name:   "probe",
				Action: workflow.ActionSingleExec,
				Config: map[string]any{
					"cli":    "codex",
					"prompt": "Run the finality probe:\n\n%s",
				},
			},
		}
	}
	t.Cleanup(func() { delete(workflow.Registry, workflowID) })

	dir := t.TempDir()
	codexPath := fakeExecutableWithContents(t, dir, "codex-progress",
		"#!/bin/sh\nprintf 'workflow step output\\n'\n",
		"@echo off\r\necho workflow step output\r\nexit /b 0\r\n",
	)
	codex := defaultRecipeProfile()
	codex.Binary = codexPath
	codex.ResolvedPath = codexPath
	codex.OutputFormat = "text"
	codex.TimeoutSeconds = 5
	registry := driver.NewRegistry(map[string]*config.CLIProfile{"codex": codex})
	registry.SetAvailable("codex", true)

	engine := newTaskToolEngine(t)
	srv := &Server{loom: engine, cfg: &config.Config{}, registry: registry}
	engine.RegisterWorker(workflowRecipeWorkerType, workflowRecipeWorker{server: srv, defaultCLI: "codex"})

	taskID, err := engine.Submit(context.Background(), loom.TaskRequest{
		WorkerType: workflowRecipeWorkerType,
		ProjectID:  "t018-finality",
		Prompt:     "probe production workflow dispatch",
		Metadata: map[string]any{
			"recipe_workflow_id": workflowID,
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForTaskTerminal(t, engine, taskID)
	task, err := engine.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Status != loom.TaskStatusCompleted {
		t.Fatalf("task status = %v, want completed; error=%q", task.Status, task.Error)
	}
	if task.ProgressUpdatedAt == nil {
		t.Fatal("task.ProgressUpdatedAt = nil, want a refreshed progress timestamp from the native production dispatch path")
	}
	if strings.TrimSpace(task.LastOutputLine) == "" {
		t.Fatal("task.LastOutputLine is empty, want the workflow step's stdout to have been recorded as progress")
	}

	page, err := engine.ListArtifacts(taskID, loom.TaskArtifactListOptions{})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("no runtime artifacts persisted for the workflow task; want output routed through the bounded EventWriter/EventSink path")
	}
}
