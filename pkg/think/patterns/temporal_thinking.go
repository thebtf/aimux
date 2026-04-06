package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

type temporalThinkingPattern struct{}

// NewTemporalThinkingPattern returns the "temporal_thinking" pattern handler.
func NewTemporalThinkingPattern() think.PatternHandler { return &temporalThinkingPattern{} }

func (p *temporalThinkingPattern) Name() string { return "temporal_thinking" }

func (p *temporalThinkingPattern) Description() string {
	return "Analyze temporal aspects: states, events, transitions, and constraints over time"
}

func (p *temporalThinkingPattern) Validate(input map[string]any) (map[string]any, error) {
	timeFrame, ok := input["timeFrame"]
	if !ok {
		return nil, fmt.Errorf("missing required field: timeFrame")
	}
	tf, ok := timeFrame.(string)
	if !ok || tf == "" {
		return nil, fmt.Errorf("field 'timeFrame' must be a non-empty string")
	}
	out := map[string]any{"timeFrame": tf}
	if v, ok := input["states"].([]any); ok {
		out["states"] = v
	}
	if v, ok := input["events"].([]any); ok {
		out["events"] = v
	}
	if v, ok := input["transitions"].([]any); ok {
		out["transitions"] = v
	}
	if v, ok := input["constraints"].([]any); ok {
		out["constraints"] = v
	}
	if v, ok := input["analysis"].(string); ok {
		out["analysis"] = v
	}
	return out, nil
}

func (p *temporalThinkingPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	timeFrame := validInput["timeFrame"].(string)

	countSlice := func(key string) int {
		if v, ok := validInput[key].([]any); ok {
			return len(v)
		}
		return 0
	}

	stateCount := countSlice("states")
	eventCount := countSlice("events")
	transitionCount := countSlice("transitions")
	constraintCount := countSlice("constraints")

	data := map[string]any{
		"timeFrame":       timeFrame,
		"stateCount":      stateCount,
		"eventCount":      eventCount,
		"transitionCount": transitionCount,
		"constraintCount": constraintCount,
		"totalComponents": stateCount + eventCount + transitionCount + constraintCount,
	}
	return think.MakeThinkResult("temporal_thinking", data, sessionID, nil, "", []string{"totalComponents"}), nil
}
