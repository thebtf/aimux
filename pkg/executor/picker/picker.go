package picker

import (
	"context"
	"math"
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
