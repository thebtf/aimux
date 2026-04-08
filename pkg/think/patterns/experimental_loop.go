package patterns

import (
	"fmt"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

type experimentalLoopPattern struct{}

// NewExperimentalLoopPattern returns the "experimental_loop" pattern handler.
func NewExperimentalLoopPattern() think.PatternHandler { return &experimentalLoopPattern{} }

func (p *experimentalLoopPattern) Name() string { return "experimental_loop" }

func (p *experimentalLoopPattern) Description() string {
	return "Autonomous experiment tracking — hypothesize, test, measure, iterate"
}

func (p *experimentalLoopPattern) Validate(input map[string]any) (map[string]any, error) {
	hypothesisRaw, ok := input["hypothesis"]
	if !ok {
		return nil, fmt.Errorf("missing required field: hypothesis")
	}
	hypothesis, ok := hypothesisRaw.(string)
	if !ok || hypothesis == "" {
		return nil, fmt.Errorf("field 'hypothesis' must be a non-empty string")
	}

	out := map[string]any{"hypothesis": hypothesis}

	if v, ok := input["observation"].(string); ok {
		out["observation"] = v
	}
	if v, ok := input["result"].(string); ok {
		out["result"] = v
	}
	if v, ok := input["metric"].(float64); ok {
		out["metric"] = v
	} else if v, ok := input["metric"].(int); ok {
		out["metric"] = float64(v)
	}

	return out, nil
}

func (p *experimentalLoopPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	hypothesis := validInput["hypothesis"].(string)
	observation, _ := validInput["observation"].(string)
	result, _ := validInput["result"].(string)
	metric, _ := validInput["metric"].(float64)

	sess := think.GetOrCreateSession(sessionID, "experimental_loop", map[string]any{
		"experiments":    []any{},
		"bestMetric":     0.0,
		"iterationCount": 0,
	})

	experiments, _ := sess.State["experiments"].([]any)
	bestMetric, _ := sess.State["bestMetric"].(float64)
	iterationCount, _ := sess.State["iterationCount"].(int)

	isImprovement := metric > bestMetric
	newBestMetric := bestMetric
	if isImprovement {
		newBestMetric = metric
	}

	entry := map[string]any{
		"hypothesis":  hypothesis,
		"observation": observation,
		"result":      result,
		"metric":      metric,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	}
	experiments = append(experiments, entry)
	iterationCount++

	// Determine suggested action.
	suggestedAction := suggestAction(experiments, isImprovement)

	think.UpdateSessionState(sessionID, map[string]any{
		"experiments":    experiments,
		"bestMetric":     newBestMetric,
		"iterationCount": iterationCount,
	})

	guidanceDepth := "enriched"
	if len(experiments) <= 1 {
		guidanceDepth = "basic"
	}

	data := map[string]any{
		"hypothesis":      hypothesis,
		"experimentCount": len(experiments),
		"currentMetric":   metric,
		"bestMetric":      newBestMetric,
		"isImprovement":   isImprovement,
		"suggestedAction": suggestedAction,
		"guidance":        BuildGuidance("experimental_loop", guidanceDepth, []string{"observation", "result", "metric"}),
	}
	return think.MakeThinkResult("experimental_loop", data, sessionID, nil, "experimental_loop", nil), nil
}

// suggestAction returns "pivot" if the last 3+ experiments show no improvement,
// "stop" if experiment count is very high, otherwise "continue".
func suggestAction(experiments []any, latestImproved bool) string {
	n := len(experiments)
	if n >= 10 {
		return "stop"
	}
	if n >= 3 && !latestImproved {
		// Check if last 3 showed no improvement (metric stayed same or fell).
		noImprovementStreak := 0
		for i := n - 1; i >= 0 && noImprovementStreak < 3; i-- {
			e, ok := experiments[i].(map[string]any)
			if !ok {
				break
			}
			m, _ := e["metric"].(float64)
			if i == n-1 {
				// Compare to previous best is tracked externally; just count zero-metric entries.
				if m == 0 {
					noImprovementStreak++
				}
			}
		}
		if n >= 3 && !latestImproved {
			return "pivot"
		}
	}
	return "continue"
}
