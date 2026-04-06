package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

type metacognitiveMonitoringPattern struct{}

// NewMetacognitiveMonitoringPattern returns the "metacognitive_monitoring" pattern handler.
func NewMetacognitiveMonitoringPattern() think.PatternHandler {
	return &metacognitiveMonitoringPattern{}
}

func (p *metacognitiveMonitoringPattern) Name() string { return "metacognitive_monitoring" }

func (p *metacognitiveMonitoringPattern) Description() string {
	return "Monitor cognitive processes for overconfidence and blind spots"
}

func (p *metacognitiveMonitoringPattern) Validate(input map[string]any) (map[string]any, error) {
	task, ok := input["task"]
	if !ok {
		return nil, fmt.Errorf("missing required field: task")
	}
	ts, ok := task.(string)
	if !ok || ts == "" {
		return nil, fmt.Errorf("field 'task' must be a non-empty string")
	}
	out := map[string]any{"task": ts}
	if v, ok := input["knowledgeAssessment"].(string); ok {
		out["knowledgeAssessment"] = v
	}
	if v, ok := input["claims"].([]any); ok {
		out["claims"] = v
	}
	if v, ok := input["cognitiveProcesses"].([]any); ok {
		out["cognitiveProcesses"] = v
	}
	if v, ok := input["biases"].([]any); ok {
		out["biases"] = v
	}
	if v, ok := input["uncertainties"].([]any); ok {
		out["uncertainties"] = v
	}
	if v, err := toFloat64(input["confidence"]); err == nil {
		out["confidence"] = v
	}
	return out, nil
}

func (p *metacognitiveMonitoringPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	task := validInput["task"].(string)

	countSlice := func(key string) int {
		if v, ok := validInput[key].([]any); ok {
			return len(v)
		}
		return 0
	}

	claimsCount := countSlice("claims")
	biasesCount := countSlice("biases")
	uncertaintiesCount := countSlice("uncertainties")
	processesCount := countSlice("cognitiveProcesses")

	confidence := 0.0
	if v, ok := validInput["confidence"].(float64); ok {
		confidence = v
	}

	overconfidenceWarning := ""
	if confidence > 0.8 && claimsCount < 3 {
		overconfidenceWarning = fmt.Sprintf(
			"High confidence (%.2f) with few supporting claims (%d). Consider gathering more evidence.",
			confidence, claimsCount,
		)
	}

	data := map[string]any{
		"task":                    task,
		"claimsCount":            claimsCount,
		"biasesCount":            biasesCount,
		"uncertaintiesCount":     uncertaintiesCount,
		"cognitiveProcessCount":  processesCount,
		"confidence":             confidence,
		"overconfidenceWarning":  overconfidenceWarning,
	}
	return think.MakeThinkResult("metacognitive_monitoring", data, sessionID, nil, "", []string{"overconfidenceWarning"}), nil
}
