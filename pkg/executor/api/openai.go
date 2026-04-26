package api

import (
	"context"
	"fmt"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/thebtf/aimux/pkg/types"
)

// Compile-time interface check.
var _ types.ExecutorV2 = (*OpenAIExecutor)(nil)

// OpenAIExecutor sends prompts to the OpenAI Chat Completions API.
type OpenAIExecutor struct {
	base   *baseExecutor
	client *openai.Client
}

// NewOpenAI creates a new OpenAIExecutor.  apiKey must be non-empty; if model
// is empty, DefaultOpenAIModel ("gpt-4o") is used.
func NewOpenAI(apiKey, model string) (*OpenAIExecutor, error) {
	if model == "" {
		model = DefaultOpenAIModel
	}
	base, err := newBase(apiKey, model)
	if err != nil {
		return nil, err
	}
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIExecutor{base: base, client: &client}, nil
}

// Info returns static metadata about this executor.
func (e *OpenAIExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "openai",
		Type: types.ExecutorTypeAPI,
		Capabilities: types.ExecutorCapabilities{
			Streaming: true,
			Tools:     true,
		},
	}
}

// Send sends a single message to the OpenAI API and returns the full response.
func (e *OpenAIExecutor) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	if !e.base.alive.Load() {
		return nil, fmt.Errorf("openai executor: already closed")
	}

	ctx, cancel := context.WithTimeout(ctx, e.base.timeout)
	defer cancel()

	messages := buildOpenAIMessages(msg)

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(e.base.model),
		Messages: messages,
	}

	start := time.Now()
	resp, err := e.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai: chat completion failed: %w", err)
	}

	content := ""
	finishReason := ""
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
		finishReason = string(resp.Choices[0].FinishReason)
	}

	return &types.Response{
		Content:  content,
		ExitCode: 0,
		TokensUsed: types.TokenCount{
			Input:  int(resp.Usage.PromptTokens),
			Output: int(resp.Usage.CompletionTokens),
		},
		Duration: time.Since(start),
		Metadata: map[string]any{
			"model": resp.Model,
			"id":    resp.ID,
			"stop":  finishReason,
		},
	}, nil
}

// SendStream sends a message and invokes onChunk for each streaming delta.
// The final accumulated Response is returned after streaming completes.
func (e *OpenAIExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	if !e.base.alive.Load() {
		return nil, fmt.Errorf("openai executor: already closed")
	}

	ctx, cancel := context.WithTimeout(ctx, e.base.timeout)
	defer cancel()

	messages := buildOpenAIMessages(msg)

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(e.base.model),
		Messages: messages,
	}

	start := time.Now()
	stream := e.client.Chat.Completions.NewStreaming(ctx, params)

	var full string
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta.Content
			if delta != "" {
				full += delta
				onChunk(types.Chunk{Content: delta, Done: false})
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("openai: streaming failed: %w", err)
	}
	onChunk(types.Chunk{Done: true})

	return &types.Response{
		Content:  full,
		ExitCode: 0,
		Duration: time.Since(start),
	}, nil
}

// IsAlive reports whether the executor is still operational.
func (e *OpenAIExecutor) IsAlive() types.HealthStatus {
	return e.base.isAlive()
}

// Close permanently shuts down the executor.  Subsequent calls to Send or
// SendStream will return an error.
func (e *OpenAIExecutor) Close() error {
	return e.base.close()
}

// buildOpenAIMessages converts a types.Message (with optional system prompt and
// history) into the OpenAI SDK message union slice.
func buildOpenAIMessages(msg types.Message) []openai.ChatCompletionMessageParamUnion {
	var msgs []openai.ChatCompletionMessageParamUnion

	// System prompt goes first when provided.
	if msg.SystemPrompt != "" {
		msgs = append(msgs, openai.ChatCompletionMessageParamUnion{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{
					OfString: param.NewOpt(msg.SystemPrompt),
				},
			},
		})
	}

	// Replay conversation history.
	for _, turn := range buildHistory(msg.History) {
		switch turn.Role {
		case "assistant":
			msgs = append(msgs, openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content: openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: param.NewOpt(turn.Content),
					},
				},
			})
		default: // "user" and any unknown role → user message
			msgs = append(msgs, openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: param.NewOpt(turn.Content),
					},
				},
			})
		}
	}

	// Current user message.
	msgs = append(msgs, openai.ChatCompletionMessageParamUnion{
		OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{
				OfString: param.NewOpt(msg.Content),
			},
		},
	})

	return msgs
}
