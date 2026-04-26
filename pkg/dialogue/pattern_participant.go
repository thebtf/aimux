package dialogue

import (
	"context"
	"fmt"
	"strings"
)

// PatternParticipant wraps a think pattern as a Participant.
// Any pattern whose Handle signature is func(map[string]any) (map[string]any, error)
// can participate in a dialogue without modification.
//
// The handleFn receives a flat map containing:
//   - "thought": the current prompt (primary input)
//   - "prompt":  same value (alias for patterns that expect "prompt")
//   - "history": newline-separated prior turn summaries
//
// The function should return a map with at least one of the well-known
// text fields ("content", "output", "thought", "result") as its primary
// output. PatternParticipant extracts the first non-empty value it finds.
type PatternParticipant struct {
	patternName string
	handleFn    func(input map[string]any) (map[string]any, error)
	role        string
}

// NewPatternParticipant creates a Participant backed by a think pattern handler.
// name is the pattern's identifier (e.g., "peer_review"); role is the
// human-readable label (e.g., "critic"). handleFn is called on every turn.
func NewPatternParticipant(name string, handleFn func(map[string]any) (map[string]any, error)) *PatternParticipant {
	return &PatternParticipant{
		patternName: name,
		handleFn:    handleFn,
		role:        "pattern",
	}
}

// NewPatternParticipantWithRole creates a Participant backed by a think pattern
// handler with an explicit role label.
func NewPatternParticipantWithRole(name, role string, handleFn func(map[string]any) (map[string]any, error)) *PatternParticipant {
	return &PatternParticipant{
		patternName: name,
		handleFn:    handleFn,
		role:        role,
	}
}

// Name implements Participant.
func (p *PatternParticipant) Name() string { return p.patternName }

// Role implements Participant.
func (p *PatternParticipant) Role() string { return p.role }

// Respond calls the wrapped pattern handler with prompt and history context,
// then extracts the response text from the result map.
func (p *PatternParticipant) Respond(_ context.Context, prompt string, history []DialogueTurn) (string, error) {
	input := buildPatternInput(prompt, history)

	result, err := p.handleFn(input)
	if err != nil {
		return "", fmt.Errorf("pattern participant %q: %w", p.patternName, err)
	}

	text := extractTextFromResult(result)
	if text == "" {
		return fmt.Sprintf("[%s: no text output]", p.patternName), nil
	}

	return text, nil
}

// buildPatternInput constructs the input map for a pattern handler.
// It uses "thought" as the primary field because it is required by the
// largest number of patterns; "prompt" is included as an alias.
func buildPatternInput(prompt string, history []DialogueTurn) map[string]any {
	input := map[string]any{
		"thought": prompt,
		"prompt":  prompt,
	}

	if len(history) > 0 {
		var sb strings.Builder
		for _, t := range history {
			// Use XML-style structured delimiters to prevent injection via
			// fabricated "[OtherParticipant]: ..." patterns in output.
			sb.WriteString(fmt.Sprintf("<dialogue-turn participant=%q>\n%s\n</dialogue-turn>\n",
				sanitizeName(t.Participant), t.Content))
		}
		input["history"] = sb.String()
	}

	return input
}

// extractTextFromResult searches a result map for the first non-empty string
// value under the well-known output keys.
func extractTextFromResult(result map[string]any) string {
	// Priority order: most specific → most generic
	keys := []string{
		"content",
		"output",
		"response",
		"result",
		"thought",
		"analysis",
		"summary",
		"text",
	}

	for _, key := range keys {
		if v, ok := result[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}

	// Fall back to any string value in the map.
	for _, v := range result {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}

	return ""
}
