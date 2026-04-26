package types

import "time"

// Message is the unified input to any Executor (CLI or API).
// Higher layers construct Messages; Executors translate them to backend-specific calls.
type Message struct {
	// Content is the prompt text (required).
	Content string `json:"content"`

	// SystemPrompt is an optional system/role prompt prepended to the conversation.
	SystemPrompt string `json:"system_prompt,omitempty"`

	// History provides prior conversation turns for stateful executors.
	// Stateless executors ignore this field.
	History []Turn `json:"history,omitempty"`

	// Metadata carries optional parameters: model override, timeout, effort level,
	// environment variables, working directory, etc. Keys are executor-specific.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Turn represents one turn in a conversation history.
type Turn struct {
	Role    string `json:"role"`    // "user", "assistant", "system"
	Content string `json:"content"` // turn text
}

// Response is the unified output from any Executor.
// CLI executors populate ExitCode; API executors populate TokensUsed.
// Zero-value fields for inapplicable aspects (CLI: TokensUsed={0,0}, API: ExitCode=0).
type Response struct {
	// Content is the response text (required).
	Content string `json:"content"`

	// ExitCode is the CLI process exit code. Zero-value (0) for API executors.
	ExitCode int `json:"exit_code,omitempty"`

	// TokensUsed tracks input/output token consumption. Zero-value for CLI executors.
	TokensUsed TokenCount `json:"tokens_used,omitempty"`

	// Duration is the wall-clock time for the request.
	Duration time.Duration `json:"duration"`

	// Metadata carries executor-specific output: model used, cost, warnings, etc.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TokenCount tracks token consumption for API executors.
type TokenCount struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// Total returns the sum of input and output tokens.
func (t TokenCount) Total() int {
	return t.Input + t.Output
}

// Chunk is a streaming fragment from SendStream.
type Chunk struct {
	// Content is the text fragment.
	Content string `json:"content"`

	// Done signals the final chunk (Content may be empty).
	Done bool `json:"done"`
}
