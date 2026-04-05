package session

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thebtf/aimux/pkg/types"
)

// Job represents an async execution task.
type Job struct {
	ID              string              `json:"id"`
	SessionID       string              `json:"session_id"`
	CLI             string              `json:"cli"`
	Status          types.JobStatus     `json:"status"`
	Progress        string              `json:"progress,omitempty"`
	Content         string              `json:"content,omitempty"`
	ExitCode        int                 `json:"exit_code"`
	Error           *types.TypedError   `json:"error,omitempty"`
	PollCount       int                 `json:"poll_count"`
	Pheromones      map[string]string   `json:"pheromones,omitempty"`
	Pipeline        *types.PipelineStats `json:"pipeline,omitempty"`
	PID             int                 `json:"pid"`
	CreatedAt       time.Time           `json:"created_at"`
	ProgressUpdatedAt time.Time         `json:"progress_updated_at"`
	CompletedAt     *time.Time          `json:"completed_at,omitempty"`
}

// JobManager manages async jobs with state machine transitions.
type JobManager struct {
	jobs map[string]*Job
	mu   sync.RWMutex
}

// NewJobManager creates an empty job manager.
func NewJobManager() *JobManager {
	return &JobManager{
		jobs: make(map[string]*Job),
	}
}

// Create registers a new job for a session.
func (m *JobManager) Create(sessionID, cli string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	j := &Job{
		ID:                uuid.Must(uuid.NewV7()).String(),
		SessionID:         sessionID,
		CLI:               cli,
		Status:            types.JobStatusCreated,
		Pheromones:        make(map[string]string),
		CreatedAt:         now,
		ProgressUpdatedAt: now,
	}

	m.jobs[j.ID] = j
	return j
}

// Import inserts a job from recovery (WAL replay). Thread-safe.
func (m *JobManager) Import(j *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = j
}

// Get returns a job by ID, or nil if not found.
func (m *JobManager) Get(id string) *Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobs[id]
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
	return true
}

// UpdateProgress updates the progress text for a running job.
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
	return true
}

// FailJob transitions a job to failed state.
func (m *JobManager) FailJob(id string, err *types.TypedError) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok {
		return false
	}
	now := time.Now()
	j.Status = types.JobStatusFailed
	j.Error = err
	j.PID = 0
	j.CompletedAt = &now
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

// ListBySession returns all jobs for a given session.
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
