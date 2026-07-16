package picker

import (
	"context"
	"math"

	"github.com/thebtf/aimux/pkg/types"
)

// TaskSpec describes a task submitted for CLI routing.
type TaskSpec struct {
	// TaskClass is the semantic category of the task (e.g., "code", "review",
	// "research", "task", "write-task"). Used by CapabilityScore to rank CLIs.
	TaskClass string

	// Prompt is the raw task prompt. The picker does not modify or inspect it —
	// it is passed through unchanged to the selected CLI worker (ADR-005).
	Prompt string

	// CWD is the project/worktree directory where a leaf CLI must execute.
	CWD string

	// Env carries project/session environment variables to leaf CLI dispatch.
	Env map[string]string

	// Model optionally overrides the selected CLI profile's default model.
	Model string

	// Effort optionally sets the selected CLI's reasoning effort flag.
	Effort string

	// Sandbox carries the requested code sandbox mode to leaf CLI dispatch.
	Sandbox string

	// SessionID carries the CLI-native session/thread id when resuming a task.
	SessionID string

	// SessionResume requests resume-mode command rendering for the CLI.
	SessionResume bool

	// TimeoutSeconds optionally overrides the selected CLI profile timeout for
	// this task dispatch.
	TimeoutSeconds int

	// OnOutput, when non-nil, is invoked once per stdout line as the leaf CLI
	// produces it (live progress). The task dispatch path wires this into
	// SpawnArgs.OnOutput so the IOManager delivers each line in real time —
	// used to forward progress into loom.AppendProgress so the stall detector's
	// ProgressUpdatedAt reflects genuine last-output time rather than staying nil.
	// It must be safe for concurrent use and should return quickly; a slow sink
	// blocks the streaming loop.
	OnOutput func(line string)

	// EventSink is the internal bounded raw-event admission path used by the
	// production WorkerRuntime. It is never interpreted by the picker.
	EventSink types.ExecutorEventSink

	// TaskID is the durable Loom task identifier for this dispatch attempt.
	// Internal-only: never part of a public MCP request/response schema.
	// CR-003's server-owned binding coordinator uses it to reserve an exact
	// durable Run Binding before any Swarm acquisition/execution.
	TaskID string

	// TenantID is the owning tenant for this task, taken from the durable
	// Loom task record rather than the execution context — Loom dispatches
	// worker.Execute on a context.Background()-derived context that never
	// carries the original request's tenant (see loom.go dispatch). Internal
	// only; never part of a public MCP request/response schema.
	TenantID string

	// ProjectID is the owning project for this task, taken from the durable
	// Loom task record. Internal-only: never part of a public MCP
	// request/response schema.
	ProjectID string

	// WorkerSessionID is the durable Loom Worker Session selected for this
	// provider attempt. Internal-only: it is populated by task workers from
	// durable metadata and never exposed on the public task/review schema.
	WorkerSessionID string

	// ParentWorkerSessionID is the exact durable parent used by fork mode.
	// It stays separate from the provider-neutral live Parent identity below.
	ParentWorkerSessionID string

	// SessionBinding carries the selected provider-neutral live binding mode
	// and exact expected/parent identity through picker and fallback copies.
	// Its zero value preserves the current stateless public default.
	SessionBinding types.SessionBindingRequest
}

// Picker selects the optimal CLI for a TaskSpec when the caller does not
// specify one explicitly. It applies config overrides, health filtering, and
// capability scoring in that priority order (architecture.md §5).
//
// Picker is goroutine-safe after construction.
type Picker struct {
	cfg        *PickerConfig
	score      *CapabilityScore
	health     *HealthChecker
	activeCLIs []string // ordered list of CLIs to consider; tie-break: first entry wins
}

// NewPicker constructs a Picker. All fields are required (non-nil).
//
//   - cfg: picker configuration (overrides, disabled list, etc.)
//   - score: capability score table
//   - health: health checker with pre-warmed cache
//   - activeCLIs: ordered list of CLI names to consider (e.g., ["codex","claude","gemini"])
//     The order is used as a tie-break when two CLIs have equal scores (first wins).
func NewPicker(cfg *PickerConfig, score *CapabilityScore, health *HealthChecker, activeCLIs []string) *Picker {
	if cfg == nil || score == nil || health == nil {
		panic("picker: cfg, score, and health must not be nil")
	}
	return &Picker{
		cfg:        cfg,
		score:      score,
		health:     health,
		activeCLIs: activeCLIs,
	}
}

// Pick selects the best CLI for the given TaskSpec. It follows the 4-step
// decision flow from architecture.md §5:
//
//  1. Config override: if DefaultCLI or PreferCLI[TaskClass] is set and healthy → return it.
//  2. Health filter: collect healthy, non-disabled CLIs. If none → ErrNoHealthyCLI.
//  3. Capability score: score each healthy CLI for TaskSpec.TaskClass.
//  4. Return highest score. Tie-break: first entry in activeCLIs wins (typically codex).
func (p *Picker) Pick(_ context.Context, spec TaskSpec) (string, error) {
	// Step 1: config override.
	if cli := p.preferredCLI(spec.TaskClass); cli != "" {
		if contains(p.activeCLIs, cli) && !p.cfg.isDisabled(cli) && p.health.IsHealthy(cli) {
			return cli, nil
		}
		// Config override CLI is not active, disabled, or unhealthy — fall through to scored selection.
	}

	// Step 2: health filter across active CLIs.
	healthy, reasons := p.filterHealthy()
	if len(healthy) == 0 {
		return "", &ErrNoHealthyCLI{Reasons: reasons}
	}

	// Step 3 + 4: score and pick highest; tie-break by activeCLIs order.
	// healthy preserves activeCLIs order (filterHealthy iterates activeCLIs),
	// so iterating healthy directly gives O(N) and correct tie-break semantics.
	best := ""
	bestScore := math.MinInt

	for _, cli := range healthy {
		s := p.score.Score(cli, spec.TaskClass)
		if s > bestScore {
			bestScore = s
			best = cli
		}
	}

	return best, nil
}

// preferredCLI returns the config-preferred CLI for the given task class,
// checking PreferCLI first, then DefaultCLI. Returns "" if none is configured.
func (p *Picker) preferredCLI(taskClass string) string {
	if p.cfg.PreferCLI != nil {
		if cli, ok := p.cfg.PreferCLI[taskClass]; ok && cli != "" {
			return cli
		}
	}
	return p.cfg.DefaultCLI
}

// filterHealthy returns the subset of activeCLIs that are not disabled and are healthy,
// plus failure reasons for those that were rejected.
func (p *Picker) filterHealthy() ([]string, []CLIFailureReason) {
	var healthy []string
	var reasons []CLIFailureReason

	for _, cli := range p.activeCLIs {
		if p.cfg.isDisabled(cli) {
			reasons = append(reasons, CLIFailureReason{CLI: cli, Reason: "disabled by configuration"})
			continue
		}
		isOK, reason := p.health.isHealthyWithReason(cli)
		if !isOK {
			if reason == "" {
				reason = "health check failed"
			}
			reasons = append(reasons, CLIFailureReason{CLI: cli, Reason: reason})
			continue
		}
		healthy = append(healthy, cli)
	}

	return healthy, reasons
}

// contains reports whether s is in the slice.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
