package patterns

import (
	"fmt"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

var validArgumentTypes = map[string]bool{
	"claim": true, "evidence": true, "rebuttal": true,
}

type structuredArgumentationPattern struct{}

// NewStructuredArgumentationPattern returns the "structured_argumentation" pattern handler.
func NewStructuredArgumentationPattern() think.PatternHandler {
	return &structuredArgumentationPattern{}
}

func (p *structuredArgumentationPattern) Name() string { return "structured_argumentation" }

func (p *structuredArgumentationPattern) Description() string {
	return "Build structured arguments with claims, evidence, and rebuttals"
}

func (p *structuredArgumentationPattern) Validate(input map[string]any) (map[string]any, error) {
	topicRaw, ok := input["topic"]
	if !ok {
		return nil, fmt.Errorf("missing required field: topic")
	}
	topic, ok := topicRaw.(string)
	if !ok || topic == "" {
		return nil, fmt.Errorf("field 'topic' must be a non-empty string")
	}

	validated := map[string]any{"topic": topic}

	if v, ok := input["argument"]; ok {
		arg, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field 'argument' must be a map")
		}
		argType, ok := arg["type"].(string)
		if !ok || !validArgumentTypes[argType] {
			return nil, fmt.Errorf("argument 'type' must be one of: claim, evidence, rebuttal")
		}
		text, ok := arg["text"].(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("argument 'text' must be a non-empty string")
		}
		validatedArg := map[string]any{"type": argType, "text": text}
		if supportsClaimId, ok := arg["supportsClaimId"].(string); ok {
			validatedArg["supportsClaimId"] = supportsClaimId
		}
		validated["argument"] = validatedArg
	}

	return validated, nil
}

func (p *structuredArgumentationPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	sess := think.GetOrCreateSession(sessionID, "structured_argumentation", map[string]any{
		"arguments": []any{},
	})

	arguments, _ := sess.State["arguments"].([]any)

	if argRaw, ok := validInput["argument"]; ok {
		arg := argRaw.(map[string]any)
		argType := arg["type"].(string)

		// Validate supportsClaimId reference
		if supportsClaimId, ok := arg["supportsClaimId"].(string); ok && supportsClaimId != "" {
			if argType == "evidence" || argType == "rebuttal" {
				found := false
				for _, existing := range arguments {
					if em, ok := existing.(map[string]any); ok {
						if em["id"] == supportsClaimId {
							found = true
							break
						}
					}
				}
				if !found {
					return nil, fmt.Errorf("supportsClaimId references non-existent argument: %s", supportsClaimId)
				}
			}
		}

		argID := fmt.Sprintf("A-%d", len(arguments)+1)
		entry := map[string]any{
			"id":        argID,
			"type":      argType,
			"text":      arg["text"],
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
		if supportsClaimId, ok := arg["supportsClaimId"].(string); ok {
			entry["supportsClaimId"] = supportsClaimId
		}
		arguments = append(arguments, entry)
	}

	think.UpdateSessionState(sessionID, map[string]any{
		"arguments": arguments,
	})

	topic := validInput["topic"].(string)

	claimCount := 0
	evidenceCount := 0
	rebuttalCount := 0
	claimIDs := map[string]bool{}
	supportedClaimIDs := map[string]bool{}

	for _, aRaw := range arguments {
		a, ok := aRaw.(map[string]any)
		if !ok {
			continue
		}
		switch a["type"] {
		case "claim":
			claimCount++
			if id, ok := a["id"].(string); ok {
				claimIDs[id] = true
			}
		case "evidence":
			evidenceCount++
			if ref, ok := a["supportsClaimId"].(string); ok && ref != "" {
				supportedClaimIDs[ref] = true
			}
		case "rebuttal":
			rebuttalCount++
		}
	}

	var unsupportedClaims []string
	for id := range claimIDs {
		if !supportedClaimIDs[id] {
			unsupportedClaims = append(unsupportedClaims, id)
		}
	}

	data := map[string]any{
		"topic":             topic,
		"arguments":         arguments,
		"claimCount":        claimCount,
		"evidenceCount":     evidenceCount,
		"rebuttalCount":     rebuttalCount,
		"unsupportedClaims": unsupportedClaims,
	}

	return think.MakeThinkResult("structured_argumentation", data, sessionID, nil, "", nil), nil
}
