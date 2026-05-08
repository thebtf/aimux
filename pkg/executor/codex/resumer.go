package codex

import (
	"context"
	"errors"
	"fmt"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// Resumer provides thread resume operations for the CodexWorker.
//
// Primary path (O(1)): Resume by known ThreadID via thread/resume on a fresh process.
// Fallback path (slow): FindBySearchTerm via thread/list with useStateDbOnly=true.
//
// ADR-008: thread/list is FORBIDDEN in the hot resume path.
// thread/list timed out at 15s without useStateDbOnly; with the flag: ~50-70ms
// (VERIFIED: probe-2026-05-07 OQ-1 — 270x speedup with useStateDbOnly=true).
type Resumer struct {
	pool  *CodexPool
	tasks taskGetter
}

// NewResumer constructs a Resumer backed by the given pool.
func NewResumer(pool *CodexPool) *Resumer {
	return &Resumer{pool: pool}
}

// NewResumerWithTasks constructs a Resumer that can hydrate Loom resume metadata.
func NewResumerWithTasks(pool *CodexPool, tasks taskGetter) *Resumer {
	return &Resumer{pool: pool, tasks: tasks}
}

// ResumeFromTask hydrates Codex resume metadata from a previous Codex task.
func (r *Resumer) ResumeFromTask(ctx context.Context, prevTaskID string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, resumeContextError("codex resume canceled", err)
	}
	if prevTaskID == "" {
		return nil, types.NewUserInputError("resume_id is required", nil)
	}
	if r.tasks == nil {
		return nil, types.NewCapabilityMismatch("codex Resumer requires Loom task lookup for ResumeFromTask", nil)
	}

	prev, err := r.tasks.Get(prevTaskID)
	if err != nil {
		return nil, types.NewUserInputError(fmt.Sprintf("resume task %q not found", prevTaskID), err)
	}
	if prev.WorkerType != WorkerTypeCodex {
		return nil, types.NewResumeWorkerMismatch(
			fmt.Sprintf("resume_id worker mismatch: expected %s, got %s", WorkerTypeCodex, prev.WorkerType),
			nil,
		)
	}

	meta, err := parseCodexTaskMetaFromMap(prev.Metadata)
	if err != nil {
		return nil, types.NewCapabilityMismatch("codex resume task metadata is invalid", err)
	}
	if meta.ThreadID == "" {
		return nil, types.NewCapabilityMismatch("codex resume task is missing thread_id", nil)
	}
	if meta.RootThreadID == "" {
		meta.RootThreadID = meta.ThreadID
	}
	out, err := codeTaskMetaToMap(meta)
	if err != nil {
		return nil, types.NewUnknown("codex resume metadata marshal failed", err)
	}
	out["resume_task_id"] = prevTaskID
	out["worker_type"] = string(WorkerTypeCodex)
	return out, nil
}

func resumeContextError(message string, err error) *types.CLIError {
	if errors.Is(err, context.DeadlineExceeded) {
		return types.NewTimeout(message, err)
	}
	return types.NewCanceled(message, err)
}

// FindBySearchTerm scans thread/list with useStateDbOnly=true for threads whose
// preview or metadata matches the given prefix/term. Returns the first matching
// ThreadID, or an empty string with no error if no match is found.
//
// This is the FALLBACK path only — used when CodexTaskMeta.ThreadID is lost.
// The hot resume path reads ThreadID directly from Loom metadata (ADR-008).
//
// Always passes useStateDbOnly:true to avoid the 19s JSONL scan (OQ-1 probe).
func (r *Resumer) FindBySearchTerm(ctx context.Context, projectID, workDir, searchTerm string) (string, error) {
	proc, err := r.pool.Acquire(ctx, projectID, workDir)
	if err != nil {
		return "", fmt.Errorf("codex: Resumer.FindBySearchTerm: acquire process: %w", err)
	}
	defer r.pool.Release(projectID)

	params := ThreadListParams{
		SearchTerm:     searchTerm,
		SourceKinds:    []string{"appServer"},
		UseStateDbOnly: true, // ALWAYS true — 270x speedup (probe-2026-05-07 OQ-1)
	}

	var resp ThreadListResponse
	if err := proc.client.Call(ctx, "thread/list", params, &resp); err != nil {
		return "", fmt.Errorf("codex: Resumer.FindBySearchTerm: thread/list RPC: %w", err)
	}

	if len(resp.Data) == 0 {
		return "", nil // no match — caller should start a fresh thread
	}
	return resp.Data[0].ID, nil
}

// Resume resumes a thread on a fresh AppServerProcess and returns the Thread.
// On ErrThreadNotFound (RPC -32600), returns ("", ErrThreadNotFound) — caller
// should fall back to starting a fresh thread.
func (r *Resumer) Resume(
	ctx context.Context,
	projectID, workDir, threadID string,
	sandbox SandboxConfig,
) (Thread, error) {
	proc, err := r.pool.Acquire(ctx, projectID, workDir)
	if err != nil {
		return Thread{}, fmt.Errorf("codex: Resumer.Resume: acquire process: %w", err)
	}
	defer r.pool.Release(projectID)

	params := ThreadResumeParams{
		ThreadID:       threadID,
		CWD:            workDir,
		ApprovalPolicy: sandbox.AskForApproval,
		Sandbox:        sandbox.Mode,
	}
	return proc.ResumeThread(ctx, params)
}
