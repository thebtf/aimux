package patterns

import (
	"fmt"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

var (
	validStages = map[string]bool{
		"observation": true, "question": true, "hypothesis": true,
		"experiment": true, "analysis": true, "conclusion": true, "iteration": true,
	}
	validEntryTypes = map[string]bool{
		"hypothesis": true, "prediction": true, "experiment": true, "result": true,
	}
	stageOrder = []string{
		"observation", "question", "hypothesis", "experiment", "analysis", "conclusion", "iteration",
	}
)

type scientificMethodPattern struct{}

// NewScientificMethodPattern returns the "scientific_method" pattern handler.
func NewScientificMethodPattern() think.PatternHandler { return &scientificMethodPattern{} }

func (p *scientificMethodPattern) Name() string { return "scientific_method" }

func (p *scientificMethodPattern) Description() string {
	return "Guide reasoning through the scientific method stages with linked entries"
}

func (p *scientificMethodPattern) Validate(input map[string]any) (map[string]any, error) {
	stageRaw, ok := input["stage"]
	if !ok {
		return nil, fmt.Errorf("missing required field: stage")
	}
	stage, ok := stageRaw.(string)
	if !ok || !validStages[stage] {
		return nil, fmt.Errorf("field 'stage' must be one of: observation, question, hypothesis, experiment, analysis, conclusion, iteration")
	}

	validated := map[string]any{"stage": stage}

	for _, field := range []string{"observation", "question", "hypothesis", "experiment", "analysis", "conclusion"} {
		if v, ok := input[field]; ok {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("field '%s' must be a string", field)
			}
			validated[field] = s
		}
	}

	if v, ok := input["entry"]; ok {
		entry, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field 'entry' must be a map")
		}
		entryType, ok := entry["type"].(string)
		if !ok || !validEntryTypes[entryType] {
			return nil, fmt.Errorf("entry 'type' must be one of: hypothesis, prediction, experiment, result")
		}
		text, ok := entry["text"].(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("entry 'text' must be a non-empty string")
		}
		validatedEntry := map[string]any{"type": entryType, "text": text}
		if linkedTo, ok := entry["linkedTo"].(string); ok {
			validatedEntry["linkedTo"] = linkedTo
		}
		validated["entry"] = validatedEntry
	}

	return validated, nil
}

func (p *scientificMethodPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	sess := think.GetOrCreateSession(sessionID, "scientific_method", map[string]any{
		"stageHistory":      []any{},
		"hypothesesHistory": []any{},
		"entries":           []any{},
	})

	stageHistory, _ := sess.State["stageHistory"].([]any)
	hypothesesHistory, _ := sess.State["hypothesesHistory"].([]any)
	entries, _ := sess.State["entries"].([]any)

	stage := validInput["stage"].(string)
	stageHistory = append(stageHistory, stage)

	var addedEntry map[string]any
	if entryRaw, ok := validInput["entry"]; ok {
		entry := entryRaw.(map[string]any)

		// Validate linkedTo references an existing entry
		if linkedTo, ok := entry["linkedTo"].(string); ok && linkedTo != "" {
			found := false
			for _, e := range entries {
				if em, ok := e.(map[string]any); ok {
					if em["id"] == linkedTo {
						found = true
						break
					}
				}
			}
			if !found {
				return nil, fmt.Errorf("linkedTo references non-existent entry: %s", linkedTo)
			}
		}

		entryID := fmt.Sprintf("E-%d", len(entries)+1)
		addedEntry = map[string]any{
			"id":        entryID,
			"type":      entry["type"],
			"text":      entry["text"],
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
		if linkedTo, ok := entry["linkedTo"].(string); ok {
			addedEntry["linkedTo"] = linkedTo
		}
		entries = append(entries, addedEntry)
	}

	if h, ok := validInput["hypothesis"].(string); ok && h != "" {
		hypothesesHistory = append(hypothesesHistory, h)
	}

	think.UpdateSessionState(sessionID, map[string]any{
		"stageHistory":      stageHistory,
		"hypothesesHistory": hypothesesHistory,
		"entries":           entries,
	})

	data := map[string]any{
		"stage":            stage,
		"stageHistoryLen":  len(stageHistory),
		"entriesCount":     len(entries),
		"hypothesesCount":  len(hypothesesHistory),
	}
	if addedEntry != nil {
		data["entry"] = addedEntry
	}

	suggestedNext := nextStage(stage)

	return think.MakeThinkResult("scientific_method", data, sessionID, nil, suggestedNext, nil), nil
}

func nextStage(current string) string {
	for i, s := range stageOrder {
		if s == current && i+1 < len(stageOrder) {
			return "scientific_method"
		}
	}
	return "scientific_method"
}
