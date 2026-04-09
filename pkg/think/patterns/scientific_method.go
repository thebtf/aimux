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

// validateEntryLink enforces lifecycle chain rules:
//   - prediction MUST link to a hypothesis entry
//   - experiment MUST link to a prediction entry
//   - result     MUST link to an experiment entry
func validateEntryLink(entryType, linkedTo string, entries []any) error {
	required := map[string]string{
		"prediction": "hypothesis",
		"experiment": "prediction",
		"result":     "experiment",
	}
	requiredTargetType, needsLink := required[entryType]
	if !needsLink {
		return nil
	}
	if linkedTo == "" {
		return fmt.Errorf("%s entry MUST include \"linkedTo\" pointing to a %s entry id", entryType, requiredTargetType)
	}
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if em["id"] == linkedTo {
			if em["type"] != requiredTargetType {
				return fmt.Errorf("%s entry \"linkedTo\" (%s) must reference a %s entry, got %v", entryType, linkedTo, requiredTargetType, em["type"])
			}
			return nil
		}
	}
	return fmt.Errorf("%s entry \"linkedTo\" references non-existent entry: %s", entryType, linkedTo)
}

// findUntestedHypotheses returns descriptions of hypothesis entries that have no linked prediction.
func findUntestedHypotheses(entries []any) []string {
	var result []string
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok || em["type"] != "hypothesis" {
			continue
		}
		id := em["id"].(string)
		linked := false
		for _, e2 := range entries {
			em2, ok := e2.(map[string]any)
			if ok && em2["type"] == "prediction" && em2["linkedTo"] == id {
				linked = true
				break
			}
		}
		if !linked {
			result = append(result, fmt.Sprintf("[%s] %s", id, em["text"]))
		}
	}
	return result
}

// findIncompleteExperiments returns descriptions of experiment entries that have no linked result.
func findIncompleteExperiments(entries []any) []string {
	var result []string
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok || em["type"] != "experiment" {
			continue
		}
		id := em["id"].(string)
		linked := false
		for _, e2 := range entries {
			em2, ok := e2.(map[string]any)
			if ok && em2["type"] == "result" && em2["linkedTo"] == id {
				linked = true
				break
			}
		}
		if !linked {
			result = append(result, fmt.Sprintf("[%s] %s", id, em["text"]))
		}
	}
	return result
}

// countByType returns a count of entries per entry type.
func countByType(entries []any) map[string]int {
	counts := map[string]int{"hypothesis": 0, "prediction": 0, "experiment": 0, "result": 0}
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := em["type"].(string); ok {
			counts[t]++
		}
	}
	return counts
}

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

		// Enforce lifecycle chain linking rules
		linkedTo, _ := entry["linkedTo"].(string)
		if err := validateEntryLink(entry["type"].(string), linkedTo, entries); err != nil {
			return nil, err
		}
		// For plain hypothesis entries, still verify linkedTo references an existing entry if provided
		if entry["type"] == "hypothesis" && linkedTo != "" {
			found := false
			for _, e := range entries {
				if em, ok := e.(map[string]any); ok && em["id"] == linkedTo {
					found = true
					break
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

	untestedHypotheses := findUntestedHypotheses(entries)
	incompleteExperiments := findIncompleteExperiments(entries)
	entryCount := countByType(entries)

	guidanceDepth := "enriched"
	if len(entries) == 0 {
		guidanceDepth = "basic"
	}

	data := map[string]any{
		"stage":                 stage,
		"stageHistoryLen":       len(stageHistory),
		"entriesCount":          len(entries),
		"hypothesesCount":       len(hypothesesHistory),
		"untestedHypotheses":    untestedHypotheses,
		"incompleteExperiments": incompleteExperiments,
		"entryCount":            entryCount,
		"guidance":              BuildGuidance("scientific_method", guidanceDepth, []string{"entry", "hypothesis", "observation", "question", "analysis", "conclusion"}),
	}
	if addedEntry != nil {
		data["entry"] = addedEntry
	}

	suggestedNext := nextStage(stage)

	// Tier 2A: text analysis (added on every call for stateful pattern)
	// Primary text: observation if provided, else hypothesis, else stage name.
	primaryText := stage
	if obs, ok := validInput["observation"].(string); ok && obs != "" {
		primaryText = obs
	} else if hyp, ok := validInput["hypothesis"].(string); ok && hyp != "" {
		primaryText = hyp
	}
	if analysis := AnalyzeText(primaryText); analysis != nil {
		domain := MatchDomainTemplate(primaryText)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("scientific_method", data, sessionID, nil, suggestedNext, nil), nil
}

func nextStage(current string) string {
	if current == stageOrder[len(stageOrder)-1] {
		return ""
	}
	return "scientific_method"
}
