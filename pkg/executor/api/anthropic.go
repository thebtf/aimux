package api

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/thebtf/aimux/pkg/types"
)

// Compile-time interface check.
var _ types.ExecutorV2 = (*AnthropicExecutor)(nil)

// AnthropicExecutor sends prompts to the Anthropic Messages API.
type AnthropicExecutor struct {
	base   *baseExecutor
	client *anthropic.Client
}

// maxTokensDefault is the default max_tokens value required by the Anthropic
// API.  Callers can override this via Metadata["max_tokens"] in future.
const maxTokensDefault = 8192

// NewAnthropic creates a new AnthropicExecutor.  apiKey must be non-empty; if
// model is empty, DefaultAnthropicModel is used.
func NewAnthropic(apiKey, model string) (*AnthropicExecutor, error) {
	if model == "" {
		model = DefaultAnthropicModel
	}
	base, err := newBase(apiKey, model)
	if err != nil {
		return nil, err
	}
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicExecutor{base: base, client: &client}, nil
}

// Info returns static metadata about this executor.
func (e *AnthropicExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "anthropic",
		Type: types.ExecutorTypeAPI,
		Capabilities: types.ExecutorCapabilities{
			Streaming: true,
			Tools:     true,
		},
	}
}

// Send sends a single message to the Anthropic API and returns the full
// response.
func (e *AnthropicExecutor) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	if !e.base.alive.Load() {
		return nil, fmt.Errorf("anthropic executor: already closed")
	}

	ctx, cancel := context.WithTimeout(ctx, e.base.timeout)
	defer cancel()

	params := buildAnthropicParams(e.base.model, msg)

	start := time.Now()
	resp, err := e.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic: message creation failed: %w", err)
	}

	content := extractAnthropicText(resp.Content)

	return &types.Response{
		Content:  content,
		ExitCode: 0,
		TokensUsed: types.TokenCount{
			Input:  int(resp.Usage.InputTokens),
			Output: int(resp.Usage.OutputTokens),
		},
		Duration: time.Since(start),
		Metadata: map[string]any{
			"model": string(resp.Model),
			"id":    resp.ID,
		},
	}, nil
}

// SendStream streams a message and invokes onChunk for each text delta.
func (e *AnthropicExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	if !e.base.alive.Load() {
		return nil, fmt.Errorf("anthropic executor: already closed")
	}

	ctx, cancel := context.WithTimeout(ctx, e.base.timeout)
	defer cancel()

	params := buildAnthropicParams(e.base.model, msg)

	start := time.Now()
	stream := e.client.Messages.NewStreaming(ctx, params)

	acc := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		if err := acc.Accumulate(event); err != nil {
			return nil, fmt.Errorf("anthropic: accumulate error: %w", err)
		}
		// Emit text deltas as they arrive.
		if delta, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if textDelta, ok2 := delta.Delta.AsAny().(anthropic.TextDelta); ok2 && textDelta.Text != "" {
				onChunk(types.Chunk{Content: textDelta.Text, Done: false})
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("anthropic: streaming failed: %w", err)
	}
	onChunk(types.Chunk{Done: true})

	content := extractAnthropicText(acc.Content)
	return &types.Response{
		Content:  content,
		ExitCode: 0,
		TokensUsed: types.TokenCount{
			Input:  int(acc.Usage.InputTokens),
			Output: int(acc.Usage.OutputTokens),
		},
		Duration: time.Since(start),
	}, nil
}

// IsAlive reports whether the executor is still operational.
func (e *AnthropicExecutor) IsAlive() types.HealthStatus {
	return e.base.isAlive()
}

// Close permanently shuts down the executor.
func (e *AnthropicExecutor) Close() error {
	return e.base.close()
}

// buildAnthropicParams constructs MessageNewParams from a types.Message.
func buildAnthropicParams(model string, msg types.Message) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokensDefault,
	}

	// System prompt.
	if msg.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: msg.SystemPrompt},
		}
	}

	// History + current message.
	var messages []anthropic.MessageParam
	for _, turn := range buildHistory(msg.History) {
		role := anthropic.MessageParamRoleUser
		if turn.Role == "assistant" {
			role = anthropic.MessageParamRoleAssistant
		}
		messages = append(messages, anthropic.MessageParam{
			Role: role,
			Content: []anthropic.ContentBlockParamUnion{
				{OfText: &anthropic.TextBlockParam{Text: turn.Content}},
			},
		})
	}
	messages = append(messages, anthropic.MessageParam{
		Role: anthropic.MessageParamRoleUser,
		Content: []anthropic.ContentBlockParamUnion{
			{OfText: &anthropic.TextBlockParam{Text: msg.Content}},
		},
	})
	params.Messages = messages

	return params
}

// extractAnthropicText extracts the concatenated text from a response content
// block slice.
func extractAnthropicText(blocks []anthropic.ContentBlockUnion) string {
	var out string
	for _, block := range blocks {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			out += tb.Text
		}
	}
	return out
}
