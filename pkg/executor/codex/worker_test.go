package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/runtime"
)

// --- Test doubles ---

type fakeProgressSink struct {
	lines []string
}

func (f *fakeProgressSink) AppendProgress(_, line string) error {
	f.lines = append(f.lines, line)
	return nil
}

type fakeTaskGetter struct {
	tasks map[string]*loom.Task
}

func (f *fakeTaskGetter) Get(taskID string) (*loom.Task, error) {
	t, ok := f.tasks[taskID]
	if !ok {
		return nil, loom.ErrTaskNotFound
	}
	return t, nil
}

// buildTask constructs a minimal loom.Task for worker tests.
func buildTask(jobClass, prompt, projectID string, extraMeta map[string]any) *loom.Task {
	meta := map[string]any{"job_class": jobClass}
	for k, v := range extraMeta {
		meta[k] = v
	}
	return &loom.Task{
		ID:         "task-001",
		Status:     loom.TaskStatusRunning,
		WorkerType: WorkerTypeCodex,
		ProjectID:  projectID,
		Prompt:     prompt,
		CWD:        "/workspace",
		Metadata:   meta,
	}
}

// buildWorkerWithFakePool builds a CodexWorker backed by a testPool and fake process.
// Returns the worker, the fake pool (for inspection), sink, and getter.
func buildWorkerWithFakePool(t *testing.T, dialer *fakeAppServerDialer) (*CodexWorker, *testPool, *fakeProgressSink, *fakeTaskGetter) {
	t.Helper()

	proc := newTestProcess(t, dialer)
	sink := &fakeProgressSink{}
	getter := &fakeTaskGetter{tasks: make(map[string]*loom.Task)}

	pool := newTestPool(t, func(_, _ string) *AppServerProcess { return proc })
	pool.mu.Lock()
	pool.entries["proj-1"] = readyEntry(proc)
	pool.mu.Unlock()

	w, err := NewCodexWorker(CodexWorkerConfig{
		Pool:    pool.CodexPool,
		Loom:    sink,
		LoomGet: getter,
		Model:   "gpt-5-test",
	})
	if err != nil {
		t.Fatalf("NewCodexWorker: %v", err)
	}
	return w, pool, sink, getter
}

// programCompleteTurn sets up a dialer for a successful single-turn execution.
func programCompleteTurn(t *testing.T, d *fakeAppServerDialer, threadID, turnID, agentText string) {
	t.Helper()
	d.respondWith("thread/start", ThreadStartResponse{
		Thread: Thread{ID: threadID, CWD: "/workspace"},
	})
	d.respondWith("turn/start", TurnStartResponse{
		Turn: Turn{ID: turnID, Status: TurnStatusRunning},
	})

	itemParams, _ := json.Marshal(ItemCompletedNotification{
		Item:     ThreadItem{Type: "agentMessage", ID: "item-1", Text: agentText},
		ThreadID: threadID,
		TurnID:   turnID,
	})
	d.queueNotification(JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  MethodItemCompleted,
		Params:  itemParams,
	})

	completedParams, _ := json.Marshal(TurnCompletedNotification{
		ThreadID: threadID,
		Turn:     Turn{ID: turnID, Status: TurnStatusCompleted},
	})
	d.queueNotification(JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  MethodTurnCompleted,
		Params:  completedParams,
	})
}

// buildProcessFromPipePair builds an AppServerProcess wired to in-process pipes.
// clientWrite and clientRead are the client side; the test controls the server side.
func buildProcessFromPipePair(t *testing.T, clientWrite io.WriteCloser, clientRead io.Reader) *AppServerProcess {
	t.Helper()
	proc := &AppServerProcess{
		codexPath: "/fake/codex",
		profile:   runtime.CLIRuntimeProfile{},
		state:     AppServerStateReady,
	}
	client := NewJSONLClient(clientWrite, clientRead)
	readCtx, cancel := context.WithCancel(context.Background())
	go client.Start(readCtx)

	proc.mu.Lock()
	proc.client = client
	proc.cancelReadLoop = cancel
	proc.mu.Unlock()

	t.Cleanup(func() {
		cancel()
		clientWrite.Close()
	})
	return proc
}

// --- Tests ---

func TestCodexWorker_Type(t *testing.T) {
	d := newFakeDialer(t)
	w, _, _, _ := buildWorkerWithFakePool(t, d)
	if w.Type() != WorkerTypeCodex {
		t.Errorf("Type() = %q, want %q", w.Type(), WorkerTypeCodex)
	}
}

func TestCodexWorker_NewWorker_NilPool_Fails(t *testing.T) {
	sink := &fakeProgressSink{}
	getter := &fakeTaskGetter{tasks: make(map[string]*loom.Task)}
	_, err := NewCodexWorker(CodexWorkerConfig{Loom: sink, LoomGet: getter})
	if err == nil {
		t.Error("expected error for nil Pool")
	}
}

func TestCodexWorker_NewWorker_NilLoom_Fails(t *testing.T) {
	pool := newTestPool(t, nil)
	getter := &fakeTaskGetter{tasks: make(map[string]*loom.Task)}
	_, err := NewCodexWorker(CodexWorkerConfig{Pool: pool.CodexPool, LoomGet: getter})
	if err == nil {
		t.Error("expected error for nil Loom")
	}
}

func TestCodexWorker_NewWorker_NilLoomGet_Fails(t *testing.T) {
	pool := newTestPool(t, nil)
	sink := &fakeProgressSink{}
	_, err := NewCodexWorker(CodexWorkerConfig{Pool: pool.CodexPool, Loom: sink})
	if err == nil {
		t.Error("expected error for nil LoomGet")
	}
}

func TestCodexWorker_Execute_MissingJobClass_Fails(t *testing.T) {
	d := newFakeDialer(t)
	w, _, _, _ := buildWorkerWithFakePool(t, d)

	task := &loom.Task{
		ID:        "task-bad",
		ProjectID: "proj-1",
		Prompt:    "do something",
		CWD:       "/work",
		Metadata:  map[string]any{},
	}

	_, err := w.Execute(context.Background(), task)
	if err == nil {
		t.Error("expected error for missing job_class")
	}
}

func TestCodexWorker_Execute_UnknownJobClass_Fails(t *testing.T) {
	d := newFakeDialer(t)
	w, _, _, _ := buildWorkerWithFakePool(t, d)

	task := buildTask("bogus-class", "prompt", "proj-1", nil)
	_, err := w.Execute(context.Background(), task)
	if err == nil {
		t.Error("expected error for unknown job_class")
	}
}

// TestCodexWorker_Execute_HappyPath verifies the full execution path.
func TestCodexWorker_Execute_HappyPath(t *testing.T) {
	d := newFakeDialer(t)
	programCompleteTurn(t, d, "thread-happy", "turn-happy", "The answer is 42")

	w, _, sink, _ := buildWorkerWithFakePool(t, d)

	task := buildTask(JobClassTask, "what is the answer?", "proj-1", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := w.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Content != "The answer is 42" {
		t.Errorf("Content: got %q, want %q", result.Content, "The answer is 42")
	}
	if v, _ := result.Metadata["thread_id"]; v != "thread-happy" {
		t.Errorf("metadata thread_id: got %v, want %q", v, "thread-happy")
	}
	if v, _ := result.Metadata["turn_id"]; v != "turn-happy" {
		t.Errorf("metadata turn_id: got %v, want %q", v, "turn-happy")
	}
	if len(sink.lines) == 0 {
		t.Error("expected at least one progress line in sink")
	}
	if sink.lines[0] != "The answer is 42" {
		t.Errorf("sink.lines[0]: got %q, want %q", sink.lines[0], "The answer is 42")
	}
	if result.DurationMS <= 0 {
		t.Errorf("DurationMS: got %d, want > 0", result.DurationMS)
	}
}

// TestCodexWorker_Execute_ReviewClass_EphemeralThread verifies ADR-006 policy for review class.
func TestCodexWorker_Execute_ReviewClass_EphemeralThread(t *testing.T) {
	var capturedThreadParams ThreadStartParams

	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      *int64          `json:"id,omitempty"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params,omitempty"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue
			}
			switch msg.Method {
			case "thread/start":
				_ = json.Unmarshal(msg.Params, &capturedThreadParams)
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"thread":{"id":"t-review","cwd":"/work"}}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			case "turn/start":
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"turn":{"id":"turn-review","status":"running"}}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
				go func() {
					time.Sleep(5 * time.Millisecond)
					completed, _ := json.Marshal(TurnCompletedNotification{
						ThreadID: "t-review",
						Turn:     Turn{ID: "turn-review", Status: TurnStatusCompleted},
					})
					notif, _ := json.Marshal(JSONRPCNotification{JSONRPC: "2.0", Method: MethodTurnCompleted, Params: completed})
					fmt.Fprintln(serverWrite, string(notif))
				}()
			default:
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	proc := buildProcessFromPipePair(t, clientWrite, clientRead)
	pool := newTestPool(t, nil)
	pool.mu.Lock()
	pool.entries["proj-review"] = readyEntry(proc)
	pool.mu.Unlock()

	w, _ := NewCodexWorker(CodexWorkerConfig{
		Pool:    pool.CodexPool,
		Loom:    &fakeProgressSink{},
		LoomGet: &fakeTaskGetter{tasks: make(map[string]*loom.Task)},
	})

	task := buildTask(JobClassReview, "review this", "proj-review", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := w.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !capturedThreadParams.Ephemeral {
		t.Error("review class must start ephemeral thread")
	}
	if capturedThreadParams.Sandbox != SandboxModeReadOnly {
		t.Errorf("review sandbox: got %q, want %q", capturedThreadParams.Sandbox, SandboxModeReadOnly)
	}
}

// TestCodexWorker_Execute_ResumePath verifies resume_task_id lookup → thread/resume.
func TestCodexWorker_Execute_ResumePath(t *testing.T) {
	priorMeta := CodexTaskMeta{
		ThreadID:     "thread-prior",
		RootThreadID: "thread-prior",
		JobClass:     JobClassTask,
	}
	priorMetaMap, _ := codeTaskMetaToMap(priorMeta)
	priorTask := &loom.Task{
		ID:        "prior-task-001",
		Status:    loom.TaskStatusCompleted,
		ProjectID: "proj-resume",
		Metadata:  priorMetaMap,
	}

	d := newFakeDialer(t)
	d.respondWith("thread/resume", ThreadResumeResponse{
		Thread: Thread{ID: "thread-prior", CWD: "/work"},
	})
	d.respondWith("turn/start", TurnStartResponse{
		Turn: Turn{ID: "turn-resumed", Status: TurnStatusRunning},
	})
	completedParams, _ := json.Marshal(TurnCompletedNotification{
		ThreadID: "thread-prior",
		Turn:     Turn{ID: "turn-resumed", Status: TurnStatusCompleted},
	})
	d.queueNotification(JSONRPCNotification{JSONRPC: "2.0", Method: MethodTurnCompleted, Params: completedParams})

	w, pool, _, getter := buildWorkerWithFakePool(t, d)
	getter.tasks["prior-task-001"] = priorTask

	proc := pool.entries["proj-1"].process
	pool.mu.Lock()
	delete(pool.entries, "proj-1")
	pool.entries["proj-resume"] = readyEntry(proc)
	pool.mu.Unlock()

	task := buildTask(JobClassTask, "continue", "proj-resume", map[string]any{
		"resume_task_id": "prior-task-001",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := w.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute (resume): %v", err)
	}

	if v, _ := result.Metadata["thread_id"]; v != "thread-prior" {
		t.Errorf("resume thread_id: got %v, want %q", v, "thread-prior")
	}
}

// TestCodexWorker_Execute_ResumeFallback verifies ErrThreadNotFound → fresh thread.
func TestCodexWorker_Execute_ResumeFallback(t *testing.T) {
	priorMeta := CodexTaskMeta{ThreadID: "ghost-thread", JobClass: JobClassTask}
	priorMetaMap, _ := codeTaskMetaToMap(priorMeta)
	priorTask := &loom.Task{
		ID:       "prior-ghost",
		Status:   loom.TaskStatusCompleted,
		Metadata: priorMetaMap,
	}

	d := newFakeDialer(t)
	d.respondError("thread/resume", -32600, "thread not found")
	d.respondWith("thread/start", ThreadStartResponse{
		Thread: Thread{ID: "fresh-thread", CWD: "/work"},
	})
	d.respondWith("turn/start", TurnStartResponse{
		Turn: Turn{ID: "turn-fresh", Status: TurnStatusRunning},
	})
	completedParams, _ := json.Marshal(TurnCompletedNotification{
		ThreadID: "fresh-thread",
		Turn:     Turn{ID: "turn-fresh", Status: TurnStatusCompleted},
	})
	d.queueNotification(JSONRPCNotification{JSONRPC: "2.0", Method: MethodTurnCompleted, Params: completedParams})

	w, pool, _, getter := buildWorkerWithFakePool(t, d)
	getter.tasks["prior-ghost"] = priorTask

	proc := pool.entries["proj-1"].process
	pool.mu.Lock()
	delete(pool.entries, "proj-1")
	pool.entries["proj-fallback"] = readyEntry(proc)
	pool.mu.Unlock()

	task := buildTask(JobClassTask, "try to resume", "proj-fallback", map[string]any{
		"resume_task_id": "prior-ghost",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := w.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute (fallback): %v", err)
	}

	if fallback, _ := result.Metadata["resume_fallback"]; fallback != true {
		t.Errorf("resume_fallback: got %v, want true", fallback)
	}
}

// TestCodexWorker_Execute_ContextCancel verifies cancellation produces context error.
func TestCodexWorker_Execute_ContextCancel(t *testing.T) {
	d := newFakeDialer(t)
	d.respondWith("thread/start", ThreadStartResponse{
		Thread: Thread{ID: "thread-cancel", CWD: "/work"},
	})
	// turn/start succeeds but no turn/completed is queued — turn hangs.
	d.respondWith("turn/start", TurnStartResponse{
		Turn: Turn{ID: "turn-cancel", Status: TurnStatusRunning},
	})

	w, _, _, _ := buildWorkerWithFakePool(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	task := buildTask(JobClassTask, "slow operation", "proj-1", nil)

	_, err := w.Execute(ctx, task)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

// TestCodexWorker_Execute_TurnFailed verifies turn failure → error.
func TestCodexWorker_Execute_TurnFailed(t *testing.T) {
	d := newFakeDialer(t)
	d.respondWith("thread/start", ThreadStartResponse{
		Thread: Thread{ID: "thread-fail", CWD: "/work"},
	})
	d.respondWith("turn/start", TurnStartResponse{
		Turn: Turn{ID: "turn-fail", Status: TurnStatusRunning},
	})
	failedParams, _ := json.Marshal(TurnCompletedNotification{
		ThreadID: "thread-fail",
		Turn: Turn{
			ID:    "turn-fail",
			Status: TurnStatusFailed,
			Error: &TurnError{Code: "model_error", Message: "context window exceeded"},
		},
	})
	d.queueNotification(JSONRPCNotification{JSONRPC: "2.0", Method: MethodTurnCompleted, Params: failedParams})

	w, _, _, _ := buildWorkerWithFakePool(t, d)

	task := buildTask(JobClassTask, "too long prompt", "proj-1", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := w.Execute(ctx, task)
	if err == nil {
		t.Fatal("expected error for failed turn, got nil")
	}
	if !strings.Contains(err.Error(), "turn failed") {
		t.Errorf("error must mention 'turn failed': %v", err)
	}
}

// TestCodexWorker_Execute_StreamClosedEarly verifies that if the turn stream closes
// without a turn/completed notification (e.g. process crash or idle eviction), the
// worker returns an error instead of a silent partial/empty success.
func TestCodexWorker_Execute_StreamClosedEarly(t *testing.T) {
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string  `json:"jsonrpc"`
				ID      *int64  `json:"id,omitempty"`
				Method  string  `json:"method"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue
			}
			switch msg.Method {
			case "thread/start":
				fmt.Fprintf(serverWrite, `{"jsonrpc":"2.0","id":%d,"result":{"thread":{"id":"t-crash","cwd":"/work"}}}`+"\n", *msg.ID)
			case "turn/start":
				fmt.Fprintf(serverWrite, `{"jsonrpc":"2.0","id":%d,"result":{"turn":{"id":"turn-crash","status":"running"}}}`+"\n", *msg.ID)
				// Simulate process crash: close the server-side write pipe without
				// sending turn/completed. This closes the client's read pipe.
				go func() {
					time.Sleep(10 * time.Millisecond)
					serverWrite.Close()
				}()
			default:
				fmt.Fprintf(serverWrite, `{"jsonrpc":"2.0","id":%d,"result":null}`+"\n", *msg.ID)
			}
		}
	}()

	proc := buildProcessFromPipePair(t, clientWrite, clientRead)
	pool := newTestPool(t, nil)
	pool.mu.Lock()
	pool.entries["proj-crash"] = readyEntry(proc)
	pool.mu.Unlock()

	w, _ := NewCodexWorker(CodexWorkerConfig{
		Pool:    pool.CodexPool,
		Loom:    &fakeProgressSink{},
		LoomGet: &fakeTaskGetter{tasks: make(map[string]*loom.Task)},
	})

	task := buildTask(JobClassTask, "crash test", "proj-crash", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := w.Execute(ctx, task)
	if err == nil {
		t.Fatal("expected error when turn stream closes without turn/completed")
	}
	if !strings.Contains(err.Error(), "prematurely") {
		t.Errorf("error should mention premature closure; got: %v", err)
	}
}

// --- helpers ---

func workerContains(s, substr string) bool {
	return strings.Contains(s, substr)
}
