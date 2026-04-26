package patterns

import (
	"fmt"
	"math"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

var (
	validCollabStages = map[string]bool{
		"problem-definition": true, "ideation": true, "critique": true,
		"integration": true, "decision": true, "reflection": true,
	}
	validContributionTypes = map[string]bool{
		"observation": true, "question": true, "insight": true, "concern": true,
		"suggestion": true, "challenge": true, "synthesis": true,
	}
)

type collaborativeReasoningPattern struct{}

// NewCollaborativeReasoningPattern returns the "collaborative_reasoning" pattern handler.
func NewCollaborativeReasoningPattern() think.PatternHandler {
	return &collaborativeReasoningPattern{}
}

func (p *collaborativeReasoningPattern) Name() string { return "collaborative_reasoning" }

func (p *collaborativeReasoningPattern) Description() string {
	return "Multi-persona collaborative reasoning with stage tracking"
}

func (p *collaborativeReasoningPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"topic":             {Type: "string", Required: true, Description: "The topic to reason about collaboratively"},
		"stage":             {Type: "enum", Required: false, Description: "Current collaboration stage", EnumValues: []string{"problem-definition", "ideation", "critique", "integration", "decision", "reflection"}},
		"contribution_type": {Type: "enum", Required: false, Description: "Flat format: type of contribution", EnumValues: []string{"observation", "question", "insight", "concern", "suggestion", "challenge", "synthesis"}},
		"contribution_text": {Type: "string", Required: false, Description: "Flat format: text of the contribution"},
		"persona_id":        {Type: "string", Required: false, Description: "Flat format: persona making the contribution"},
		"personas":          {Type: "array", Required: false, Description: "List of participant personas for tracking", Items: map[string]any{"type": "string"}},
	}
}

func (p *collaborativeReasoningPattern) Category() string { return "solo" }

func (p *collaborativeReasoningPattern) Validate(input map[string]any) (map[string]any, error) {
	topicRaw, ok := input["topic"]
	if !ok {
		return nil, fmt.Errorf("missing required field: topic")
	}
	topic, ok := topicRaw.(string)
	if !ok || topic == "" {
		return nil, fmt.Errorf("field 'topic' must be a non-empty string")
	}

	validated := map[string]any{"topic": topic}

	if v, ok := input["stage"]; ok {
		stage, ok := v.(string)
		if !ok || !validCollabStages[stage] {
			return nil, fmt.Errorf("field 'stage' must be one of: problem-definition, ideation, critique, integration, decision, reflection")
		}
		validated["stage"] = stage
	}

	// Flat param detection: contribution_type present → build contribution from flat params.
	if _, hasFlat := input["contribution_type"]; hasFlat {
		contribType, ok := input["contribution_type"].(string)
		if !ok || !validContributionTypes[contribType] {
			return nil, fmt.Errorf("field 'contribution_type' must be one of: observation, question, insight, concern, suggestion, challenge, synthesis")
		}
		text, ok := input["contribution_text"].(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("field 'contribution_text' must be a non-empty string")
		}
		validatedContrib := map[string]any{"type": contribType, "text": text}
		if persona, ok := input["persona_id"].(string); ok && persona != "" {
			validatedContrib["persona"] = persona
		}
		if cv, ok := input["contribution_confidence"].(float64); ok {
			validatedContrib["confidence"] = cv
		}
		validated["contribution"] = validatedContrib
	} else if v, ok := input["contribution"]; ok {
		contrib, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field 'contribution' must be a map")
		}
		contribType, ok := contrib["type"].(string)
		if !ok || !validContributionTypes[contribType] {
			return nil, fmt.Errorf("contribution 'type' must be one of: observation, question, insight, concern, suggestion, challenge, synthesis")
		}
		text, ok := contrib["text"].(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("contribution 'text' must be a non-empty string")
		}
		validatedContrib := map[string]any{"type": contribType, "text": text}
		if persona, ok := contrib["persona"].(string); ok {
			validatedContrib["persona"] = persona
		}
		validated["contribution"] = validatedContrib
	}

	// Accept optional personas list for participation tracking.
	if v, ok := input["personas"]; ok {
		if personas, ok := v.([]any); ok {
			validated["personas"] = personas
		}
	}

	return validated, nil
}

func (p *collaborativeReasoningPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	sess := think.GetOrCreateSession(sessionID, "collaborative_reasoning", map[string]any{
		"contributions": []any{},
		"currentStage":  "problem-definition",
	})

	contributions, _ := sess.State["contributions"].([]any)
	currentStage, _ := sess.State["currentStage"].(string)
	if currentStage == "" {
		currentStage = "problem-definition"
	}

	if stage, ok := validInput["stage"].(string); ok {
		currentStage = stage
	}

	if contribRaw, ok := validInput["contribution"]; ok {
		contrib := contribRaw.(map[string]any)
		contribID := fmt.Sprintf("C-%d", len(contributions)+1)
		entry := map[string]any{
			"id":        contribID,
			"type":      contrib["type"],
			"text":      contrib["text"],
			"stage":     currentStage,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
		if persona, ok := contrib["persona"].(string); ok {
			entry["persona"] = persona
		}
		contributions = append(contributions, entry)
	}

	think.UpdateSessionState(sessionID, map[string]any{
		"contributions": contributions,
		"currentStage":  currentStage,
	})

	// Compute stage progress: which stages have contributions
	stageProgress := map[string]int{}
	for _, cRaw := range contributions {
		if c, ok := cRaw.(map[string]any); ok {
			if s, ok := c["stage"].(string); ok {
				stageProgress[s]++
			}
		}
	}

	// Extract personas list if provided (enables silent persona detection).
	var knownPersonas []string
	if personas, ok := validInput["personas"].([]any); ok {
		for _, p := range personas {
			if s, ok := p.(string); ok {
				knownPersonas = append(knownPersonas, s)
			}
		}
	}
	participation := computeParticipation(contributions, knownPersonas)

	topic := validInput["topic"].(string)

	// TS v1 parity: stagesCompleted — array of stage keys that have at least one contribution.
	stagesCompleted := make([]string, 0, len(stageProgress))
	for s := range stageProgress {
		stagesCompleted = append(stagesCompleted, s)
	}

	// TS v1 parity: has* type-based booleans — contribution types present in session.
	typesSeen := map[string]bool{}
	for _, cRaw := range contributions {
		if c, ok := cRaw.(map[string]any); ok {
			if t, ok := c["type"].(string); ok {
				typesSeen[t] = true
			}
		}
	}

	// TS v1 parity: personaCount from known personas list.
	personaCount := 0
	if personas, ok := validInput["personas"].([]any); ok {
		personaCount = len(personas)
	}

	// TS v1 parity: iteration — total number of contributions in session.
	iteration := len(contributions)

	// TS v1 parity: nextContributionNeeded — true when not at reflection stage (final stage).
	nextContributionNeeded := currentStage != "reflection"

	// TS v1 parity: activePersonaId / nextPersonaId — derived from last contribution persona
	// and knownPersonas rotation (approximate; Go uses string personas not {id,name} objects).
	var activePersonaId string
	var nextPersonaId string
	if len(contributions) > 0 {
		if last, ok := contributions[len(contributions)-1].(map[string]any); ok {
			activePersonaId, _ = last["persona"].(string)
		}
	}
	// Next persona: if knownPersonas available, rotate to next after active.
	if personas, ok := validInput["personas"].([]any); ok && len(personas) > 1 && activePersonaId != "" {
		for i, p := range personas {
			if ps, ok := p.(string); ok && ps == activePersonaId {
				next := personas[(i+1)%len(personas)]
				nextPersonaId, _ = next.(string)
				break
			}
		}
	}

	// TS v1 parity: consensus/disagreement/insight/question point counts from contribution types.
	consensusPointCount := 0
	for _, cRaw := range contributions {
		if c, ok := cRaw.(map[string]any); ok {
			if c["type"] == "synthesis" {
				consensusPointCount++
			}
		}
	}
	hasDisagreements := typesSeen["challenge"] || typesSeen["concern"]
	hasKeyInsights := typesSeen["insight"]
	hasOpenQuestions := typesSeen["question"]
	hasFinalRecommendation := currentStage == "decision" || currentStage == "reflection"

	// Compute per-stage contribution type frequencies and Shannon entropy.
	stageTypeCounts := map[string]map[string]int{}
	for _, cRaw := range contributions {
		if c, ok := cRaw.(map[string]any); ok {
			s, _ := c["stage"].(string)
			t, _ := c["type"].(string)
			if s == "" || t == "" {
				continue
			}
			if stageTypeCounts[s] == nil {
				stageTypeCounts[s] = map[string]int{}
			}
			stageTypeCounts[s][t]++
		}
	}
	stageTypeEntropy := make(map[string]float64, len(stageTypeCounts))
	for s, typeCounts := range stageTypeCounts {
		stageTypeEntropy[s] = ShannonEntropy(typeCounts)
	}

	lowDiversityStages := []string{}
	for s, e := range stageTypeEntropy {
		if e < 0.5 {
			lowDiversityStages = append(lowDiversityStages, s)
		}
	}

	guidanceDepth := "enriched"
	if len(contributions) == 0 {
		guidanceDepth = "basic"
	}

	data := map[string]any{
		"topic": topic,
		// TS v1 field alias.
		"stage":        currentStage,
		"currentStage": currentStage,
		// Contribution tracking.
		"contributions":     contributions,
		"contributionCount": len(contributions),
		// TS v1 parity fields.
		"personaCount":           personaCount,
		"iteration":              iteration,
		"nextContributionNeeded": nextContributionNeeded,
		"stagesCompleted":        stagesCompleted,
		"consensusPointCount":    consensusPointCount,
		"hasDisagreements":       hasDisagreements,
		"hasKeyInsights":         hasKeyInsights,
		"hasOpenQuestions":       hasOpenQuestions,
		"hasFinalRecommendation": hasFinalRecommendation,
		// Participation metrics.
		"stageProgress":           stageProgress,
		"contributionsPerPersona": participation.contributionsPerPersona,
		"silentPersonas":          participation.silentPersonas,
		"participationBalance":    participation.participationBalance,
		// Stage contribution-type diversity.
		"stageTypeEntropy":   stageTypeEntropy,
		"lowDiversityStages": lowDiversityStages,
		"guidance":           BuildGuidance("collaborative_reasoning", guidanceDepth, []string{"stage", "contribution", "personas"}),
	}
	// Only include persona rotation fields when they have values.
	if activePersonaId != "" {
		data["activePersonaId"] = activePersonaId
	}
	if nextPersonaId != "" {
		data["nextPersonaId"] = nextPersonaId
	}

	// suggestedNext: decision_framework at decision stage, reflect at reflection, self otherwise.
	suggestedNext := "collaborative_reasoning"
	if currentStage == "decision" {
		suggestedNext = "decision_framework"
	} else if currentStage == "reflection" {
		suggestedNext = "structured_argumentation"
	}

	return think.MakeThinkResult("collaborative_reasoning", data, sessionID, nil, suggestedNext, nil), nil
}

type participationResult struct {
	contributionsPerPersona map[string]int
	silentPersonas          []string
	participationBalance    float64
}

// computeParticipation tallies contributions by persona, identifies silent personas (0 contributions),
// and computes a balance score in [0,1]: 1 = perfectly equal, 0 = one persona has everything.
// Formula: balance = 1 - deviation / (2 * total), where deviation = sum(|count_i - avg|).
func computeParticipation(contributions []any, knownPersonas []string) participationResult {
	counts := map[string]int{}

	// Pre-seed with known personas so silent ones start at 0.
	for _, p := range knownPersonas {
		counts[p] = 0
	}

	for _, cRaw := range contributions {
		c, ok := cRaw.(map[string]any)
		if !ok {
			continue
		}
		persona, ok := c["persona"].(string)
		if !ok || persona == "" {
			continue
		}
		counts[persona]++
	}

	numPersonas := len(counts)
	if numPersonas == 0 {
		return participationResult{
			contributionsPerPersona: counts,
			silentPersonas:          []string{},
			participationBalance:    1.0,
		}
	}

	total := 0
	for _, v := range counts {
		total += v
	}

	var silentPersonas []string
	for persona, cnt := range counts {
		if cnt == 0 {
			silentPersonas = append(silentPersonas, persona)
		}
	}
	if silentPersonas == nil {
		silentPersonas = []string{}
	}

	balance := 1.0
	if total > 0 && numPersonas > 1 {
		avg := float64(total) / float64(numPersonas)
		deviation := 0.0
		for _, cnt := range counts {
			deviation += math.Abs(float64(cnt) - avg)
		}
		balance = math.Max(0.0, 1.0-deviation/(2.0*float64(total)))
	}

	return participationResult{
		contributionsPerPersona: counts,
		silentPersonas:          silentPersonas,
		participationBalance:    balance,
	}
}
