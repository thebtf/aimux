package codex

import (
	"context"
	"fmt"
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
	pool *CodexPool
}

// NewResumer constructs a Resumer backed by the given pool.
func NewResumer(pool *CodexPool) *Resumer {
	return &Resumer{pool: pool}
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
