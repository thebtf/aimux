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

	// Flat param detection: argument_type present → build argument from flat params.
	if _, hasFlat := input["argument_type"]; hasFlat {
		argType, ok := input["argument_type"].(string)
		if !ok || !validArgumentTypes[argType] {
			return nil, fmt.Errorf("field 'argument_type' must be one of: claim, evidence, rebuttal")
		}
		text, ok := input["argument_text"].(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("field 'argument_text' must be a non-empty string")
		}
		validatedArg := map[string]any{"type": argType, "text": text}
		if supportsClaimId, ok := input["supports_claim_id"].(string); ok && supportsClaimId != "" {
			validatedArg["supportsClaimId"] = supportsClaimId
		}
		validated["argument"] = validatedArg
	} else if v, ok := input["argument"]; ok {
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

func (p *structuredArgumentationPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"topic":             {Type: "string", Required: true, Description: "The topic being argued"},
		"argument_type":     {Type: "enum", Required: false, Description: "Type of argument being added", EnumValues: []string{"claim", "evidence", "rebuttal"}},
		"argument_text":     {Type: "string", Required: false, Description: "Text of the argument (required when argument_type is set)"},
		"supports_claim_id": {Type: "string", Required: false, Description: "ID of the claim this evidence or rebuttal supports"},
	}
}

func (p *structuredArgumentationPattern) Category() string { return "solo" }

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

	// Build unsupportedClaims as structured objects (TS v1 parity: Array<{id, text}>).
	var unsupportedClaims []map[string]any
	for _, aRaw := range arguments {
		a, ok := aRaw.(map[string]any)
		if !ok || a["type"] != "claim" {
			continue
		}
		id, ok := a["id"].(string)
		if !ok {
			continue
		}
		if !supportedClaimIDs[id] {
			unsupportedClaims = append(unsupportedClaims, map[string]any{
				"id":   id,
				"text": a["text"],
			})
		}
	}

	// Compute claimStrengths: per-claim ratio of evidence / (evidence + rebuttals).
	// Matches TS v1: strength = evidenceCount / (evidenceCount + rebuttalCount), 0 if no evidence.
	evidencePerClaim := map[string]int{}
	rebuttalPerClaim := map[string]int{}
	for _, aRaw := range arguments {
		a, ok := aRaw.(map[string]any)
		if !ok {
			continue
		}
		ref, _ := a["supportsClaimId"].(string)
		if ref == "" {
			continue
		}
		switch a["type"] {
		case "evidence":
			evidencePerClaim[ref]++
		case "rebuttal":
			rebuttalPerClaim[ref]++
		}
	}
	claimStrengths := map[string]float64{}
	for id := range claimIDs {
		ev := evidencePerClaim[id]
		rb := rebuttalPerClaim[id]
		if ev == 0 {
			claimStrengths[id] = 0.0
		} else {
			claimStrengths[id] = float64(ev) / float64(ev+rb)
		}
	}

	guidanceDepth := "enriched"
	if len(arguments) == 0 {
		guidanceDepth = "basic"
	}

	data := map[string]any{
		"topic":      topic,
		"arguments":  arguments,
		"session_id": sessionID,
		// Flat counts (Go-native, preferred by MCP callers).
		"claimCount":    claimCount,
		"evidenceCount": evidenceCount,
		"rebuttalCount": rebuttalCount,
		// TS v1 parity: argumentCount envelope.
		"argumentCount": map[string]any{
			"claim":    claimCount,
			"evidence": evidenceCount,
			"rebuttal": rebuttalCount,
			"total":    len(arguments),
		},
		// TS v1 parity: structured unsupportedClaims with id+text.
		"unsupportedClaims": unsupportedClaims,
		// TS v1 parity: per-claim strength ratios.
		"claimStrengths": claimStrengths,
		"guidance":       BuildGuidance("structured_argumentation", guidanceDepth, []string{"argument"}),
	}

	// suggestedNext: decision_framework when claims are being evaluated (TS v1 implied terminal → self).
	suggestedNext := ""
	if claimCount > 0 {
		suggestedNext = "decision_framework"
	}

	result := think.MakeThinkResult("structured_argumentation", data, sessionID, nil, suggestedNext, nil)

	// R5-3: single unsupported claim warning — when session has exactly 1 claim and 0 evidence/rebuttals,
	// flag it as load-bearing risk in the summary.
	if claimCount == 1 && evidenceCount == 0 && rebuttalCount == 0 {
		result.Summary = "[structured_argumentation] 1 claim, 0 evidence, 0 rebuttals" +
			" | warning: single unsupported claim — add evidence/rebuttals before treating as load-bearing"
	}

	return result, nil
}
