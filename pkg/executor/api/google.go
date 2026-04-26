package api

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/genai"

	"github.com/thebtf/aimux/pkg/types"
)

// Compile-time interface check.
var _ types.ExecutorV2 = (*GoogleAIExecutor)(nil)

// GoogleAIExecutor sends prompts to the Google AI (Gemini) API.
type GoogleAIExecutor struct {
	base   *baseExecutor
	client *genai.Client
}

// NewGoogleAI creates a new GoogleAIExecutor.  apiKey must be non-empty; if
// model is empty, DefaultGoogleAIModel ("gemini-2.0-flash") is used.
func NewGoogleAI(apiKey, model string) (*GoogleAIExecutor, error) {
	if model == "" {
		model = DefaultGoogleAIModel
	}
	base, err := newBase(apiKey, model)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("google ai: failed to create client: %w", err)
	}
	return &GoogleAIExecutor{base: base, client: client}, nil
}

// Info returns static metadata about this executor.
func (e *GoogleAIExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "google",
		Type: types.ExecutorTypeAPI,
		Capabilities: types.ExecutorCapabilities{
			Streaming: true,
			Tools:     true,
			Images:    true,
		},
	}
}

// Send sends a single message to the Google AI API and returns the full
// response.
func (e *GoogleAIExecutor) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	if !e.base.alive.Load() {
		return nil, fmt.Errorf("google ai executor: already closed")
	}

	ctx, cancel := context.WithTimeout(ctx, e.base.timeout)
	defer cancel()

	cfg, contents := buildGoogleAIRequest(msg)

	start := time.Now()
	result, err := e.client.Models.GenerateContent(ctx, e.base.model, contents, cfg)
	if err != nil {
		return nil, fmt.Errorf("google ai: generate content failed: %w", err)
	}

	content := result.Text()
	var inputTokens, outputTokens int
	if result.UsageMetadata != nil {
		inputTokens = int(result.UsageMetadata.PromptTokenCount)
		outputTokens = int(result.UsageMetadata.CandidatesTokenCount)
	}

	return &types.Response{
		Content:  content,
		ExitCode: 0,
		TokensUsed: types.TokenCount{
			Input:  inputTokens,
			Output: outputTokens,
		},
		Duration: time.Since(start),
		Metadata: map[string]any{
			"model": result.ModelVersion,
		},
	}, nil
}

// SendStream streams content from the Google AI API, invoking onChunk for each
// partial response.
func (e *GoogleAIExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	if !e.base.alive.Load() {
		return nil, fmt.Errorf("google ai executor: already closed")
	}

	ctx, cancel := context.WithTimeout(ctx, e.base.timeout)
	defer cancel()

	cfg, contents := buildGoogleAIRequest(msg)

	start := time.Now()
	var full string
	var inputTokens, outputTokens int

	for result, err := range e.client.Models.GenerateContentStream(ctx, e.base.model, contents, cfg) {
		if err != nil {
			return nil, fmt.Errorf("google ai: streaming failed: %w", err)
		}
		delta := result.Text()
		if delta != "" {
			full += delta
			onChunk(types.Chunk{Content: delta, Done: false})
		}
		if result.UsageMetadata != nil {
			inputTokens = int(result.UsageMetadata.PromptTokenCount)
			outputTokens = int(result.UsageMetadata.CandidatesTokenCount)
		}
	}
	onChunk(types.Chunk{Done: true})

	return &types.Response{
		Content:  full,
		ExitCode: 0,
		TokensUsed: types.TokenCount{
			Input:  inputTokens,
			Output: outputTokens,
		},
		Duration: time.Since(start),
	}, nil
}

// IsAlive reports whether the executor is still operational.
func (e *GoogleAIExecutor) IsAlive() types.HealthStatus {
	return e.base.isAlive()
}

// Close permanently shuts down the executor.
func (e *GoogleAIExecutor) Close() error {
	return e.base.close()
}

// buildGoogleAIRequest converts a types.Message into the config + contents
// pair expected by the Google AI SDK.
func buildGoogleAIRequest(msg types.Message) (*genai.GenerateContentConfig, []*genai.Content) {
	var cfg *genai.GenerateContentConfig
	if msg.SystemPrompt != "" {
		cfg = &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: msg.SystemPrompt}},
			},
		}
	}

	// Build history + current message as a Contents slice.
	var contents []*genai.Content
	for _, turn := range buildHistory(msg.History) {
		role := genai.RoleUser
		if turn.Role == "assistant" {
			role = genai.RoleModel
		}
		contents = append(contents, &genai.Content{
			Role:  role,
			Parts: []*genai.Part{{Text: turn.Content}},
		})
	}
	contents = append(contents, &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: msg.Content}},
	})

	return cfg, contents
}
