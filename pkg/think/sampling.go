package think

import "context"

// SamplingMessage represents a message in a sampling request.
type SamplingMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

// SamplingProvider allows think patterns to request LLM completions
// from the MCP client via the sampling protocol.
type SamplingProvider interface {
	// RequestSampling sends messages to the client's LLM and returns the response text.
	// maxTokens limits the response length. Returns error if sampling is unavailable.
	RequestSampling(ctx context.Context, messages []SamplingMessage, maxTokens int) (string, error)
}

// SamplingAwareHandler is implemented by patterns that can use LLM sampling
// to enhance their analysis beyond pure computation.
type SamplingAwareHandler interface {
	PatternHandler
	// SetSampling injects the sampling provider. Called once during server init.
	// If never called, the pattern operates in computation-only mode.
	SetSampling(provider SamplingProvider)
}
