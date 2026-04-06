package types

import "context"

// Executor spawns and manages CLI processes.
// Three implementations: ConPTY (Windows), PTY (Linux/Mac), Pipe (fallback).
type Executor interface {
	// Run executes a single prompt and returns the result.
	Run(ctx context.Context, args SpawnArgs) (*Result, error)

	// Start begins a persistent session (LiveStateful mode).
	Start(ctx context.Context, args SpawnArgs) (Session, error)

	// Name returns the executor implementation name (conpty/pty/pipe).
	Name() string

	// Available checks if this executor can run on the current platform.
	Available() bool
}

// Session represents a persistent CLI process for multi-turn interaction.
type Session interface {
	// ID returns the session identifier.
	ID() string

	// Send sends a prompt and waits for the response.
	Send(ctx context.Context, prompt string) (*Result, error)

	// Stream sends a prompt and returns an event channel for streaming output.
	Stream(ctx context.Context, prompt string) (<-chan Event, error)

	// Close terminates the session and kills the underlying process.
	Close() error

	// Alive checks if the underlying process is still running.
	Alive() bool

	// PID returns the OS process ID.
	PID() int
}

// Strategy defines an orchestration pattern.
// Implementations: PairCoding, SequentialDialog, ParallelConsensus,
// StructuredDebate, AuditPipeline.
type Strategy interface {
	// Name returns the strategy identifier.
	Name() string

	// Execute runs the orchestration with given parameters.
	Execute(ctx context.Context, params StrategyParams) (*StrategyResult, error)
}

// CLIResolver resolves CLI spawn arguments from profile data.
// Passed to orchestrator strategy constructors for profile-aware command resolution.
// When nil, strategies fall back to legacy behavior (Command=cli, Args=["-p", prompt]).
type CLIResolver interface {
	ResolveSpawnArgs(cli string, prompt string) (SpawnArgs, error)
}

// StrategyParams is the input for orchestrator strategies.
type StrategyParams struct {
	Prompt    string         `json:"prompt"`
	CLIs      []string       `json:"clis,omitempty"`
	Roles     []string       `json:"roles,omitempty"`
	CWD       string         `json:"cwd,omitempty"`
	MaxTurns  int            `json:"max_turns,omitempty"`
	Timeout   int            `json:"timeout,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// StrategyResult is the output from orchestrator strategies.
type StrategyResult struct {
	Content      string         `json:"content"`
	Status       string         `json:"status"`
	Turns        int            `json:"turns"`
	Participants []string       `json:"participants,omitempty"`
	ReviewReport *ReviewReport  `json:"review_report,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}
