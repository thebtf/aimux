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

	// Spawn is an exact in-process CLI launch carrier. It is never serialized;
	// metadata remains a backwards-compatible fallback for older callers.
	Spawn *SpawnArgs `json:"-"`
}

// SpawnArgsFromMessage returns an owned exact launch specification. The typed
// carrier wins; metadata decoding is retained only for compatibility.
func SpawnArgsFromMessage(msg Message) SpawnArgs {
	if msg.Spawn != nil {
		return cloneSpawnArgs(*msg.Spawn)
	}
	args := SpawnArgs{Stdin: msg.Content}
	if msg.Metadata == nil {
		return args
	}
	if value, ok := msg.Metadata["command"].(string); ok {
		args.Command = value
	}
	switch value := msg.Metadata["args"].(type) {
	case []string:
		args.Args = append([]string(nil), value...)
	case []any:
		for _, item := range value {
			if arg, ok := item.(string); ok {
				args.Args = append(args.Args, arg)
			}
		}
	}
	if value, ok := msg.Metadata["cwd"].(string); ok {
		args.CWD = value
	}
	if value, ok := msg.Metadata["stdin"].(string); ok {
		args.Stdin = value
	}
	switch value := msg.Metadata["timeout"].(type) {
	case int:
		args.TimeoutSeconds = value
	case int64:
		args.TimeoutSeconds = int(value)
	case float64:
		args.TimeoutSeconds = int(value)
	}
	if value, ok := msg.Metadata["completion_pattern"].(string); ok {
		args.CompletionPattern = value
	}
	switch value := msg.Metadata["env"].(type) {
	case map[string]string:
		args.Env = cloneStringMap(value)
	case map[string]any:
		args.Env = make(map[string]string, len(value))
		for key, item := range value {
			if text, ok := item.(string); ok {
				args.Env[key] = text
			}
		}
	}
	return args
}

func cloneSpawnArgs(args SpawnArgs) SpawnArgs {
	args.Args = append([]string(nil), args.Args...)
	args.Env = cloneStringMap(args.Env)
	args.EnvList = append([]string(nil), args.EnvList...)
	return args
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
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

	// Stderr, Partial, and Error preserve legacy CLI terminal semantics while
	// the ExecutorV2 migration remains additive.
	Stderr  string      `json:"stderr,omitempty"`
	Partial bool        `json:"partial,omitempty"`
	Error   *TypedError `json:"error,omitempty"`

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
