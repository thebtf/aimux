package patterns

import (
	"fmt"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

type sequentialThinkingPattern struct{}

// NewSequentialThinkingPattern returns the "sequential_thinking" pattern handler.
func NewSequentialThinkingPattern() think.PatternHandler { return &sequentialThinkingPattern{} }

func (p *sequentialThinkingPattern) Name() string { return "sequential_thinking" }

func (p *sequentialThinkingPattern) Description() string {
	return "Chain thoughts sequentially with branching and revision support"
}

func (p *sequentialThinkingPattern) Validate(input map[string]any) (map[string]any, error) {
	thought, ok := input["thought"]
	if !ok {
		return nil, fmt.Errorf("missing required field: thought")
	}
	s, ok := thought.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("field 'thought' must be a non-empty string")
	}

	validated := map[string]any{"thought": s}

	if v, ok := input["thoughtNumber"]; ok {
		n, ok := toInt(v)
		if !ok {
			return nil, fmt.Errorf("field 'thoughtNumber' must be an integer")
		}
		validated["thoughtNumber"] = n
	} else {
		validated["thoughtNumber"] = 1
	}

	if v, ok := input["totalThoughts"]; ok {
		n, ok := toInt(v)
		if !ok {
			return nil, fmt.Errorf("field 'totalThoughts' must be an integer")
		}
		validated["totalThoughts"] = n
	} else {
		validated["totalThoughts"] = 1
	}

	if v, ok := input["isRevision"]; ok {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("field 'isRevision' must be a bool")
		}
		validated["isRevision"] = b
	} else {
		validated["isRevision"] = false
	}

	if v, ok := input["revisesThought"]; ok {
		n, ok := toInt(v)
		if !ok {
			return nil, fmt.Errorf("field 'revisesThought' must be an integer")
		}
		validated["revisesThought"] = n
	}

	if v, ok := input["branchFromThought"]; ok {
		n, ok := toInt(v)
		if !ok {
			return nil, fmt.Errorf("field 'branchFromThought' must be an integer")
		}
		validated["branchFromThought"] = n
	}

	if v, ok := input["branchId"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("field 'branchId' must be a string")
		}
		validated["branchId"] = s
	}

	return validated, nil
}

func (p *sequentialThinkingPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	sess := think.GetOrCreateSession(sessionID, "sequential_thinking", map[string]any{
		"thoughts": []any{},
		"branches": map[string]any{},
	})

	thoughts, _ := sess.State["thoughts"].([]any)
	branches, _ := sess.State["branches"].(map[string]any)
	if branches == nil {
		branches = map[string]any{}
	}

	thoughtNumber, _ := validInput["thoughtNumber"].(int)
	isRevision, _ := validInput["isRevision"].(bool)

	entry := map[string]any{
		"thoughtNumber":    thoughtNumber,
		"thought":          validInput["thought"],
		"isRevision":       isRevision,
		"revisesThought":   validInput["revisesThought"],
		"branchFromThought": validInput["branchFromThought"],
		"branchId":         validInput["branchId"],
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
	}

	branchId, _ := validInput["branchId"].(string)
	if branchId != "" {
		branches[branchId] = entry
	}

	thoughts = append(thoughts, entry)

	think.UpdateSessionState(sessionID, map[string]any{
		"thoughts": thoughts,
		"branches": branches,
	})

	hasBranches := len(branches) > 0

	data := map[string]any{
		"thoughtEntry":    entry,
		"totalInSession":  len(thoughts),
		"totalThoughts":   validInput["totalThoughts"],
		"hasBranches":     hasBranches,
	}

	return think.MakeThinkResult("sequential_thinking", data, sessionID, nil, "sequential_thinking", nil), nil
}

// toInt converts a value to int, handling float64 from JSON unmarshalling.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

