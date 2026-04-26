package types

import (
	"context"
	"time"
)

// Executor spawns and manages CLI processes.
// Three implementations: ConPTY (Windows), PTY (Linux/Mac), Pipe (fallback).
//
// Deprecated: Use ExecutorV2 for new code. This interface is preserved as
// LegacyExecutor and aliased for backward compatibility during M1→M8 migration.
// Will be removed in v5.0.0 when all callers migrate to ExecutorV2.
type Executor = LegacyExecutor

// LegacyExecutor is the original executor interface (v4.x).
// Kept for backward compatibility — all existing code continues to work unchanged.
type LegacyExecutor interface {
	// Run executes a single prompt and returns the result.
	Run(ctx context.Context, args SpawnArgs) (*Result, error)

	// Start begins a persistent session (LiveStateful mode).
	Start(ctx context.Context, args SpawnArgs) (Session, error)

	// Name returns the executor implementation name (conpty/pty/pipe).
	Name() string

	// Available checks if this executor can run on the current platform.
	Available() bool
}

// ExecutorV2 is the unified executor interface for aimux v5 agent engine.
// Both CLI binaries and HTTP APIs implement this contract. Higher layers
// (Swarm, Dialogue Controller, Workflows) interact only with ExecutorV2 —
// never with transport-specific details.
//
// Design: Decorator pattern — uniform Send(msg)→Response externally,
// backend-specific internals hidden. Zero-value fields for inapplicable
// aspects (CLI: TokensUsed={0,0}, API: ExitCode=0).
type ExecutorV2 interface {
	// Info returns metadata about this executor (name, type, capabilities).
	Info() ExecutorInfo

	// Send sends a message and waits for the complete response.
	Send(ctx context.Context, msg Message) (*Response, error)

	// SendStream sends a message and delivers incremental output via onChunk.
	// Returns the complete aggregated response after the final chunk.
	// Executors that don't support streaming call onChunk once with Done=true.
	SendStream(ctx context.Context, msg Message, onChunk func(Chunk)) (*Response, error)

	// IsAlive returns the current health status of this executor.
	IsAlive() HealthStatus

	// Close releases all resources held by this executor (process, connection, etc.).
	Close() error
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

// CooldownEntry is a snapshot of one active model cooldown entry.
type CooldownEntry struct {
	CLI           string    `json:"cli"`
	Model         string    `json:"model"`
	ExpiresAt     time.Time `json:"expires_at"`
	TriggerStderr string    `json:"trigger_stderr"`
}

// ModelCooldownTracker tracks rate-limited models to skip them during fallback.
// Passed to agent runner so it can participate in model fallback without
// depending on the executor package directly.
type ModelCooldownTracker interface {
	// MarkCooledDown records that a (cli, model) pair should not be used until
	// duration expires. triggerStderr is the redacted stderr excerpt that caused
	// this cooldown (empty string if unknown).
	MarkCooledDown(cli, model string, duration time.Duration, triggerStderr string)
	IsAvailable(cli, model string) bool
	FilterAvailable(cli string, models []string) []string
	// SetDuration stores a duration override for the next MarkCooledDown call
	// for the given (cli, model) pair. Not retroactive.
	SetDuration(cli, model string, duration time.Duration)
	// Flush removes the cooldown entry immediately.
	// Returns nil if removed, error if no entry found.
	Flush(cli, model string) error
	// List returns a snapshot of all non-expired cooldown entries.
	List() []CooldownEntry
}

// ModelledCLIResolver extends CLIResolver with model and effort overrides.
// Implementations that support per-agent model/effort selection implement this
// optional interface; callers check via type assertion.
type ModelledCLIResolver interface {
	CLIResolver
	ResolveSpawnArgsWithOpts(cli string, prompt string, model string, effort string) (SpawnArgs, error)
}

// StrategyParams is the input for orchestrator strategies.
// Model and Effort are optional overrides forwarded to profile-aware
// resolvers; empty values fall back to role defaults.
type StrategyParams struct {
	Prompt    string         `json:"prompt"`
	CLIs      []string       `json:"clis,omitempty"`
	Roles     []string       `json:"roles,omitempty"`
	CWD       string         `json:"cwd,omitempty"`
	MaxTurns  int            `json:"max_turns,omitempty"`
	Timeout   int            `json:"timeout,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Model     string         `json:"model,omitempty"`
	Effort    string         `json:"effort,omitempty"`
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
	// TurnHistory is JSON-encoded []TurnEntry for dialog session resume.
	// Populated by SequentialDialog after Execute completes.
	TurnHistory []byte `json:"turn_history,omitempty"`
}
