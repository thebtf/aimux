package session

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/util"
)

// Job represents an async execution task.
type Job struct {
	ID                string               `json:"id"`
	SessionID         string               `json:"session_id"`
	CLI               string               `json:"cli"`
	Status            types.JobStatus      `json:"status"`
	Progress          string               `json:"progress,omitempty"`
	Content           string               `json:"content,omitempty"`
	ExitCode          int                  `json:"exit_code"`
	Error             *types.TypedError    `json:"error,omitempty"`
	PollCount         int                  `json:"poll_count"`
	Pheromones        map[string]string    `json:"pheromones,omitempty"`
	Pipeline          *types.PipelineStats `json:"pipeline,omitempty"`
	PID               int                  `json:"pid"`
	CreatedAt         time.Time            `json:"created_at"`
	ProgressUpdatedAt time.Time            `json:"progress_updated_at"`
	LastOutputAt      time.Time            `json:"last_output_at,omitempty"`
	CompletedAt       *time.Time           `json:"completed_at,omitempty"`
	// LastOutputLine is the last non-empty line of accumulated progress, UTF-8-safe
	// truncated to ≤100 bytes. Updated on every AppendProgress call. O(1) extraction.
	LastOutputLine string `json:"last_output_line,omitempty"`
	// ProgressLines is the total count of lines seen in progress buffer writes
	// (one per AppendProgress call, plus one per embedded \n). Monotonically increasing.
	// int64 to avoid overflow on 32-bit architectures for extremely long-running jobs.
	ProgressLines int64 `json:"progress_lines,omitempty"`
}

// Deprecated: JobManager is superseded by pkg/loom.LoomEngine for task management.
// Kept for backward compatibility during migration. New code should use LoomEngine.
//
// JobManager manages async jobs with state machine transitions.
type JobManager struct {
	jobs    map[string]*Job
	cancels map[string]context.CancelFunc
	mu      sync.RWMutex
	store   *Store // optional — if set, jobs are persisted immediately on create/complete
}

// NewJobManager creates an empty job manager.
func NewJobManager() *JobManager {
	return &JobManager{
		jobs:    make(map[string]*Job),
		cancels: make(map[string]context.CancelFunc),
	}
}

// SetStore enables immediate persistence — every Create/Complete/Fail writes to SQLite.
// Without this, jobs survive only until the next 30s snapshot interval.
func (m *JobManager) SetStore(store *Store) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
}

// Create registers a new job for a session.
func (m *JobManager) Create(sessionID, cli string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New() // V4 fallback — never panics
	}

	j := &Job{
		ID:                id.String(),
		SessionID:         sessionID,
		CLI:               cli,
		Status:            types.JobStatusCreated,
		Pheromones:        make(map[string]string),
		CreatedAt:         now,
		ProgressUpdatedAt: now,
	}

	m.jobs[j.ID] = j

	// Immediate persist — survive process restart between 30s snapshot intervals.
	if m.store != nil {
		if snapErr := m.store.SnapshotJob(j); snapErr != nil {
			fmt.Fprintf(os.Stderr, "session: Create: SnapshotJob failed for job %s (durability warning): %v\n", j.ID, snapErr)
		}
	}

	return j
}

// UpdateJobFields applies a mutation function to a job under the write lock.
// Used by WAL recovery to update status/content without exposing the raw pointer.
// Returns false if the job does not exist.
func (m *JobManager) UpdateJobFields(id string, fn func(*Job)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok {
		return false
	}
	fn(j)
	return true
}

// Import inserts a job from recovery (WAL replay). Thread-safe.
func (m *JobManager) Import(j *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = j
}

// Restore adds a job directly to the in-memory store (for SQLite recovery).
func (m *JobManager) Restore(j *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = j
}

// Get returns a live job pointer by ID, or nil if not found.
// Callers that only need to read must prefer GetSnapshot to avoid data races.
func (m *JobManager) Get(id string) *Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobs[id]
}

// GetSnapshot returns a deep-copied job snapshot by ID, or nil if not found.
// The returned job is detached from internal mutable state and safe for read-only use.
func (m *JobManager) GetSnapshot(id string) *Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	j, ok := m.jobs[id]
	if !ok {
		return nil
	}
	return cloneJob(j)
}

func cloneJob(j *Job) *Job {
	if j == nil {
		return nil
	}

	copy := *j

	if j.CompletedAt != nil {
		completedAt := *j.CompletedAt
		copy.CompletedAt = &completedAt
	}

	if j.Error != nil {
		errCopy := *j.Error
		// Detach snapshot error from live-state: keep metadata/message but drop
		// underlying wrapped cause pointer to avoid aliasing via Unwrap().
		errCopy.Cause = nil
		copy.Error = &errCopy
	}

	if j.Pipeline != nil {
		pipelineCopy := *j.Pipeline
		copy.Pipeline = &pipelineCopy
	}

	if j.Pheromones != nil {
		pheromones := make(map[string]string, len(j.Pheromones))
		for k, v := range j.Pheromones {
			pheromones[k] = v
		}
		copy.Pheromones = pheromones
	}

	return &copy
}

// StartJob transitions a job to running state.
func (m *JobManager) StartJob(id string, pid int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok || j.Status != types.JobStatusCreated {
		return false
	}
	j.Status = types.JobStatusRunning
	j.PID = pid
	j.ProgressUpdatedAt = time.Now()
	if m.store != nil {
		if snapErr := m.store.SnapshotJob(j); snapErr != nil {
			fmt.Fprintf(os.Stderr, "session: StartJob: SnapshotJob failed for job %s (durability warning): %v\n", id, snapErr)
		}
	}
	return true
}

// UpdateProgress replaces the progress text for a running job.
func (m *JobManager) UpdateProgress(id, progress string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok || j.Status != types.JobStatusRunning {
		return false
	}
	j.Progress = progress
	j.ProgressUpdatedAt = time.Now()
	return true
}

// AppendProgress appends a line to the progress text for a running job.
// More efficient than UpdateProgress for streaming — avoids resending the full buffer.
// Also maintains LastOutputLine (last non-empty line, ≤100 UTF-8 bytes) and
// ProgressLines (total line count: 1 per call + embedded \n count) for O(1) compact-field access on status().
func (m *JobManager) AppendProgress(id, line string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok || j.Status != types.JobStatusRunning {
		return false
	}
	if j.Progress != "" {
		j.Progress += "\n"
	}
	j.Progress += line

	// Each AppendProgress call contributes at least 1 line to the count
	// (the content being appended), plus any embedded newlines within it.
	j.ProgressLines += 1 + int64(strings.Count(line, "\n"))

	// Update LastOutputLine: use the last non-empty line from the appended text.
	// If line contains embedded newlines, walk them to find the last non-empty segment.
	lastNonEmpty := lastNonEmptyLine(line)
	if lastNonEmpty != "" {
		j.LastOutputLine = util.TruncateUTF8(lastNonEmpty, 100)
	}
	// If line was all whitespace, LastOutputLine retains its previous value.

	now := time.Now()
	j.ProgressUpdatedAt = now
	j.LastOutputAt = now
	return true
}

// lastNonEmptyLine returns the last non-empty (non-whitespace-only) segment
// when s is split by newlines. Returns "" if s contains only whitespace.
// Uses a reverse scan via strings.LastIndex to avoid allocating a []string
// for the whole input — O(n) in the worst case but avoids per-segment allocations.
func lastNonEmptyLine(s string) string {
	for {
		idx := strings.LastIndex(s, "\n")
		line := s[idx+1:]
		if strings.TrimSpace(line) != "" {
			return line
		}
		if idx == -1 {
			break
		}
		s = s[:idx]
	}
	return ""
}

// CompleteJob transitions a job to completed state.
func (m *JobManager) CompleteJob(id, content string, exitCode int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok {
		return false
	}
	// Allow transition from running or completing
	if j.Status != types.JobStatusRunning && j.Status != types.JobStatusCompleting {
		return false
	}
	now := time.Now()
	j.Status = types.JobStatusCompleted
	j.Content = content
	j.ExitCode = exitCode
	j.PID = 0
	j.CompletedAt = &now

	// Clean up cancel func to prevent memory leak
	if cancel, ok := m.cancels[id]; ok {
		cancel()
		delete(m.cancels, id)
	}

	if m.store != nil {
		if snapErr := m.store.SnapshotJob(j); snapErr != nil {
			fmt.Fprintf(os.Stderr, "session: CompleteJob: SnapshotJob failed for job %s (durability warning): %v\n", j.ID, snapErr)
		}
	}

	return true
}

// FailJob transitions a job to failed state.
func (m *JobManager) FailJob(id string, err *types.TypedError) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failJobLocked(id, err, false)
}

// FailJobIfActive transitions a job to failed only when it is still active.
// Active states are created/running/completing. Completed/failed jobs are left untouched.
func (m *JobManager) FailJobIfActive(id string, err *types.TypedError) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failJobLocked(id, err, true)
}

func (m *JobManager) failJobLocked(id string, err *types.TypedError, activeOnly bool) bool {
	j, ok := m.jobs[id]
	if !ok {
		return false
	}
	if activeOnly {
		if j.Status != types.JobStatusCreated && j.Status != types.JobStatusRunning && j.Status != types.JobStatusCompleting {
			return false
		}
	}
	now := time.Now()
	j.Status = types.JobStatusFailed
	j.Error = err
	j.PID = 0
	j.CompletedAt = &now

	// Clean up cancel func to prevent memory leak
	if cancel, ok := m.cancels[id]; ok {
		cancel()
		delete(m.cancels, id)
	}

	if m.store != nil {
		if snapErr := m.store.SnapshotJob(j); snapErr != nil {
			fmt.Fprintf(os.Stderr, "session: failJobLocked: SnapshotJob failed for job %s (durability warning): %v\n", j.ID, snapErr)
		}
	}

	return true
}

// IncrementPoll increments the poll counter for anti-polling detection.
func (m *JobManager) IncrementPoll(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok {
		return 0
	}
	j.PollCount++
	return j.PollCount
}

// SetPheromone sets a pheromone marker on a job.
func (m *JobManager) SetPheromone(id, key, value string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok {
		return false
	}
	j.Pheromones[key] = value
	return true
}

// ListBySession returns live job pointers for a given session.
// Callers that only need to read must prefer ListBySessionSnapshot to avoid data races.
func (m *JobManager) ListBySession(sessionID string) []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Job
	for _, j := range m.jobs {
		if j.SessionID == sessionID {
			result = append(result, j)
		}
	}
	return result
}

// ListBySessionSnapshot returns deep-copied job snapshots for a given session.
// Returned jobs are detached from internal mutable state and safe for read-only use.
func (m *JobManager) ListBySessionSnapshot(sessionID string) []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Job
	for _, j := range m.jobs {
		if j.SessionID == sessionID {
			result = append(result, cloneJob(j))
		}
	}
	return result
}

// ListNonTerminal returns all jobs that are not yet in a terminal state
// (i.e. Created, Running, or Completing). Used by SnapshotAll to ensure
// in-flight jobs survive process restarts.
func (m *JobManager) ListNonTerminal() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Job
	for _, j := range m.jobs {
		if j.Status != types.JobStatusCompleted && j.Status != types.JobStatusFailed {
			result = append(result, j)
		}
	}
	return result
}

// ListRunning returns all jobs in running state.
func (m *JobManager) ListRunning() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Job
	for _, j := range m.jobs {
		if j.Status == types.JobStatusRunning {
			result = append(result, j)
		}
	}
	return result
}

// CountsBySession returns a map of sessionID → job count for all tracked jobs.
// Acquires the read lock once (O(J) total) rather than once per session,
// avoiding the N+1 pattern in session list handlers.
func (m *JobManager) CountsBySession() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]int, len(m.jobs))
	for _, j := range m.jobs {
		result[j.SessionID]++
	}
	return result
}

// CountRunning returns the number of jobs in running state.
func (m *JobManager) CountRunning() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, j := range m.jobs {
		if j.Status == types.JobStatusRunning {
			count++
		}
	}
	return count
}

// RegisterCancel stores a CancelFunc for an async job.
// Called when launching a background goroutine with context.WithCancel.
func (m *JobManager) RegisterCancel(id string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancels[id] = cancel
}

// CancelJob calls the stored CancelFunc for a job and marks it as failed.
// Returns true if the job was found and cancelled.
func (m *JobManager) CancelJob(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	cancel, ok := m.cancels[id]
	if ok {
		cancel()
		delete(m.cancels, id)
	}

	j, exists := m.jobs[id]
	if !exists {
		return false
	}
	if j.Status == types.JobStatusRunning || j.Status == types.JobStatusCreated {
		now := time.Now()
		j.Status = types.JobStatusFailed
		j.Error = types.NewExecutorError("job cancelled", nil, j.Content)
		j.CompletedAt = &now
	}
	if m.store != nil {
		if snapErr := m.store.SnapshotJob(j); snapErr != nil {
			fmt.Fprintf(os.Stderr, "session: CancelJob: SnapshotJob failed for job %s (durability warning): %v\n", id, snapErr)
		}
	}
	return true
}

// Delete removes a job.
func (m *JobManager) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.jobs[id]; !ok {
		return false
	}
	delete(m.jobs, id)
	return true
}
