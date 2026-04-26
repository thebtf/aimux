package patterns

import (
	"fmt"
	"strings"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

const (
	duplicateThoughtThreshold     = 0.8 // Jaccard similarity above this triggers duplicate warning
	contradictionSimilarityThresh = 0.6 // Jaccard similarity above this (with negation) triggers contradiction
)

// negationWords are words that, when present, signal a thought may contradict a prior one.
var negationWords = []string{
	"not", "wrong", "incorrect", "false", "disagree",
	"contrary", "opposite", "however", "but actually",
}

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

	// step_number: optional flat param for external step tracking (forwarded to output).
	if v, ok := input["step_number"]; ok {
		n, ok := toInt(v)
		if !ok {
			return nil, fmt.Errorf("field 'step_number' must be an integer")
		}
		validated["step_number"] = n
	}

	return validated, nil
}

func (p *sequentialThinkingPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"thought":           {Type: "string", Required: true, Description: "The thought content for this step"},
		"thoughtNumber":     {Type: "integer", Required: false, Description: "Current thought number (default 1)"},
		"totalThoughts":     {Type: "integer", Required: false, Description: "Total expected thoughts (default 1)"},
		"isRevision":        {Type: "boolean", Required: false, Description: "Whether this thought revises a prior one"},
		"revisesThought":    {Type: "integer", Required: false, Description: "Thought number being revised"},
		"branchFromThought": {Type: "integer", Required: false, Description: "Thought number to branch from"},
		"branchId":          {Type: "string", Required: false, Description: "Branch identifier"},
		"step_number":       {Type: "integer", Required: false, Description: "External step tracking number"},
	}
}

func (p *sequentialThinkingPattern) Category() string { return "solo" }

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
	totalThoughts, _ := validInput["totalThoughts"].(int)
	isRevision, _ := validInput["isRevision"].(bool)

	entry := map[string]any{
		"thoughtNumber":     thoughtNumber,
		"thought":           validInput["thought"],
		"isRevision":        isRevision,
		"revisesThought":    validInput["revisesThought"],
		"branchFromThought": validInput["branchFromThought"],
		"branchId":          validInput["branchId"],
		"timestamp":         time.Now().UTC().Format(time.RFC3339),
	}

	branchId, _ := validInput["branchId"].(string)
	_, hasBranchFrom := validInput["branchFromThought"]
	if branchId != "" && hasBranchFrom {
		existing, _ := branches[branchId].([]any)
		branches[branchId] = append(existing, entry)
	}

	// Scan prior thoughts for similarity (duplicate warning) and contradiction detection.
	currentThought, _ := validInput["thought"].(string)
	currentLower := strings.ToLower(currentThought)

	hasNegation := false
	for _, word := range negationWords {
		if strings.Contains(currentLower, word) {
			hasNegation = true
			break
		}
	}

	var (
		duplicateSimilarTo     string
		duplicateSimilarity    float64
		contradictionDetected  bool
		contradictsWith        int
		bestContradictionScore float64
	)

	for _, existing := range thoughts {
		m, ok := existing.(map[string]any)
		if !ok {
			continue
		}
		prev, ok := m["thought"].(string)
		if !ok {
			continue
		}
		sim := jaccardSimilarity(prev, currentThought)

		// Duplicate warning: very high similarity regardless of negation.
		if sim > duplicateSimilarity {
			duplicateSimilarity = sim
			duplicateSimilarTo = prev
		}

		// Contradiction: negation present + similarity above lower threshold.
		if hasNegation && sim > contradictionSimilarityThresh && sim > bestContradictionScore {
			bestContradictionScore = sim
			contradictionDetected = true
			if n, ok := m["thoughtNumber"].(int); ok {
				contradictsWith = n
			}
		}
	}

	thoughts = append(thoughts, entry)

	think.UpdateSessionState(sessionID, map[string]any{
		"thoughts": thoughts,
		"branches": branches,
	})

	hasBranches := len(branches) > 0
	stage := determineStage(thoughtNumber, totalThoughts)

	// suggestedNextPattern mirrors v2 behaviour: mental_model at the start,
	// decision_framework at the end, nothing in the middle.
	suggestedNext := "sequential_thinking"
	if thoughtNumber == 1 && totalThoughts > 1 {
		suggestedNext = "mental_model"
	} else if thoughtNumber == totalThoughts && totalThoughts > 1 {
		suggestedNext = "decision_framework"
	}

	guidanceDepth := "enriched"
	if len(thoughts) <= 1 {
		guidanceDepth = "basic"
	}

	// nextThoughtNeeded / needsMoreThoughts: pass through from validInput when present,
	// otherwise derive from position (matches TS v1 pass-through behaviour).
	nextThoughtNeeded, hasNTN := validInput["nextThoughtNeeded"]
	if !hasNTN {
		nextThoughtNeeded = thoughtNumber < totalThoughts
	}
	needsMoreThoughts, hasNMT := validInput["needsMoreThoughts"]
	if !hasNMT {
		needsMoreThoughts = false
	}

	data := map[string]any{
		// ── core ──────────────────────────────────────────────────────────────
		"thoughtEntry":   entry,
		"totalInSession": len(thoughts),
		// TS v1 alias for totalInSession
		"thoughtHistoryLength": len(thoughts),
		"totalThoughts":        validInput["totalThoughts"],
		// ── branch ────────────────────────────────────────────────────────────
		"hasBranches": hasBranches,
		"branchCount": len(branches),
		// ── stage / flow ──────────────────────────────────────────────────────
		"stage":             stage,
		"nextThoughtNeeded": nextThoughtNeeded,
		"needsMoreThoughts": needsMoreThoughts,
		// ── step tracking booleans (TS v1) ────────────────────────────────────
		"hasCurrentStep":    thoughtNumber > 0,
		"hasPreviousSteps":  thoughtNumber > 1,
		"hasRemainingSteps": thoughtNumber < totalThoughts,
		// ── contradiction ─────────────────────────────────────────────────────
		"contradictionDetected": contradictionDetected,
		"contradictsWith":       contradictsWith,
		// ── guidance ──────────────────────────────────────────────────────────
		"guidance": BuildGuidance("sequential_thinking", guidanceDepth, []string{"thoughtNumber", "totalThoughts", "isRevision", "revisesThought", "branchFromThought", "branchId"}),
	}

	if duplicateSimilarity >= duplicateThoughtThreshold {
		data["duplicateWarning"] = fmt.Sprintf(
			"This thought is %.0f%% similar to an existing thought: %q. Consider revising instead.",
			duplicateSimilarity*100, duplicateSimilarTo,
		)
		data["similarity"] = duplicateSimilarity
	}

	// Propagate optional flat param to output.
	if v, ok := validInput["step_number"]; ok {
		data["step_number"] = v
	}

	return think.MakeThinkResult("sequential_thinking", data, sessionID, nil, suggestedNext, nil), nil
}

// toInt converts a value to int, handling float64 from JSON unmarshalling.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		// Reject fractional values — silently truncating 1.9 → 1 would corrupt
		// ordinal fields like thoughtNumber, totalThoughts, revisesThought, etc.
		if n != float64(int(n)) {
			return 0, false
		}
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

// determineStage maps (thoughtNumber, totalThoughts) to a named stage,
// mirroring the v2 TypeScript implementation.
func determineStage(thoughtNumber, totalThoughts int) string {
	if totalThoughts <= 1 {
		return "final"
	}
	if thoughtNumber == 1 {
		return "initial"
	}
	if thoughtNumber == totalThoughts {
		return "final"
	}
	if totalThoughts == 2 {
		return "final" // only two thoughts: 1=initial (above), 2=final
	}
	progress := float64(thoughtNumber) / float64(totalThoughts)
	if progress <= 0.33 {
		return "initial"
	}
	if progress >= 0.67 {
		return "final"
	}
	return "middle"
}
