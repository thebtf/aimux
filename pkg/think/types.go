// Package think implements the think MCP tool with 17 reasoning patterns,
// session state management, and complexity scoring for auto-consensus mode.
package think

import "time"

// ThinkResult is the universal return type for all pattern handlers.
type ThinkResult struct {
	Pattern              string         `json:"pattern"`
	Status               string         `json:"status"`
	Timestamp            string         `json:"timestamp"`
	Data                 map[string]any `json:"data"`
	SessionID            string         `json:"session_id,omitempty"`
	Metadata             map[string]any `json:"metadata,omitempty"`
	SuggestedNextPattern string         `json:"suggestedNextPattern,omitempty"`
	ComputedFields       []string       `json:"computed_fields,omitempty"`
}

// PatternHandler is implemented by each thinking pattern.
type PatternHandler interface {
	Name() string
	Description() string
	Validate(input map[string]any) (map[string]any, error)
	Handle(validInput map[string]any, sessionID string) (*ThinkResult, error)
}

// ThinkSession holds per-session state for stateful patterns.
type ThinkSession struct {
	ID             string         `json:"id"`
	Pattern        string         `json:"pattern"`
	CreatedAt      string         `json:"createdAt"`
	LastAccessedAt string         `json:"lastAccessedAt"`
	State          map[string]any `json:"state"`
}

// ComplexityScore represents the 4-component complexity analysis.
type ComplexityScore struct {
	Total           int    `json:"total"`
	TextLength      int    `json:"textLength"`
	SubItemCount    int    `json:"subItemCount"`
	StructuralDepth int    `json:"structuralDepth"`
	PatternBias     int    `json:"patternBias"`
	Recommendation  string `json:"recommendation"`
	Threshold       int    `json:"threshold"`
}

// DialogParticipant defines a CLI + role pair for dialog mode.
type DialogParticipant struct {
	CLI  string `json:"cli"`
	Role string `json:"role"`
}

// DialogConfig defines per-pattern dialog configuration.
type DialogConfig struct {
	Participants   []DialogParticipant `json:"participants"`
	TopicTemplate  string              `json:"topicTemplate"`
	PromptTemplate string              `json:"promptTemplate"`
	MaxTurns       int                 `json:"maxTurns"`
	Mode           string              `json:"mode"`
	Synthesize     bool                `json:"synthesize"`
	ComplexityBias int                 `json:"complexityBias"`
}

// MakeThinkResult creates a new ThinkResult with the current timestamp.
func MakeThinkResult(pattern string, data map[string]any, sessionID string, metadata map[string]any, suggestedNext string, computedFields []string) *ThinkResult {
	// Create a defensive copy of data to preserve immutability
	dataCopy := make(map[string]any, len(data))
	for k, v := range data {
		dataCopy[k] = v
	}

	var metaCopy map[string]any
	if metadata != nil {
		metaCopy = make(map[string]any, len(metadata))
		for k, v := range metadata {
			metaCopy[k] = v
		}
	}

	return &ThinkResult{
		Pattern:              pattern,
		Status:               "success",
		Timestamp:            time.Now().UTC().Format(time.RFC3339),
		Data:                 dataCopy,
		SessionID:            sessionID,
		Metadata:             metaCopy,
		SuggestedNextPattern: suggestedNext,
		ComputedFields:       computedFields,
	}
}

// MakeErrorResult creates a failed ThinkResult.
func MakeErrorResult(pattern string, errMsg string) *ThinkResult {
	return &ThinkResult{
		Pattern:   pattern,
		Status:    "failed",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      map[string]any{"error": errMsg},
	}
}
