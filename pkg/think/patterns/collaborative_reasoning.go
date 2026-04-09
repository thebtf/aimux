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

	if v, ok := input["contribution"]; ok {
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

	guidanceDepth := "enriched"
	if len(contributions) == 0 {
		guidanceDepth = "basic"
	}

	data := map[string]any{
		"topic":                   topic,
		"currentStage":            currentStage,
		"contributions":           contributions,
		"contributionCount":       len(contributions),
		"stageProgress":           stageProgress,
		"contributionsPerPersona": participation.contributionsPerPersona,
		"silentPersonas":          participation.silentPersonas,
		"participationBalance":    participation.participationBalance,
		"guidance":                BuildGuidance("collaborative_reasoning", guidanceDepth, []string{"stage", "contribution", "personas"}),
	}

	return think.MakeThinkResult("collaborative_reasoning", data, sessionID, nil, "", nil), nil
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
