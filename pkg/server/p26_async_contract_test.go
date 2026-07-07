package server

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor/code"
)

type p26AsyncToolSpec struct {
	name       string
	workerType loom.WorkerType
	launch     func(context.Context, *Server) (*mcp.CallToolResult, error)
}

func TestP26AsyncContractTemplate_TaskCancelProgressCleanup(t *testing.T) {
	t.Parallel()

	p26RunAsyncContractTemplate(t, p26AsyncToolSpec{
		name:       "task",
		workerType: code.WorkerTypeCode,
		launch: func(ctx context.Context, srv *Server) (*mcp.CallToolResult, error) {
			return srv.handleTask(ctx, makeRequest("task", map[string]any{
				"prompt":     "Implement a P26 cancellation proof.",
				"task_class": "code",
			}))
		},
	})
}

func TestP26AsyncContractTemplate_SpecCancelProgressCleanup(t *testing.T) {
	t.Parallel()

	p26RunAsyncContractTemplate(t, p26AsyncToolSpec{
		name:       "spec",
		workerType: specWorkerType,
		launch: func(ctx context.Context, srv *Server) (*mcp.CallToolResult, error) {
			return srv.handleSpec(ctx, makeRequest("spec", map[string]any{
				"prompt": "Write a P26 specification proof.",
				"target": "AIMUX-9 CR-007",
			}))
		},
	})
}

func TestP26AsyncContractTemplate_DeepResearchCancelProgressCleanup(t *testing.T) {
	t.Parallel()

	p26RunAsyncContractTemplate(t, p26AsyncToolSpec{
		name:       "deepresearch",
		workerType: deepResearchWorkerType,
		launch: func(ctx context.Context, srv *Server) (*mcp.CallToolResult, error) {
			return srv.handleDeepresearch(ctx, makeRequest("deepresearch", map[string]any{
				"topic":         "P26 async cancellation proof",
				"output_format": "summary",
			}))
		},
	})
}

func p26RunAsyncContractTemplate(t *testing.T, spec p26AsyncToolSpec) {
	t.Helper()

	engine := newTaskToolEngine(t)
	worker := newP26BlockingWorker(spec.name, spec.workerType, engine)
	engine.RegisterWorker(spec.workerType, worker)
	srv := &Server{
		loom: engine,
		cfg:  &config.Config{},
	}

	result, err := spec.launch(context.Background(), srv)
	if err != nil {
		t.Fatalf("%s launch returned Go error: %v", spec.name, err)
	}
	if result.IsError {
		t.Fatalf("%s launch returned tool error: %s", spec.name, taskToolResultText(t, result))
	}

	payload := decodeTaskToolResult(t, result)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" || payload["job_id"] != taskID {
		t.Fatalf("%s accepted payload missing task/job alias: %v", spec.name, payload)
	}
	if payload["content"] != nil {
		t.Fatalf("%s accepted payload content = %v, want omitted", spec.name, payload["content"])
	}
	for _, field := range []string{"status_command", "cancel_command", "progress_uri"} {
		if strings.TrimSpace(fmt.Sprint(payload[field])) == "" {
			t.Fatalf("%s accepted payload missing %s: %v", spec.name, field, payload)
		}
	}

	p26WaitStarted(t, spec.name, worker)
	p26WaitTaskStatus(t, engine, taskID, loom.TaskStatusRunning)

	statusBeforeCancel := p26CallStatus(t, srv, taskID)
	if got := statusBeforeCancel["progress_tail"]; got != worker.progressLine {
		t.Fatalf("%s status progress_tail = %v, want %q; payload=%v", spec.name, got, worker.progressLine, statusBeforeCancel)
	}
	if got, _ := statusBeforeCancel["progress_lines"].(float64); got < 1 {
		t.Fatalf("%s status progress_lines = %v, want >= 1; payload=%v", spec.name, statusBeforeCancel["progress_lines"], statusBeforeCancel)
	}

	cancelResult, err := srv.handleSessions(context.Background(), makeRequest("sessions", map[string]any{
		"action": "cancel",
		"job_id": taskID,
	}))
	if err != nil {
		t.Fatalf("%s sessions cancel returned Go error: %v", spec.name, err)
	}
	if cancelResult.IsError {
		t.Fatalf("%s sessions cancel returned tool error: %v", spec.name, parseResult(t, cancelResult))
	}
	cancelPayload := parseResult(t, cancelResult)
	if cancelPayload["status"] != "cancelled" {
		t.Fatalf("%s cancel status = %v, want cancelled", spec.name, cancelPayload["status"])
	}

	p26WaitDone(t, spec.name, worker)
	if !worker.cancelObserved.Load() {
		t.Fatalf("%s worker did not observe cancellation", spec.name)
	}

	finalTask := p26WaitTaskStatus(t, engine, taskID, loom.TaskStatusFailed)
	if finalTask.Error != "job cancelled" {
		t.Fatalf("%s final error = %q, want job cancelled", spec.name, finalTask.Error)
	}
	if finalTask.LastOutputLine != worker.progressLine || finalTask.ProgressLines < 1 {
		t.Fatalf("%s final progress fields changed unexpectedly: tail=%q lines=%d", spec.name, finalTask.LastOutputLine, finalTask.ProgressLines)
	}

	if err := engine.AppendProgress(taskID, worker.progressLine+" late"); err != nil {
		t.Fatalf("%s late AppendProgress returned error: %v", spec.name, err)
	}
	afterLate, err := engine.Get(taskID)
	if err != nil {
		t.Fatalf("%s get after late progress: %v", spec.name, err)
	}
	if afterLate.LastOutputLine != worker.progressLine || afterLate.ProgressLines != finalTask.ProgressLines {
		t.Fatalf("%s late progress mutated terminal task: before tail=%q lines=%d after tail=%q lines=%d",
			spec.name, finalTask.LastOutputLine, finalTask.ProgressLines, afterLate.LastOutputLine, afterLate.ProgressLines)
	}

	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := engine.Close(closeCtx); err != nil {
		t.Fatalf("%s LoomEngine.Close() after cancel = %v, want drained worker", spec.name, err)
	}
}

type p26BlockingWorker struct {
	name           string
	workerType     loom.WorkerType
	progress       *loom.LoomEngine
	progressLine   string
	started        chan struct{}
	done           chan struct{}
	release        chan struct{}
	cancelObserved atomic.Bool
}

func newP26BlockingWorker(name string, workerType loom.WorkerType, progress *loom.LoomEngine) *p26BlockingWorker {
	return &p26BlockingWorker{
		name:         name,
		workerType:   workerType,
		progress:     progress,
		progressLine: "p26 " + name + " worker running",
		started:      make(chan struct{}),
		done:         make(chan struct{}),
		release:      make(chan struct{}),
	}
}

func (w *p26BlockingWorker) Type() loom.WorkerType {
	return w.workerType
}

func (w *p26BlockingWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	if w.progress != nil && task != nil {
		_ = w.progress.AppendProgress(task.ID, w.progressLine)
	}
	close(w.started)
	select {
	case <-ctx.Done():
		w.cancelObserved.Store(true)
		close(w.done)
		return nil, ctx.Err()
	case <-w.release:
		close(w.done)
		return &loom.WorkerResult{Content: "released " + w.name}, nil
	}
}

func p26WaitStarted(t *testing.T, name string, worker *p26BlockingWorker) {
	t.Helper()
	select {
	case <-worker.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s worker did not start", name)
	}
}

func p26WaitDone(t *testing.T, name string, worker *p26BlockingWorker) {
	t.Helper()
	select {
	case <-worker.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s worker did not drain after cancellation", name)
	}
}

func p26WaitTaskStatus(t *testing.T, engine *loom.LoomEngine, taskID string, want loom.TaskStatus) *loom.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last *loom.Task
	var lastErr error
	for time.Now().Before(deadline) {
		task, err := engine.Get(taskID)
		if err == nil {
			last = task
			if task.Status == want {
				return task
			}
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("task %s did not reach %s: last error %v", taskID, want, lastErr)
	}
	if last == nil {
		t.Fatalf("task %s did not reach %s: task not found", taskID, want)
	}
	t.Fatalf("task %s status = %s, want %s", taskID, last.Status, want)
	return nil
}

func p26CallStatus(t *testing.T, srv *Server, taskID string) map[string]any {
	t.Helper()
	result, err := srv.handleStatus(context.Background(), makeRequest("status", map[string]any{"job_id": taskID}))
	if err != nil {
		t.Fatalf("status(%s) returned Go error: %v", taskID, err)
	}
	if result.IsError {
		t.Fatalf("status(%s) returned tool error: %v", taskID, parseResult(t, result))
	}
	return parseResult(t, result)
}
