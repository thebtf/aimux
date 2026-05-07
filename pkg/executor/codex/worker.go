package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/types"
)

// WorkerTypeCodex is the loom.WorkerType for codex tasks.
// Registered with LoomEngine so tasks with worker_type="codex" are dispatched here.
const WorkerTypeCodex loom.WorkerType = "codex"

// progressSink is the subset of LoomEngine used for progress reporting.
// Extracted as an interface for testability.
type progressSink interface {
	AppendProgress(taskID, line string) error
}

// taskGetter is the subset of LoomEngine used for resume-path lookup.
// Extracted as an interface for testability.
type taskGetter interface {
	Get(taskID string) (*loom.Task, error)
}

// CodexWorker implements loom.Worker for codex tasks.
//
// Execution flow (per task):
//  1. Parse CodexTaskMeta from task.Metadata (job_class required).
//  2. Resolve sandbox policy via ForClass(job_class).
//  3. Acquire an AppServerProcess from the pool.
//  4. Start or resume a thread (resume if resume_task_id present in metadata).
//  5. Start a turn, stream progress via loom.AppendProgress.
//  6. Return the aggregated agent message text as WorkerResult.Content.
//
// The worker is stateless — it holds no per-task state. Concurrent Execute
// calls on different tasks are safe.
type CodexWorker struct {
	pool     *CodexPool
	loom     progressSink
	loomGet  taskGetter
	model    string // default model; overridden by task.Model if set
}

// CodexWorkerConfig holds constructor parameters for CodexWorker.
type CodexWorkerConfig struct {
	Pool    *CodexPool
	Loom    progressSink
	LoomGet taskGetter
	// Model is the default codex model. If empty, codex uses its own default.
	Model string
}

// NewCodexWorker constructs a CodexWorker.
func NewCodexWorker(cfg CodexWorkerConfig) (*CodexWorker, error) {
	if cfg.Pool == nil {
		return nil, fmt.Errorf("codex: NewCodexWorker: Pool must not be nil")
	}
	if cfg.Loom == nil {
		return nil, fmt.Errorf("codex: NewCodexWorker: Loom must not be nil")
	}
	if cfg.LoomGet == nil {
		return nil, fmt.Errorf("codex: NewCodexWorker: LoomGet must not be nil")
	}
	return &CodexWorker{
		pool:    cfg.Pool,
		loom:    cfg.Loom,
		loomGet: cfg.LoomGet,
		model:   cfg.Model,
	}, nil
}

// Type implements loom.Worker.
func (w *CodexWorker) Type() loom.WorkerType {
	return WorkerTypeCodex
}

// Execute implements loom.Worker.
//
// task.Metadata must contain:
//   - "job_class": string ("review"|"task"|"write-task"|"danger")
//
// Optional:
//   - "resume_task_id": string — if set, resume the thread from the prior task
//   - "thread_cwd": string — override cwd for this thread/turn (defaults to task.CWD)
func (w *CodexWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	start := time.Now()

	// --- 1. Parse metadata ---
	meta, err := parseCodexTaskMeta(task)
	if err != nil {
		return nil, mapToCliError(fmt.Errorf("codex worker: parse metadata: %w", err))
	}

	// --- 2. Sandbox policy ---
	sandboxCfg, err := ForClass(meta.JobClass)
	if err != nil {
		return nil, mapToCliError(fmt.Errorf("codex worker: sandbox: %w", err))
	}

	// --- 3. Acquire process ---
	workDir := task.CWD
	if v, ok := stringFromMeta(task.Metadata, "thread_cwd"); ok && v != "" {
		workDir = v
	}

	proc, err := w.pool.Acquire(ctx, task.ProjectID, workDir)
	if err != nil {
		// pool.Acquire already returns *types.CLIError; mapToCliError passes through.
		return nil, mapToCliError(err)
	}
	defer w.pool.Release(task.ProjectID)

	// --- 4. Start or resume thread ---
	model := w.model
	if task.Model != "" {
		model = task.Model
	}

	thread, updatedMeta, err := w.acquireThread(ctx, proc, task, meta, workDir, model, sandboxCfg)
	if err != nil {
		return nil, mapToCliError(err)
	}

	// --- 5. Start turn, stream progress ---
	turnParams := TurnStartParams{
		ThreadID:     thread.ID,
		Input:        []UserInput{{Type: "text", Text: task.Prompt}},
		CWD:          workDir,
		Model:        model,
		OutputSchema: meta.OutputSchema,
	}
	completedCh, progressCh, err := proc.StartTurn(ctx, turnParams)
	if err != nil {
		return nil, mapToCliError(fmt.Errorf("codex worker: start turn: %w", err))
	}

	// Fan progress lines to loom.AppendProgress and accumulate text.
	var lines []string
	var turnCompleted TurnCompletedNotification
	turnCompletedReceived := false
	done := false
	for !done {
		select {
		case text, ok := <-progressCh:
			if !ok {
				// Channel closed by the fanout goroutine. If we never received a
				// turn/completed notification, the process exited or was evicted
				// mid-turn — treat as an error so Loom can retry or fail the task.
				done = true
				break
			}
			lines = append(lines, text)
			// Best-effort progress (ignore errors — non-critical).
			_ = w.loom.AppendProgress(task.ID, text)

		case completed, ok := <-completedCh:
			if !ok {
				done = true
				break
			}
			turnCompleted = completed
			turnCompletedReceived = true
			// Drain remaining progress.
			for text := range progressCh {
				lines = append(lines, text)
				_ = w.loom.AppendProgress(task.ID, text)
			}
			done = true

		case <-ctx.Done():
			// Context cancelled — interrupt best-effort.
			interruptCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = proc.Interrupt(interruptCtx)
			cancel()
			return nil, types.NewTimeout("context cancelled", ctx.Err())
		}
	}

	// --- 6. Build result ---

	// If the turn stream closed without a turn/completed notification the process
	// exited prematurely (crash, idle eviction, or shutdown). Return an error
	// so Loom records a failure instead of an empty/partial success.
	if !turnCompletedReceived {
		return nil, types.NewUnknown("codex worker: turn stream closed without completion (process exited prematurely)", nil)
	}

	if turnCompleted.Turn.Status == TurnStatusFailed {
		errMsg := "codex: turn failed"
		if turnCompleted.Turn.Error != nil {
			errMsg = fmt.Sprintf("codex: turn failed: %s (code: %s)",
				turnCompleted.Turn.Error.Message, turnCompleted.Turn.Error.Code)
		}
		return nil, mapToCliError(fmt.Errorf("%s", errMsg))
	}

	content := strings.Join(lines, "\n")

	// Store updated meta for resume.
	updatedMeta.TurnID = turnCompleted.Turn.ID
	metaMap, _ := codeTaskMetaToMap(updatedMeta)

	return &loom.WorkerResult{
		Content:    content,
		Metadata:   metaMap,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// acquireThread starts a new thread or resumes an existing one.
// Returns the Thread and updated CodexTaskMeta.
func (w *CodexWorker) acquireThread(
	ctx context.Context,
	proc *AppServerProcess,
	task *loom.Task,
	meta CodexTaskMeta,
	workDir, model string,
	sandboxCfg SandboxConfig,
) (Thread, CodexTaskMeta, error) {
	// Check for resume path (ADR-008).
	if resumeTaskID, ok := stringFromMeta(task.Metadata, "resume_task_id"); ok && resumeTaskID != "" {
		priorTask, err := w.loomGet.Get(resumeTaskID)
		if err != nil {
			// Resume task not found — fall through to fresh thread.
			goto fresh
		}
		priorMeta, err := parseCodexTaskMetaFromMap(priorTask.Metadata)
		if err != nil || priorMeta.ThreadID == "" {
			goto fresh
		}

		resumeParams := ThreadResumeParams{
			ThreadID:       priorMeta.ThreadID,
			CWD:            workDir,
			ApprovalPolicy: sandboxCfg.AskForApproval,
			Sandbox:        sandboxCfg.Mode,
		}
		thread, err := proc.ResumeThread(ctx, resumeParams)
		if err == ErrThreadNotFound {
			// Thread not found on disk — fall through to fresh thread with fallback flag.
			meta.ResumeFallback = true
			goto fresh
		}
		if err != nil {
			return Thread{}, meta, err
		}
		meta.ThreadID = thread.ID
		meta.RootThreadID = priorMeta.RootThreadID
		if meta.RootThreadID == "" {
			meta.RootThreadID = priorMeta.ThreadID
		}
		return thread, meta, nil
	}

fresh:
	threadParams := ThreadStartParams{
		Model:          model,
		CWD:            workDir,
		ApprovalPolicy: sandboxCfg.AskForApproval,
		Sandbox:        sandboxCfg.Mode,
		Ephemeral:      meta.JobClass == JobClassReview,
	}
	thread, err := proc.StartThread(ctx, threadParams)
	if err != nil {
		return Thread{}, meta, err
	}
	meta.ThreadID = thread.ID
	if meta.RootThreadID == "" {
		meta.RootThreadID = thread.ID
	}
	return thread, meta, nil
}

// --- helpers ---

// parseCodexTaskMeta extracts CodexTaskMeta from task.Metadata.
// job_class is required.
func parseCodexTaskMeta(task *loom.Task) (CodexTaskMeta, error) {
	return parseCodexTaskMetaFromMap(task.Metadata)
}

func parseCodexTaskMetaFromMap(m map[string]any) (CodexTaskMeta, error) {
	if m == nil {
		return CodexTaskMeta{}, fmt.Errorf("task metadata is nil; job_class required")
	}
	jobClass, ok := stringFromMeta(m, "job_class")
	if !ok || jobClass == "" {
		return CodexTaskMeta{}, fmt.Errorf("task metadata missing job_class")
	}
	var meta CodexTaskMeta
	// Re-marshal through JSON for forward-compatible struct decode.
	b, err := json.Marshal(m)
	if err != nil {
		return CodexTaskMeta{}, fmt.Errorf("marshal metadata: %w", err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return CodexTaskMeta{}, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return meta, nil
}

// codeTaskMetaToMap converts CodexTaskMeta to map[string]any for Loom storage.
func codeTaskMetaToMap(meta CodexTaskMeta) (map[string]any, error) {
	b, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// stringFromMeta extracts a string value from a map[string]any.
func stringFromMeta(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
