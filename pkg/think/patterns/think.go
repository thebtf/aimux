package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

type thinkPattern struct{}

// NewThinkPattern returns the base "think" pattern handler.
func NewThinkPattern() think.PatternHandler { return &thinkPattern{} }

func (p *thinkPattern) Name() string { return "think" }

func (p *thinkPattern) Description() string {
	return "Base thinking pattern — records and reflects on a thought"
}

func (p *thinkPattern) Validate(input map[string]any) (map[string]any, error) {
	thought, ok := input["thought"]
	if !ok {
		return nil, fmt.Errorf("missing required field: thought")
	}
	s, ok := thought.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("field 'thought' must be a non-empty string")
	}
	return map[string]any{"thought": s}, nil
}

func (p *thinkPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	thought := validInput["thought"].(string)
	data := map[string]any{
		"thought":       thought,
		"thoughtLength": len(thought),
	}
	return think.MakeThinkResult("think", data, sessionID, nil, "", nil), nil
}
