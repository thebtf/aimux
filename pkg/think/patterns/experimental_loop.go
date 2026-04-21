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

func (p *experimentalLoopPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"hypothesis":  {Type: "string", Required: true, Description: "The hypothesis to test in this iteration"},
		"observation": {Type: "string", Required: false, Description: "Observation from this iteration"},
		"result":      {Type: "string", Required: false, Description: "Result of the experiment"},
		"metric":      {Type: "number", Required: false, Description: "Numeric metric for this iteration"},
	}
}

func (p *experimentalLoopPattern) Category() string { return "solo" }

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
	// Tier 2A: text analysis (added on every call for stateful pattern)
	primaryText := validInput["hypothesis"].(string)
	if analysis := AnalyzeText(primaryText); analysis != nil {
		domain := MatchDomainTemplate(primaryText)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("experimental_loop", data, sessionID, nil, "experimental_loop", nil), nil
}

// suggestAction returns "pivot" if the last 3+ consecutive experiments each show
// no improvement over the running max metric, "stop" if the experiment count is
// very high, otherwise "continue".
func suggestAction(experiments []any, _ bool) string {
	n := len(experiments)
	if n >= 10 {
		return "stop"
	}

	if n >= 3 {
		// Walk experiments in order to compute the running max at each position,
		// then count how many trailing experiments failed to beat it.
		runningMax := 0.0
		noImprovementStreak := 0
		for _, raw := range experiments {
			e, ok := raw.(map[string]any)
			if !ok {
				noImprovementStreak = 0
				continue
			}
			m, _ := e["metric"].(float64)
			if m > runningMax {
				runningMax = m
				noImprovementStreak = 0
			} else {
				noImprovementStreak++
			}
		}
		if noImprovementStreak >= 3 {
			return "pivot"
		}
	}

	return "continue"
}
