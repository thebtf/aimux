// Package types defines all shared types, interfaces, and errors for aimux v3.
package types

// SessionMode determines process lifecycle behavior.
type SessionMode string

const (
	// SessionModeLive keeps a persistent process alive across multiple turns.
	SessionModeLive SessionMode = "live"
	// SessionModeOnceStateful spawns, runs one turn, exits, and can resume later.
	SessionModeOnceStateful SessionMode = "once_stateful"
	// SessionModeOnceStateless spawns, runs, exits with no resume capability.
	SessionModeOnceStateless SessionMode = "once_stateless"
)

// JobStatus is the state machine for async jobs.
type JobStatus string

const (
	JobStatusCreated    JobStatus = "created"
	JobStatusRunning    JobStatus = "running"
	JobStatusCompleting JobStatus = "completing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

// SessionStatus tracks session lifecycle.
type SessionStatus string

const (
	SessionStatusCreated   SessionStatus = "created"
	SessionStatusRunning   SessionStatus = "running"
	SessionStatusCompleted SessionStatus = "completed"
	SessionStatusFailed    SessionStatus = "failed"
	SessionStatusExpired   SessionStatus = "expired"
)

// EventType classifies streaming output chunks.
type EventType string

const (
	EventTypeContent  EventType = "content"
	EventTypeProgress EventType = "progress"
	EventTypeComplete EventType = "complete"
	EventTypeError    EventType = "error"
)

// Event represents a single streaming output chunk from a CLI process.
type Event struct {
	Type    EventType `json:"type"`
	Content string    `json:"content,omitempty"`
	Error   error     `json:"-"`
}

// SpawnArgs holds all arguments for spawning a CLI process.
type SpawnArgs struct {
	CLI               string            `json:"cli"`
	Command           string            `json:"command"`
	Args              []string          `json:"args"`
	CWD               string            `json:"cwd"`
	Env               map[string]string `json:"env,omitempty"`
	Stdin             string            `json:"stdin,omitempty"`
	TimeoutSeconds    int               `json:"timeout_seconds,omitempty"`
	InactivitySeconds int               `json:"inactivity_seconds,omitempty"`
	CompletionPattern string            `json:"completion_pattern,omitempty"`
}

// Result is the structured output from a CLI execution.
type Result struct {
	SessionID    string      `json:"session_id"`
	CLISessionID string      `json:"cli_session_id,omitempty"`
	Content      string      `json:"content"`
	ExitCode     int         `json:"exit_code"`
	Error        *TypedError `json:"error,omitempty"`
	Partial      bool        `json:"partial"`
	DurationMS   int64       `json:"duration_ms"`
}

// ReviewVerdict classifies per-hunk review outcomes in pair coding.
type ReviewVerdict string

const (
	ReviewApproved         ReviewVerdict = "approved"
	ReviewModified         ReviewVerdict = "modified"
	ReviewChangesRequested ReviewVerdict = "changes_requested"
)

// HunkReview is the review result for a single unified diff hunk.
type HunkReview struct {
	HunkIndex int           `json:"hunk_index"`
	Verdict   ReviewVerdict `json:"verdict"`
	Comment   string        `json:"comment,omitempty"`
	Modified  string        `json:"modified,omitempty"`
}

// ReviewReport summarizes pair coding results.
type ReviewReport struct {
	DriverCLI   string       `json:"driver_cli"`
	ReviewerCLI string       `json:"reviewer_cli"`
	HunkReviews []HunkReview `json:"hunk_reviews"`
	Approved    int          `json:"approved"`
	Modified    int          `json:"modified"`
	Rejected    int          `json:"rejected"`
	Rounds      int          `json:"rounds"`
}

// PipelineStats records timing for audit pipeline phases.
type PipelineStats struct {
	ScanDurationMS        int64 `json:"scan_duration_ms"`
	ValidateDurationMS    int64 `json:"validate_duration_ms,omitempty"`
	InvestigateDurationMS int64 `json:"investigate_duration_ms,omitempty"`
	TotalDurationMS       int64 `json:"total_duration_ms"`
}

// AuditMode controls audit pipeline depth.
type AuditMode string

const (
	AuditModeQuick    AuditMode = "quick"
	AuditModeStandard AuditMode = "standard"
	AuditModeDeep     AuditMode = "deep"
)

// AuditConfidence grades finding validation status.
type AuditConfidence string

const (
	AuditConfidenceVerified      AuditConfidence = "verified"
	AuditConfidenceConfirmed     AuditConfidence = "confirmed"
	AuditConfidenceUnconfirmed   AuditConfidence = "unconfirmed"
	AuditConfidenceFalsePositive AuditConfidence = "false_positive"
)

// RolePreference maps a role to its preferred CLI, model, and reasoning effort.
type RolePreference struct {
	CLI             string `yaml:"cli" json:"cli"`
	Model           string `yaml:"model,omitempty" json:"model,omitempty"`
	ReasoningEffort string `yaml:"reasoning_effort,omitempty" json:"reasoning_effort,omitempty"`
}

// CLIFeatures describes what capabilities a CLI supports.
type CLIFeatures struct {
	Streaming     bool `yaml:"streaming" json:"streaming"`
	Headless      bool `yaml:"headless" json:"headless"`
	ReadOnly      bool `yaml:"read_only" json:"read_only"`
	SessionResume bool `yaml:"session_resume" json:"session_resume"`
	JSON          bool `yaml:"json" json:"json"`
	JSONL         bool `yaml:"jsonl" json:"jsonl"`
	StdinPipe     bool `yaml:"stdin_pipe" json:"stdin_pipe"`
}
