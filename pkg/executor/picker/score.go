// Package picker provides CLI selection logic for the executor layer.
//
// CapabilityScore exposes two APIs for CLI × task fitness:
//
//   - Score(cli, task) int    ∈ [0, 100]       — human-readable integer; used by Picker tie-break
//   - Scoref(cli, task) float64 ∈ [0.0, 1.0]  — normalized float; intended for downstream composite
//     scoring (e.g. AIMUX-4 Orderer composite formula). Scoref is strictly Score/100.0.
package picker

// defaultScores is the built-in capability score table from architecture.md §6.
// Scores range from 0 to 100. Higher = more capable for that task class.
// Unknown task classes default to 50 for all CLIs.
//
// Rationale (from architecture.md §6):
//   - codex: highest for code/review/write-task (purpose-built code model + sandbox enforcement)
//   - gemini: highest for research (large context, web grounding)
//   - claude: balanced generalist; strong for free-form tasks
var defaultScores = map[string]map[string]int{
	"codex": {
		"review":     90,
		"task":       80,
		"write-task": 85,
		"research":   40,
		"code":       95,
	},
	"claude": {
		"review":     70,
		"task":       85,
		"write-task": 70,
		"research":   80,
		"code":       80,
	},
	"gemini": {
		"review":     50,
		"task":       60,
		"write-task": 40,
		"research":   90,
		"code":       60,
	},
}

// defaultScoreForUnknown is returned when neither the config nor the built-in
// table has an entry for a given (CLI, task class) pair.
const defaultScoreForUnknown = 50

// CapabilityScore computes a 0–100 fitness score for a CLI × task class pair.
// It consults the PickerConfig.Scores override table first, then the built-in
// defaults, and falls back to defaultScoreForUnknown.
//
// CapabilityScore is stateless and goroutine-safe after construction.
type CapabilityScore struct {
	cfg *PickerConfig
}

// NewCapabilityScore constructs a CapabilityScore backed by the given config.
// cfg must not be nil.
func NewCapabilityScore(cfg *PickerConfig) *CapabilityScore {
	return &CapabilityScore{cfg: cfg}
}

// Score returns the fitness score for (cli, taskClass) as an integer in [0, 100].
// Priority: config override → built-in default → defaultScoreForUnknown.
func (cs *CapabilityScore) Score(cli, taskClass string) int {
	// Config override wins over built-in defaults.
	if cs.cfg.Scores != nil {
		if cliScores, ok := cs.cfg.Scores[cli]; ok {
			if score, ok := cliScores[taskClass]; ok {
				return score
			}
		}
	}

	// Built-in default table.
	if cliScores, ok := defaultScores[cli]; ok {
		if score, ok := cliScores[taskClass]; ok {
			return score
		}
	}

	// Unknown CLI or task class.
	return defaultScoreForUnknown
}

// Scoref returns the fitness score for (cli, taskClass) normalized to [0.0, 1.0].
// It is exactly float64(Score(cli, taskClass)) / 100.0.
//
// Range invariant: 0.0 ≤ Scoref(cli, taskClass) ≤ 1.0 for any valid Score input in [0, 100].
// Intended for downstream composite scoring formulas (e.g. AIMUX-4 Orderer).
func (cs *CapabilityScore) Scoref(cli, taskClass string) float64 {
	return float64(cs.Score(cli, taskClass)) / 100.0
}
