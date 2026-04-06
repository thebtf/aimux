package patterns

import (
	"fmt"
	"strings"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

const duplicateThoughtThreshold = 0.8 // Jaccard similarity above this triggers warning

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

	// Detect duplicate/similar thoughts
	currentThought, _ := validInput["thought"].(string)
	var similarTo string
	var similarity float64
	for _, existing := range thoughts {
		if m, ok := existing.(map[string]any); ok {
			if prev, ok := m["thought"].(string); ok {
				sim := jaccardSimilarity(prev, currentThought)
				if sim > similarity {
					similarity = sim
					similarTo = prev
				}
			}
		}
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

	if similarity >= duplicateThoughtThreshold {
		data["duplicateWarning"] = fmt.Sprintf(
			"This thought is %.0f%% similar to an existing thought: %q. Consider revising instead.",
			similarity*100, similarTo,
		)
		data["similarity"] = similarity
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

// jaccardSimilarity computes the Jaccard similarity between two strings
// by splitting them into word sets and computing |intersection|/|union|.
func jaccardSimilarity(a, b string) float64 {
	setA := wordSet(a)
	setB := wordSet(b)

	if len(setA) == 0 && len(setB) == 0 {
		return 1.0
	}

	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}

	union := len(setA)
	for w := range setB {
		if !setA[w] {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}

func wordSet(s string) map[string]bool {
	words := strings.Fields(strings.ToLower(s))
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}
