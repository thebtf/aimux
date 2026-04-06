package patterns

import (
	"fmt"
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

	topic := validInput["topic"].(string)
	data := map[string]any{
		"topic":             topic,
		"currentStage":      currentStage,
		"contributions":     contributions,
		"contributionCount": len(contributions),
		"stageProgress":     stageProgress,
	}

	return think.MakeThinkResult("collaborative_reasoning", data, sessionID, nil, "", nil), nil
}
